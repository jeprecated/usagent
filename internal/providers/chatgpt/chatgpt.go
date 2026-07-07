package chatgpt

import (
	"context"
	"encoding/base64"
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
	cfg    config.ChatGPTConfig
	client *http.Client
}

func New(cfg config.ChatGPTConfig) *Provider { return NewWithClient(cfg, http.DefaultClient) }
func NewWithClient(cfg config.ChatGPTConfig, c *http.Client) *Provider {
	if c == nil {
		c = http.DefaultClient
	}
	return &Provider{cfg: cfg, client: c}
}
func (p *Provider) ID() string    { return "chatgpt" }
func (p *Provider) Label() string { return "ChatGPT Pro" }

type authFile struct {
	AuthMode string `json:"auth_mode"`
	Tokens   struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

type usagePayload struct {
	PlanType  string `json:"plan_type"`
	Email     string `json:"email"`
	RateLimit *struct {
		Allowed         bool         `json:"allowed"`
		LimitReached    bool         `json:"limit_reached"`
		PrimaryWindow   *usageWindow `json:"primary_window"`
		SecondaryWindow *usageWindow `json:"secondary_window"`
	} `json:"rate_limit"`
	AdditionalRateLimits []additionalLimit `json:"additional_rate_limits"`
	ResetCredits         *struct {
		AvailableCount int `json:"available_count"`
	} `json:"rate_limit_reset_credits"`
}

type usageWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds"`
}

type additionalLimit struct {
	Name            string       `json:"name"`
	DisplayName     string       `json:"display_name"`
	PrimaryWindow   *usageWindow `json:"primary_window"`
	SecondaryWindow *usageWindow `json:"secondary_window"`
}

func (p *Provider) Fetch(ctx context.Context, now time.Time) (providers.Result, error) {
	token, accountID, err := credentials(p.cfg)
	if err != nil {
		return providers.Result{}, err
	}
	if accountID == "" {
		accountID = accountIDFromJWT(token)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.EndpointURL, nil)
	if err != nil {
		return providers.Result{}, err
	}
	req.Header.Set("authorization", "Bearer "+token)
	req.Header.Set("accept", "application/json")
	if accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}
	if p.cfg.UserAgent != "" {
		req.Header.Set("user-agent", p.cfg.UserAgent)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return providers.Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retry := ratelimit.RetryAfter(resp.Header, now)
		return providers.Result{RetryAfter: retry}, providers.HTTPStatusError("chatgpt usage", resp.StatusCode, retry, resp.Body)
	}
	var payload usagePayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return providers.Result{}, err
	}
	return providers.Result{Items: Normalize(payload, now, p.cfg.RefreshMs, p.cfg.StaleMs)}, nil
}

func credentials(cfg config.ChatGPTConfig) (token, accountID string, err error) {
	if cfg.TokenEnv != "" {
		token = os.Getenv(cfg.TokenEnv)
	}
	if cfg.AccountIDEnv != "" {
		accountID = os.Getenv(cfg.AccountIDEnv)
	}
	if token != "" {
		return token, accountID, nil
	}
	if cfg.AuthPath == "" {
		return "", "", errors.New("chatgpt access token missing: set CHATGPT_ACCESS_TOKEN or configure authPath")
	}
	b, err := os.ReadFile(cfg.AuthPath)
	if err != nil {
		return "", "", fmt.Errorf("read chatgpt auth: %w", err)
	}
	var auth authFile
	if err := json.Unmarshal(b, &auth); err != nil {
		return "", "", fmt.Errorf("parse chatgpt auth: %w", err)
	}
	if auth.Tokens.AccessToken == "" {
		return "", "", errors.New("chatgpt auth missing tokens.access_token")
	}
	return auth.Tokens.AccessToken, firstNonEmpty(accountID, auth.Tokens.AccountID), nil
}

