package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jmalloc/usagent/internal/config"
	"github.com/jmalloc/usagent/internal/model"
	"github.com/jmalloc/usagent/internal/providers"
)

type Provider struct {
	cfg    config.ClaudeOAuthConfig
	client *http.Client
}

func New(cfg config.ClaudeOAuthConfig) *Provider {
	return &Provider{cfg: cfg, client: http.DefaultClient}
}
func NewWithClient(cfg config.ClaudeOAuthConfig, c *http.Client) *Provider {
	if c == nil {
		c = http.DefaultClient
	}
	return &Provider{cfg: cfg, client: c}
}
func (p *Provider) ID() string    { return "claude-code" }
func (p *Provider) Label() string { return "Claude" }

type credentials struct {
	ClaudeAiOauth struct {
		AccessToken string `json:"accessToken"`
	} `json:"claudeAiOauth"`
}
type usagePayload struct {
	Limits   []limit `json:"limits"`
	FiveHour *limit  `json:"five_hour"`
	SevenDay *limit  `json:"seven_day"`
}
type limit struct {
	Kind        string   `json:"kind"`
	Percent     *float64 `json:"percent"`
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
	Severity    string   `json:"severity"`
	Scope       struct {
		Model struct {
			DisplayName string `json:"display_name"`
			ID          string `json:"id"`
		} `json:"model"`
	} `json:"scope"`
}

func (p *Provider) Fetch(ctx context.Context, now time.Time) (providers.Result, error) {
	token, err := readToken(p.cfg.CredentialsPath)
	if err != nil {
		return providers.Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.EndpointURL, nil)
	if err != nil {
		return providers.Result{}, err
	}
	req.Header.Set("authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", p.cfg.BetaHeader)
	req.Header.Set("accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return providers.Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return providers.Result{RetryAfter: retryAfter(resp.Header.Get("retry-after"), 0, now)}, fmt.Errorf("claude oauth usage returned HTTP %d", resp.StatusCode)
	}
	var payload usagePayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return providers.Result{}, err
	}
	return providers.Result{Items: Normalize(payload, now, p.cfg.RefreshMs, p.cfg.StaleMs)}, nil
}

func readToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read claude credentials: %w", err)
	}
	var c credentials
	if err := json.Unmarshal(b, &c); err != nil {
		return "", fmt.Errorf("parse claude credentials: %w", err)
	}
	if c.ClaudeAiOauth.AccessToken == "" {
		return "", errors.New("claude credentials missing claudeAiOauth.accessToken")
	}
	return c.ClaudeAiOauth.AccessToken, nil
}

func Normalize(payload usagePayload, now time.Time, refreshMs, staleMs int64) []model.QuotaItem {
	if refreshMs <= 0 {
		refreshMs = int64((5 * time.Minute) / time.Millisecond)
	}
	if staleMs <= 0 {
		staleMs = int64((15 * time.Minute) / time.Millisecond)
	}
	items := []model.QuotaItem{}
	if session := findLimit(payload, "session", false); session != nil {
		items = append(items, quotaItem("claude-code-oauth-session", "Claude 5h", "session", "S", "rolling", *session, now, refreshMs, staleMs))
	}
	if weekly := findLimit(payload, "weekly_all", false); weekly != nil {
		items = append(items, quotaItem("claude-code-oauth-weekly-all", "Claude weekly", "weekly", "W", "weekly", *weekly, now, refreshMs, staleMs))
	}
	if fable := findLimit(payload, "weekly_scoped", true); fable != nil {
		items = append(items, quotaItem("claude-code-oauth-fable-weekly", "Claude Fable weekly", "fableWeekly", "F", "weekly", *fable, now, refreshMs, staleMs))
	}
	return items
}

func findLimit(payload usagePayload, kind string, requireFable bool) *limit {
	for i := range payload.Limits {
		l := &payload.Limits[i]
		if l.Kind != kind {
			continue
		}
		if requireFable {
			name := strings.ToLower(l.Scope.Model.DisplayName + " " + l.Scope.Model.ID)
			if !strings.Contains(name, "fable") {
				continue
			}
		}
		return l
	}
	if !requireFable && kind == "session" {
		return payload.FiveHour
	}
	if !requireFable && kind == "weekly_all" {
		return payload.SevenDay
	}
	return nil
}

func quotaItem(id, label, windowID, windowLabel, windowKind string, l limit, now time.Time, refreshMs, staleMs int64) model.QuotaItem {
	used := clampPercent(percent(l))
	remaining := math.Max(0, 100-used)
	nowMs := now.UnixMilli()
	next := nowMs + refreshMs
	stale := nowMs + staleMs
	item := model.QuotaItem{ID: id, Provider: "claude-code", Label: label, Window: model.Window{ID: windowID, Label: windowLabel, Kind: windowKind}, Unit: "percent", Limit: 100, Used: used, Remaining: remaining, PercentUsed: used, State: "fresh", Severity: normalizeSeverity(l.Severity), Visible: true, Refresh: &model.Refresh{LastUpdatedAt: nowMs, Source: "provider", NextRefreshAt: next, StaleAt: stale}}
	if reset := parseResetAt(l.ResetsAt); reset != nil {
		item.Window.ResetAt = reset
		item.Reset = &model.Reset{ResetAt: *reset, ResetWindowID: windowID, Source: "provider"}
	}
	return item
}

func percent(l limit) float64 {
	if l.Percent != nil {
		return *l.Percent
	}
	if l.Utilization != nil {
		return *l.Utilization
	}
	return 0
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
func normalizeSeverity(v string) string {
	switch v {
	case "warning", "critical", "error", "ok":
		return v
	default:
		return "ok"
	}
}
func parseResetAt(v string) *int64 {
	if v == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		if ms, err2 := time.Parse("2006-01-02T15:04:05Z07:00", v); err2 == nil {
			out := ms.UnixMilli()
			return &out
		}
		return nil
	}
	out := t.UnixMilli()
	return &out
}

func retryAfter(v string, def time.Duration, now time.Time) time.Duration {
	if v == "" {
		return def
	}
	if secs, err := time.ParseDuration(v + "s"); err == nil {
		if secs < time.Second {
			return time.Second
		}
		return secs
	}
	if t, err := http.ParseTime(v); err == nil {
		d := t.Sub(now)
		if d < time.Second {
			return time.Second
		}
		return d
	}
	return def
}
