package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/model"
	"github.com/jeprecated/usagent/internal/providers"
	"github.com/jeprecated/usagent/internal/ratelimit"
)

const (
	tokenEndpoint = "https://platform.claude.com/v1/oauth/token"
	clientID      = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
)

type Provider struct {
	cfg      config.ClaudeOAuthConfig
	client   *http.Client
	tokenURL string
}

func New(cfg config.ClaudeOAuthConfig) *Provider { return NewWithClient(cfg, http.DefaultClient) }
func NewWithClient(cfg config.ClaudeOAuthConfig, c *http.Client) *Provider {
	if c == nil {
		c = http.DefaultClient
	}
	return &Provider{cfg: cfg, client: c, tokenURL: tokenEndpoint}
}
func (p *Provider) ID() string    { return "claude-code" }
func (p *Provider) Label() string { return "Claude" }

type credentials struct {
	document     map[string]json.RawMessage
	oauth        map[string]json.RawMessage
	AccessToken  string
	RefreshToken string
}

type tokenResponse struct {
	AccessToken           string `json:"access_token"`
	RefreshToken          string `json:"refresh_token"`
	ExpiresIn             int64  `json:"expires_in"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
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
	path, err := filepath.EvalSymlinks(p.cfg.CredentialsPath)
	if err != nil {
		return providers.Result{}, fmt.Errorf("resolve claude credentials: %w", err)
	}
	creds, err := readCredentials(path)
	if err != nil {
		return providers.Result{}, err
	}
	result, status, err := p.fetchUsage(ctx, creds.AccessToken, now)
	if status != http.StatusUnauthorized {
		return result, err
	}
	token, err := p.recoverRejectedToken(ctx, path, creds, now)
	if err != nil {
		return result, err
	}
	return p.fetchUsageResult(ctx, token, now)
}

func (p *Provider) recoverRejectedToken(ctx context.Context, path string, rejected credentials, now time.Time) (string, error) {
	release, err := acquireClaudeRefreshLocks(ctx, p.cfg.CredentialsPath)
	if err != nil {
		return "", err
	}
	defer release()

	current, err := readCredentials(path)
	if err != nil {
		return "", err
	}
	if current.AccessToken != rejected.AccessToken {
		return current.AccessToken, nil
	}
	if current.RefreshToken == "" {
		return "", errors.New("claude credentials missing claudeAiOauth.refreshToken after usage returned HTTP 401")
	}
	if err := checkCredentialsWritable(path); err != nil {
		return "", err
	}
	fresh, err := p.refreshToken(ctx, current.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("refresh claude oauth token after usage returned HTTP 401: %w", err)
	}
	return saveRefreshedCredentials(path, current, fresh, now)
}

func (p *Provider) fetchUsageResult(ctx context.Context, token string, now time.Time) (providers.Result, error) {
	result, _, err := p.fetchUsage(ctx, token, now)
	return result, err
}

func (p *Provider) fetchUsage(ctx context.Context, token string, now time.Time) (providers.Result, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.EndpointURL, nil)
	if err != nil {
		return providers.Result{}, 0, err
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
		return providers.Result{}, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retry := ratelimit.AnthropicRetryAfter(resp.Header, now)
		return providers.Result{RetryAfter: retry}, resp.StatusCode, providers.HTTPStatusError("claude oauth usage", resp.StatusCode, retry, resp.Body)
	}
	var payload usagePayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return providers.Result{}, resp.StatusCode, err
	}
	return providers.Result{Items: Normalize(payload, now, p.cfg.RefreshMs, p.cfg.StaleMs)}, resp.StatusCode, nil
}

func (p *Provider) refreshToken(ctx context.Context, refreshToken string) (tokenResponse, error) {
	body, _ := json.Marshal(map[string]string{"grant_type": "refresh_token", "refresh_token": refreshToken, "client_id": clientID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL, bytes.NewReader(body))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/json")
	if p.cfg.UserAgent != "" {
		req.Header.Set("user-agent", p.cfg.UserAgent)
	}
	client := *p.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("claude oauth refresh redirect refused")
	}
	resp, err := client.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tokenResponse{}, providers.HTTPStatusError("claude oauth refresh", resp.StatusCode, 0, resp.Body)
	}
	var fresh tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&fresh); err != nil {
		return tokenResponse{}, err
	}
	if fresh.AccessToken == "" || fresh.ExpiresIn <= 0 {
		return tokenResponse{}, errors.New("claude oauth refresh response missing access_token or expires_in")
	}
	return fresh, nil
}

func readCredentials(path string) (credentials, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return credentials{}, fmt.Errorf("read claude credentials: %w", err)
	}
	var c credentials
	if err := json.Unmarshal(b, &c.document); err != nil {
		return credentials{}, fmt.Errorf("parse claude credentials: %w", err)
	}
	if err := json.Unmarshal(c.document["claudeAiOauth"], &c.oauth); err != nil {
		return credentials{}, fmt.Errorf("parse claude credentials claudeAiOauth: %w", err)
	}
	_ = json.Unmarshal(c.oauth["accessToken"], &c.AccessToken)
	_ = json.Unmarshal(c.oauth["refreshToken"], &c.RefreshToken)
	if c.AccessToken == "" {
		return credentials{}, errors.New("claude credentials missing claudeAiOauth.accessToken")
	}
	return c, nil
}

func saveRefreshedCredentials(path string, expected credentials, fresh tokenResponse, now time.Time) (string, error) {
	current, err := readCredentials(path)
	if err != nil {
		return "", err
	}
	if current.AccessToken != expected.AccessToken || current.RefreshToken != expected.RefreshToken {
		return current.AccessToken, nil
	}
	if fresh.RefreshToken == "" {
		fresh.RefreshToken = current.RefreshToken
	}
	current.oauth["accessToken"], _ = json.Marshal(fresh.AccessToken)
	current.oauth["refreshToken"], _ = json.Marshal(fresh.RefreshToken)
	current.oauth["expiresAt"], _ = json.Marshal(now.Add(time.Duration(fresh.ExpiresIn) * time.Second).UnixMilli())
	if fresh.RefreshTokenExpiresIn > 0 {
		current.oauth["refreshTokenExpiresAt"], _ = json.Marshal(now.Add(time.Duration(fresh.RefreshTokenExpiresIn) * time.Second).UnixMilli())
	}
	current.document["claudeAiOauth"], _ = json.Marshal(current.oauth)
	b, err := json.Marshal(current.document)
	if err != nil {
		return "", fmt.Errorf("encode claude credentials: %w", err)
	}
	if err := writeCredentials(path, b); err != nil {
		return "", err
	}
	return fresh.AccessToken, nil
}

func checkCredentialsWritable(path string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claude-credentials-write-check-*")
	if err != nil {
		return fmt.Errorf("claude credentials are not writable: %w", err)
	}
	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("claude credentials are not writable: %w", err)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("claude credentials are not writable: %w", err)
	}
	return nil
}

// These are the two proper-lockfile paths used by Claude Code's OAuth refresh.
func acquireClaudeRefreshLocks(ctx context.Context, credentialsPath string) (func(), error) {
	resolvedPath, err := filepath.EvalSymlinks(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("resolve claude credentials for locking: %w", err)
	}
	dirs := map[string]bool{}
	for _, dir := range []string{filepath.Dir(credentialsPath), filepath.Dir(resolvedPath)} {
		realDir, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return nil, fmt.Errorf("resolve claude credentials directory: %w", err)
		}
		dirs[realDir] = true
	}
	orderedDirs := make([]string, 0, len(dirs))
	for dir := range dirs {
		orderedDirs = append(orderedDirs, dir)
	}
	sort.Strings(orderedDirs)
	paths := make([]string, 0, len(orderedDirs)*2)
	for _, dir := range orderedDirs {
		paths = append(paths, filepath.Join(dir, ".oauth_refresh.lock"), dir+".lock")
	}
	releases := make([]func(), 0, len(paths))
	for _, path := range paths {
		release, err := acquireLockDir(ctx, path)
		if err != nil {
			for i := len(releases) - 1; i >= 0; i-- {
				releases[i]()
			}
			return nil, fmt.Errorf("acquire claude oauth refresh lock: %w", err)
		}
		releases = append(releases, release)
	}
	return func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}, nil
}

func acquireLockDir(ctx context.Context, path string) (func(), error) {
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		if err := os.Mkdir(path, 0o700); err == nil {
			owned, err := os.Stat(path)
			if err != nil {
				_ = os.Remove(path)
				return nil, err
			}
			return keepLockAlive(path, owned), nil
		} else if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) > time.Minute {
			_ = os.Remove(path)
			continue
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, errors.New("timed out")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func keepLockAlive(path string, owned os.FileInfo) func() {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				if ownsLock(path, owned) {
					_ = os.Chtimes(path, now, now)
				}
			case <-done:
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			<-stopped
			if ownsLock(path, owned) {
				_ = os.Remove(path)
			}
		})
	}
}

func ownsLock(path string, owned os.FileInfo) bool {
	current, err := os.Stat(path)
	return err == nil && owned != nil && os.SameFile(owned, current)
}

func writeCredentials(path string, b []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat claude credentials: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claude-credentials-*.json")
	if err != nil {
		return fmt.Errorf("create temporary claude credentials: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace claude credentials: %w", err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open claude credentials directory: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync claude credentials directory: %w", err)
	}
	return nil
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