func Normalize(payload usagePayload, now time.Time, refreshMs, staleMs int64) []model.QuotaItem {
	if refreshMs <= 0 {
		refreshMs = int64((5 * time.Minute) / time.Millisecond)
	}
	if staleMs <= 0 {
		staleMs = int64((15 * time.Minute) / time.Millisecond)
	}
	items := []model.QuotaItem{}
	if payload.RateLimit != nil {
		if payload.RateLimit.PrimaryWindow != nil {
			items = append(items, windowItem("chatgpt-primary", "ChatGPT 5h", *payload.RateLimit.PrimaryWindow, now, refreshMs, staleMs))
		}
		if payload.RateLimit.SecondaryWindow != nil {
			items = append(items, windowItem("chatgpt-secondary", "ChatGPT weekly", *payload.RateLimit.SecondaryWindow, now, refreshMs, staleMs))
		}
	}
	for _, limit := range payload.AdditionalRateLimits {
		name := strings.TrimSpace(firstNonEmpty(limit.DisplayName, limit.Name))
		if name == "" {
			name = "ChatGPT model"
		}
		baseID := stableID("chatgpt-" + name)
		if limit.PrimaryWindow != nil {
			items = append(items, windowItem(baseID+"-primary", name+" 5h", *limit.PrimaryWindow, now, refreshMs, staleMs))
		}
		if limit.SecondaryWindow != nil {
			items = append(items, windowItem(baseID+"-secondary", name+" weekly", *limit.SecondaryWindow, now, refreshMs, staleMs))
		}
	}
	if payload.ResetCredits != nil {
		items = append(items, resetCreditsItem(payload.ResetCredits.AvailableCount, now, refreshMs, staleMs))
	}
	return items
}

func windowItem(id, label string, w usageWindow, now time.Time, refreshMs, staleMs int64) model.QuotaItem {
	used := clampPercent(w.UsedPercent)
	remaining := math.Max(0, 100-used)
	windowID, windowLabel, windowKind := windowMeta(w.LimitWindowSeconds)
	nowMs := now.UnixMilli()
	item := model.QuotaItem{ID: id, Provider: "chatgpt", Label: label, Window: model.Window{ID: windowID, Label: windowLabel, Kind: windowKind}, Unit: "percent", Limit: 100, Used: used, Remaining: remaining, PercentUsed: used, State: "fresh", Severity: severityForPercent(used), Visible: true, Refresh: &model.Refresh{LastUpdatedAt: nowMs, Source: "provider", NextRefreshAt: nowMs + refreshMs, StaleAt: nowMs + staleMs}}
	if w.ResetAfterSeconds > 0 {
		resetAt := now.Add(time.Duration(w.ResetAfterSeconds) * time.Second).UnixMilli()
		item.Window.ResetAt = &resetAt
		item.Reset = &model.Reset{ResetAt: resetAt, ResetWindowID: windowID, Source: "provider"}
	}
	return item
}

func resetCreditsItem(count int, now time.Time, refreshMs, staleMs int64) model.QuotaItem {
	if count < 0 {
		count = 0
	}
	nowMs := now.UnixMilli()
	return model.QuotaItem{ID: "chatgpt-rate-limit-reset-credits", Provider: "chatgpt", Label: "ChatGPT resets", Window: model.Window{ID: "resetCredits", Label: "Resets", Kind: "credit"}, Unit: "credits", Limit: float64(count), Used: 0, Remaining: float64(count), PercentUsed: 0, State: "fresh", Severity: "ok", Visible: true, Refresh: &model.Refresh{LastUpdatedAt: nowMs, Source: "provider", NextRefreshAt: nowMs + refreshMs, StaleAt: nowMs + staleMs}}
}

func windowMeta(seconds int64) (id, label, kind string) {
	switch {
	case seconds >= 6*24*60*60:
		return "weekly", "W", "weekly"
	case seconds >= 4*60*60:
		return "session", "S", "rolling"
	default:
		return "window", "Q", "rolling"
	}
}

func accountIDFromJWT(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(b, &payload); err != nil {
		return ""
	}
	if auth, ok := payload["https://api.openai.com/auth"].(map[string]any); ok {
		if id, ok := auth["chatgpt_account_id"].(string); ok {
			return id
		}
	}
	return ""
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
func severityForPercent(v float64) string {
	if v >= 100 {
		return "critical"
	}
	if v >= 80 {
		return "warning"
	}
	return "ok"
}
func stableID(v string) string {
	v = strings.ToLower(v)
	var b strings.Builder
	lastDash := false
	for _, r := range v {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
