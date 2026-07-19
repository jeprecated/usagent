package zai

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/model"
	"github.com/jeprecated/usagent/internal/providers"
	"github.com/jeprecated/usagent/internal/ratelimit"
)

const defaultEndpoint = "https://api.z.ai/api/monitor/usage/quota/limit"

type Provider struct {
	cfg    config.ZAIConfig
	client *http.Client
}

func New(cfg config.ZAIConfig) *Provider { return NewWithClient(cfg, http.DefaultClient) }
func NewWithClient(cfg config.ZAIConfig, c *http.Client) *Provider {
	if c == nil {
		c = http.DefaultClient
	}
	return &Provider{cfg: cfg, client: c}
}
func (p *Provider) ID() string    { return "z-ai" }
func (p *Provider) Label() string { return "z.ai" }

type payload struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Limits []limit `json:"limits"`
	} `json:"data"`
}

type limit struct {
	Type          string      `json:"type"`
	Unit          float64     `json:"unit"`
	Number        float64     `json:"number"`
	Usage         *float64    `json:"usage"`
	CurrentValue  *float64    `json:"currentValue"`
	Remaining     *float64    `json:"remaining"`
	Percentage    *float64    `json:"percentage"`
	NextResetTime *jsonNumber `json:"nextResetTime"`
	UsageDetails  any         `json:"usageDetails"`
}

type jsonNumber int64

func (n *jsonNumber) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	var v int64
	_, err := fmt.Sscanf(s, "%d", &v)
	if err != nil {
		return err
	}
	*n = jsonNumber(v)
	return nil
}

func (p *Provider) Fetch(ctx context.Context, now time.Time) (providers.Result, error) {
	token, envName := p.token()
	if token == "" {
		return providers.Result{}, fmt.Errorf("z.ai API token missing: set %s", envName)
	}
	endpoint := p.cfg.EndpointURL
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return providers.Result{}, err
	}
	p.authorize(req, token)
	req.Header.Set("accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return providers.Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retry := ratelimit.RetryAfter(resp.Header, now)
		return providers.Result{RetryAfter: retry}, providers.HTTPStatusError("z.ai quota", resp.StatusCode, retry, resp.Body)
	}
	var pl payload
	if err := json.NewDecoder(resp.Body).Decode(&pl); err != nil {
		return providers.Result{}, err
	}
	if pl.Code != 0 && pl.Code != 200 {
		if pl.Msg == "" {
			pl.Msg = "API error"
		}
		return providers.Result{}, fmt.Errorf("z.ai quota returned code %d: %s", pl.Code, pl.Msg)
	}
	return providers.Result{Items: Normalize(pl.Data.Limits, p.cfg, now)}, nil
}

func (p *Provider) token() (string, string) {
	envName := p.cfg.TokenEnv
	if envName == "" {
		envName = "ZAI_API_KEY"
	}
	if v := os.Getenv(envName); v != "" {
		return v, envName
	}
	for _, fallback := range p.cfg.TokenEnvFallbacks {
		if v := os.Getenv(fallback); v != "" {
			return v, fallback
		}
	}
	if envName != "GLM_API_KEY" {
		if v := os.Getenv("GLM_API_KEY"); v != "" {
			return v, "GLM_API_KEY"
		}
	}
	return "", envName
}

func (p *Provider) authorize(req *http.Request, token string) {
	scheme := strings.ToLower(p.cfg.AuthScheme)
	if scheme == "" {
		scheme = "bearer"
	}
	header := p.cfg.AuthHeader
	if header == "" {
		header = "Authorization"
	}
	switch scheme {
	case "none":
		return
	case "header":
		req.Header.Set(header, token)
	default:
		req.Header.Set(header, "Bearer "+token)
	}
}

func Normalize(limits []limit, cfg config.ZAIConfig, now time.Time) []model.QuotaItem {
	excluded := map[string]bool{"TIME_LIMIT": true}
	if len(cfg.ExcludeLimitTypes) > 0 {
		excluded = map[string]bool{}
		for _, t := range cfg.ExcludeLimitTypes {
			excluded[strings.ToUpper(t)] = true
		}
	}
	visibleOnly := map[string]bool{}
	for _, t := range cfg.VisibleLimitTypes {
		visibleOnly[strings.ToUpper(t)] = true
	}
	items := []model.QuotaItem{}
	for _, l := range limits {
		typ := strings.ToUpper(l.Type)
		if typ == "" || excluded[typ] {
			continue
		}
		visible := true
		if len(visibleOnly) > 0 {
			visible = visibleOnly[typ]
		}
		limitValue := 0.0
		if l.Unit > 0 && l.Number > 0 {
			limitValue = l.Unit * l.Number
		}
		used := value(l.CurrentValue, value(l.Usage, 0))
		percent := value(l.Percentage, 0)
		if l.CurrentValue == nil && l.Usage == nil && l.Percentage != nil && limitValue > 0 {
			used = limitValue * percent / 100
		}
		remaining := value(l.Remaining, math.Max(0, limitValue-used))
		if limitValue <= 0 {
			limitValue = used + remaining
		}
		if l.Percentage == nil && limitValue > 0 {
			percent = used / limitValue * 100
		}
		windowID, windowLabel, windowKind := windowForLimit(typ, l, now)
		itemID := "z-ai-" + strings.ToLower(strings.ReplaceAll(typ, "_", "-"))
		if typ == "TOKENS_LIMIT" {
			itemID += "-" + windowID
		}
		item := model.QuotaItem{ID: itemID, Provider: "z-ai", Label: "z.ai " + windowLabel, Window: model.Window{ID: windowID, Label: windowLabel, Kind: windowKind}, Unit: unitFor(typ), Limit: round2(limitValue), Used: round2(used), Remaining: round2(math.Max(0, remaining)), PercentUsed: clampPercent(percent), State: "fresh", Severity: "ok", Visible: visible}
		if l.NextResetTime != nil {
			reset := int64(*l.NextResetTime)
			item.Window.ResetAt = &reset
			item.Reset = &model.Reset{ResetAt: reset, ResetWindowID: windowID, Source: "provider"}
		}
		items = append(items, item)
	}
	return items
}

func value(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}

func windowForLimit(typ string, l limit, now time.Time) (id, label, kind string) {
	switch typ {
	case "SESSION_LIMIT":
		return "session", "S", "rolling"
	case "TOKENS_LIMIT":
		if l.NextResetTime != nil {
			reset := time.UnixMilli(int64(*l.NextResetTime))
			if reset.Sub(now) > 24*time.Hour {
				return "weekly", "W", "weekly"
			}
		}
		return "session", "S", "rolling"
	case "TIMES_LIMIT":
		return "requests", "Req", "rolling"
	case "RATE_LIMIT":
		return "rate", "Rate", "rate"
	default:
		id = strings.ToLower(strings.ReplaceAll(typ, "_LIMIT", ""))
		id = strings.ReplaceAll(id, "_", "-")
		return id, id, "custom"
	}
}

func unitFor(typ string) string {
	switch typ {
	case "TOKENS_LIMIT":
		return "M tokens"
	case "TIMES_LIMIT", "RATE_LIMIT", "SESSION_LIMIT":
		return "count"
	default:
		return "count"
	}
}

func clampPercent(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return math.Round(v)
}
func round2(v float64) float64 { return math.Round(v*100) / 100 }
