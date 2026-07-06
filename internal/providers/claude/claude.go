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
	"github.com/jmalloc/usagent/internal/ratelimit"
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
	Limits     []limit     `json:"limits"`
	FiveHour   *limit      `json:"five_hour"`
	SevenDay   *limit      `json:"seven_day"`
	ExtraUsage *extraUsage `json:"extra_usage"`
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

type extraUsage struct {
	IsEnabled      bool     `json:"is_enabled"`
	MonthlyLimit   *float64 `json:"monthly_limit"`
	UsedCredits    *float64 `json:"used_credits"`
	Utilization    *float64 `json:"utilization"`
	Currency       string   `json:"currency"`
	DisabledReason string   `json:"disabled_reason"`
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
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/json")
	if p.cfg.UserAgent != "" {
		req.Header.Set("user-agent", p.cfg.UserAgent)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return providers.Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retry := ratelimit.AnthropicRetryAfter(resp.Header, now)
		return providers.Result{RetryAfter: retry}, providers.HTTPStatusError("claude oauth usage", resp.StatusCode, retry, resp.Body)
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
	if extra := payload.ExtraUsage; extra != nil {
		if item, ok := extraUsageItem(*extra, now, refreshMs, staleMs); ok {
			items = append(items, item)
		}
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

func extraUsageItem(extra extraUsage, now time.Time, refreshMs, staleMs int64) (model.QuotaItem, bool) {
	if !extra.IsEnabled || extra.MonthlyLimit == nil || *extra.MonthlyLimit <= 0 {
		return model.QuotaItem{}, false
	}
	limit := *extra.MonthlyLimit / 100
	used := 0.0
	if extra.UsedCredits != nil {
		used = math.Max(0, *extra.UsedCredits/100)
	}
	remaining := math.Max(0, limit-used)
	percentUsed := 0.0
	if extra.Utilization != nil {
		percentUsed = clampPercent(*extra.Utilization)
	} else if limit > 0 {
		percentUsed = clampPercent(used / limit * 100)
	}
	nowMs := now.UnixMilli()
	return model.QuotaItem{
		ID:          "claude-code-oauth-extra-credits",
		Provider:    "claude-code",
		Label:       "Claude extra credits",
		Window:      model.Window{ID: "extraCredits", Label: "Extra", Kind: "monthly"},
		Unit:        currencyUnit(extra.Currency),
		Limit:       round2(limit),
		Used:        round2(used),
		Remaining:   round2(remaining),
		PercentUsed: percentUsed,
		State:       "fresh",
		Severity:    severityForPercent(percentUsed),
		Visible:     true,
		Refresh:     &model.Refresh{LastUpdatedAt: nowMs, Source: "provider", NextRefreshAt: nowMs + refreshMs, StaleAt: nowMs + staleMs},
	}, true
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
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func currencyUnit(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return "credits"
	}
	return v
}
func severityForPercent(v float64) string {
	if v >= 100 {
		return "critical"
	}
	if v >= 80 {
		return "warning"
	}
	return "ok"
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
