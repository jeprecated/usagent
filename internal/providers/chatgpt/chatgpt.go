package chatgpt

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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

type Provider struct {
	cfg       config.ChatGPTConfig
	client    *http.Client
	token     string // Set only on an account-bound, request-scoped copy.
	accountID string
}

// BindAccount snapshots credentials so a usage check and subsequent redemption
// cannot silently switch accounts if an OAuth file changes between requests.
func (p *Provider) BindAccount(expected string) (*Provider, string, error) {
	token, account, err := credentials(p.cfg)
	if err != nil {
		return nil, "", err
	}
	if account == "" {
		account = accountIDFromJWT(token)
	}
	if account == "" {
		return nil, "", errors.New("chatgpt account ID is required for reset-once")
	}
	if expected != "" && account != expected {
		return nil, "", errors.New("chatgpt account changed; reset-once disarmed")
	}
	bound := *p
	bound.token, bound.accountID = token, account
	return &bound, account, nil
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
	OpenAICodex struct {
		Type      string `json:"type"`
		Access    string `json:"access"`
		AccountID string `json:"accountId"`
	} `json:"openai-codex"`
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
	RateLimit       *struct {
		PrimaryWindow   *usageWindow `json:"primary_window"`
		SecondaryWindow *usageWindow `json:"secondary_window"`
	} `json:"rate_limit"`
}

type resetCreditsPayload struct {
	AvailableCount int `json:"available_count"`
	Credits        []struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		Title     string `json:"title"`
		GrantedAt string `json:"granted_at"`
		ExpiresAt string `json:"expires_at"`
	} `json:"credits"`
}

type consumePayload struct {
	WindowsReset int    `json:"windows_reset"`
	Code         string `json:"code"`
	RedeemedAt   string `json:"redeemed_at"`
	Credit       *struct {
		ID         string `json:"id"`
		Status     string `json:"status"`
		RedeemedAt string `json:"redeemed_at"`
	} `json:"credit"`
}

func (p *Provider) Fetch(ctx context.Context, now time.Time) (providers.Result, error) {
	result, _, err := p.FetchForReset(ctx, now)
	return result, err
}

// FetchForReset only reports exhaustion from an unambiguous account-wide seven
// day window in this successful response, never from normalized/rounded cache.
func (p *Provider) FetchForReset(ctx context.Context, now time.Time) (providers.Result, bool, error) {
	resp, err := p.do(ctx, http.MethodGet, p.cfg.EndpointURL, nil)
	if err != nil {
		return providers.Result{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retry := ratelimit.RetryAfter(resp.Header, now)
		return providers.Result{RetryAfter: retry}, false, providers.HTTPStatusError("chatgpt usage", resp.StatusCode, retry, resp.Body)
	}
	var payload usagePayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return providers.Result{}, false, err
	}
	weeklyCount, exhausted := 0, false
	if payload.RateLimit != nil {
		for _, w := range []*usageWindow{payload.RateLimit.PrimaryWindow, payload.RateLimit.SecondaryWindow} {
			if w != nil && w.LimitWindowSeconds == 7*24*60*60 {
				weeklyCount++
				exhausted = w.UsedPercent == 100 && w.ResetAfterSeconds > 0
			}
		}
	}
	return providers.Result{Items: Normalize(payload, now, p.cfg.RefreshMs, p.cfg.StaleMs)}, weeklyCount == 1 && exhausted, nil
}

func (p *Provider) ListResetCredits(ctx context.Context, now time.Time) (model.ChatGPTResetCreditsResponse, error) {
	resp, err := p.do(ctx, http.MethodGet, p.cfg.ResetCreditsEndpointURL, nil)
	if err != nil {
		return model.ChatGPTResetCreditsResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retry := ratelimit.RetryAfter(resp.Header, now)
		return model.ChatGPTResetCreditsResponse{}, providers.HTTPStatusError("chatgpt reset credits", resp.StatusCode, retry, resp.Body)
	}
	var payload resetCreditsPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return model.ChatGPTResetCreditsResponse{}, err
	}
	credits := make([]model.ChatGPTResetCredit, 0, len(payload.Credits))
	for _, c := range payload.Credits {
		credits = append(credits, model.ChatGPTResetCredit{ID: c.ID, Status: c.Status, Title: c.Title, GrantedAt: c.GrantedAt, ExpiresAt: c.ExpiresAt})
	}
	return model.ChatGPTResetCreditsResponse{Provider: "chatgpt", AvailableCount: payload.AvailableCount, Credits: credits, FetchedAt: now.UnixMilli()}, nil
}

func (p *Provider) ConsumeResetCredit(ctx context.Context, creditID, redeemRequestID string, now time.Time) (model.ChatGPTResetConsumeResponse, error) {
	creditID = strings.TrimSpace(creditID)
	if creditID == "" {
		return model.ChatGPTResetConsumeResponse{}, errors.New("creditId is required")
	}
	if redeemRequestID == "" {
		var err error
		redeemRequestID, err = randomRedeemRequestID()
		if err != nil {
			return model.ChatGPTResetConsumeResponse{}, err
		}
	}
	body, err := json.Marshal(map[string]string{"credit_id": creditID, "redeem_request_id": redeemRequestID})
	if err != nil {
		return model.ChatGPTResetConsumeResponse{}, err
	}
	resp, err := p.do(ctx, http.MethodPost, p.cfg.ResetConsumeEndpointURL, bytes.NewReader(body))
	if err != nil {
		return model.ChatGPTResetConsumeResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retry := ratelimit.RetryAfter(resp.Header, now)
		return model.ChatGPTResetConsumeResponse{}, providers.HTTPStatusError("chatgpt consume reset credit", resp.StatusCode, retry, resp.Body)
	}
	var payload consumePayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return model.ChatGPTResetConsumeResponse{}, err
	}
	redeemedAt := payload.RedeemedAt
	if payload.Credit != nil && payload.Credit.RedeemedAt != "" {
		redeemedAt = payload.Credit.RedeemedAt
	}
	if !consumeConfirmed(payload, redeemedAt) {
		return model.ChatGPTResetConsumeResponse{}, errors.New("chatgpt reset redemption outcome is unconfirmed")
	}
	return model.ChatGPTResetConsumeResponse{Provider: "chatgpt", CreditID: creditID, RedeemRequestID: redeemRequestID, ConsumedAt: now.UnixMilli(), WindowsReset: payload.WindowsReset, Code: payload.Code, RedeemedAt: redeemedAt}, nil
}

func consumeConfirmed(payload consumePayload, redeemedAt string) bool {
	code := strings.ToLower(strings.TrimSpace(payload.Code))
	_, timeOK := parseRedeemedAt(redeemedAt)
	switch code {
	case "reset":
		return timeOK && payload.WindowsReset > 0
	case "already_redeemed":
		if timeOK {
			return true
		}
		return payload.Credit != nil && payload.Credit.Status == "redeemed"
	default:
		return false
	}
}

func parseRedeemedAt(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	if ts, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return ts, true
	}
	if ts, err := time.Parse(time.RFC3339, v); err == nil {
		return ts, true
	}
	return time.Time{}, false
}

func (p *Provider) do(ctx context.Context, method, url string, body *bytes.Reader) (*http.Response, error) {
	token, accountID := p.token, p.accountID
	if token == "" {
		var err error
		token, accountID, err = credentials(p.cfg)
		if err != nil {
			return nil, err
		}
	}
	if accountID == "" {
		accountID = accountIDFromJWT(token)
	}
	var reqBody *bytes.Reader
	if body == nil {
		reqBody = bytes.NewReader(nil)
	} else {
		reqBody = body
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("authorization", "Bearer "+token)
	req.Header.Set("accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("content-type", "application/json")
	}
	if accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}
	if p.cfg.UserAgent != "" {
		req.Header.Set("user-agent", p.cfg.UserAgent)
	}
	// Never replay a redemption through redirects. A bound GET must also stay
	// on its configured endpoint rather than forwarding pinned credentials.
	client := *p.client
	if method == http.MethodPost || p.token != "" {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return client.Do(req)
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
	fileToken := auth.Tokens.AccessToken
	fileAccountID := auth.Tokens.AccountID
	if fileToken == "" {
		fileToken = auth.OpenAICodex.Access
		fileAccountID = auth.OpenAICodex.AccountID
	}
	if fileToken == "" {
		return "", "", errors.New("chatgpt auth missing Codex tokens.access_token or Pi openai-codex.access")
	}
	return fileToken, firstNonEmpty(accountID, fileAccountID), nil
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
			items = append(items, windowItem("chatgpt-primary", "ChatGPT", *payload.RateLimit.PrimaryWindow, now, refreshMs, staleMs))
		}
		if payload.RateLimit.SecondaryWindow != nil {
			items = append(items, windowItem("chatgpt-secondary", "ChatGPT", *payload.RateLimit.SecondaryWindow, now, refreshMs, staleMs))
		}
	}
	for _, limit := range payload.AdditionalRateLimits {
		name := strings.TrimSpace(firstNonEmpty(limit.DisplayName, limit.Name))
		if name == "" {
			name = "ChatGPT model"
		}
		baseID := stableID("chatgpt-" + name)
		primary, secondary := limit.PrimaryWindow, limit.SecondaryWindow
		if limit.RateLimit != nil {
			if primary == nil {
				primary = limit.RateLimit.PrimaryWindow
			}
			if secondary == nil {
				secondary = limit.RateLimit.SecondaryWindow
			}
		}
		if primary != nil {
			items = append(items, windowItem(baseID+"-primary", name, *primary, now, refreshMs, staleMs))
		}
		if secondary != nil {
			items = append(items, windowItem(baseID+"-secondary", name, *secondary, now, refreshMs, staleMs))
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
	suffix := windowID
	if w.LimitWindowSeconds == 5*60*60 {
		suffix = "5h"
	}
	label += " " + suffix
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
func randomRedeemRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
