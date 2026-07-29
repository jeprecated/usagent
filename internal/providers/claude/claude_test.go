package claude

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/config"
)

func TestClaudeFetchNormalizesOAuthLimits(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"claudeAiOauth":{"accessToken":"secret-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var auth, userAgent, anthropicVersion, contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("authorization")
		userAgent = r.Header.Get("user-agent")
		anthropicVersion = r.Header.Get("anthropic-version")
		contentType = r.Header.Get("content-type")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"limits": []map[string]any{
				{"kind": "session", "percent": 12.4, "resets_at": "2026-07-06T12:00:00Z"},
				{"kind": "weekly_all", "utilization": 50.9},
				{"kind": "weekly_scoped", "percent": 24, "scope": map[string]any{"model": map[string]any{"display_name": "Fable"}}},
				{"kind": "weekly_scoped", "percent": 99, "scope": map[string]any{"model": map[string]any{"display_name": "Other"}}},
			},
			"extra_usage": map[string]any{"is_enabled": true, "monthly_limit": 50000, "used_credits": 1250, "utilization": 2.5, "currency": "USD"},
		})
	}))
	defer srv.Close()
	p := New(config.ClaudeOAuthConfig{CredentialsPath: credPath, EndpointURL: srv.URL, BetaHeader: "oauth-test", UserAgent: "claude-code/test", RefreshMs: 1000, StaleMs: 2000})
	res, err := p.Fetch(context.Background(), time.UnixMilli(1000))
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer secret-token" {
		t.Fatalf("authorization header not set")
	}
	if userAgent != "claude-code/test" || anthropicVersion != "2023-06-01" || contentType != "application/json" {
		t.Fatalf("headers user-agent=%q anthropic-version=%q content-type=%q", userAgent, anthropicVersion, contentType)
	}
	if len(res.Items) != 4 {
		t.Fatalf("items=%+v", res.Items)
	}
	ids := map[string]bool{}
	for _, it := range res.Items {
		ids[it.ID] = true
	}
	for _, id := range []string{"claude-code-oauth-session", "claude-code-oauth-weekly-all", "claude-code-oauth-fable-weekly", "claude-code-oauth-extra-credits"} {
		if !ids[id] {
			t.Fatalf("missing %s in %+v", id, res.Items)
		}
	}
	if res.Items[0].Used != 12 || res.Items[1].Used != 51 {
		t.Fatalf("rounding failed: %+v", res.Items)
	}
	extra := res.Items[3]
	if extra.Unit != "usd" || extra.Limit != 500 || extra.Used != 12.5 || extra.Remaining != 487.5 || extra.PercentUsed != 3 {
		t.Fatalf("extra credits item=%+v", extra)
	}
}

func TestClaudeFetchRefreshesRejectedOAuthToken(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"other":{"keep":true},"claudeAiOauth":{"accessToken":"expired","refreshToken":"refresh-old","expiresAt":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	var usageCalls, refreshCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/usage":
			usageCalls++
			if r.Header.Get("authorization") != "Bearer access-new" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"limits": []map[string]any{{"kind": "session", "percent": 10}}})
		case "/token":
			refreshCalls++
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["grant_type"] != "refresh_token" || body["refresh_token"] != "refresh-old" || body["client_id"] != clientID {
				t.Fatalf("refresh body=%v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-new", "refresh_token": "refresh-new", "expires_in": 3600, "refresh_token_expires_in": 7200})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := NewWithClient(config.ClaudeOAuthConfig{CredentialsPath: credPath, EndpointURL: srv.URL + "/usage", BetaHeader: "oauth-test"}, srv.Client())
	p.tokenURL = srv.URL + "/token"
	res, err := p.Fetch(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if usageCalls != 2 || refreshCalls != 1 || len(res.Items) != 1 {
		t.Fatalf("usageCalls=%d refreshCalls=%d result=%+v", usageCalls, refreshCalls, res)
	}
	var saved map[string]json.RawMessage
	b, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	var oauth map[string]any
	if err := json.Unmarshal(saved["claudeAiOauth"], &oauth); err != nil {
		t.Fatal(err)
	}
	if oauth["accessToken"] != "access-new" || oauth["refreshToken"] != "refresh-new" || oauth["expiresAt"] != float64(now.Add(time.Hour).UnixMilli()) || oauth["refreshTokenExpiresAt"] != float64(now.Add(2*time.Hour).UnixMilli()) || saved["other"] == nil {
		t.Fatalf("saved credentials=%s", b)
	}
}

func TestClaudeFetchUsesClaudeRefreshLocksAndPreservesSymlink(t *testing.T) {
	dir := t.TempDir()
	targetDir := t.TempDir()
	actualPath := filepath.Join(targetDir, "stored-credentials.json")
	credPath := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(actualPath, []byte(`{"claudeAiOauth":{"accessToken":"expired","refreshToken":"refresh-old"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actualPath, credPath); err != nil {
		t.Fatal(err)
	}
	lockPaths := []string{
		filepath.Join(dir, ".oauth_refresh.lock"), dir + ".lock",
		filepath.Join(targetDir, ".oauth_refresh.lock"), targetDir + ".lock",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/usage":
			if r.Header.Get("authorization") != "Bearer access-new" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"limits": []map[string]any{{"kind": "session", "percent": 10}}})
		case "/token":
			for _, path := range lockPaths {
				if info, err := os.Stat(path); err != nil || !info.IsDir() {
					t.Errorf("Claude-compatible refresh lock %q is not held: %v", path, err)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-new", "refresh_token": "refresh-new", "expires_in": 3600})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := NewWithClient(config.ClaudeOAuthConfig{CredentialsPath: credPath, EndpointURL: srv.URL + "/usage", BetaHeader: "oauth-test"}, srv.Client())
	p.tokenURL = srv.URL + "/token"
	if _, err := p.Fetch(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, path := range lockPaths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("refresh lock %q was not released: %v", path, err)
		}
	}
	if info, err := os.Lstat(credPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("credentials symlink was replaced: info=%v err=%v", info, err)
	}
}

func TestClaudeFetchRefusesRefreshRedirect(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"claudeAiOauth":{"accessToken":"expired","refreshToken":"refresh-old"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stolen := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/usage":
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		case "/token":
			http.Redirect(w, r, "/stolen", http.StatusTemporaryRedirect)
		case "/stolen":
			stolen = true
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "stolen", "expires_in": 3600})
		}
	}))
	defer srv.Close()
	p := NewWithClient(config.ClaudeOAuthConfig{CredentialsPath: credPath, EndpointURL: srv.URL + "/usage", BetaHeader: "oauth-test"}, srv.Client())
	p.tokenURL = srv.URL + "/token"
	if _, err := p.Fetch(context.Background(), time.Now()); err == nil {
		t.Fatal("expected refresh redirect error")
	}
	if stolen {
		t.Fatal("refresh token was sent to redirect destination")
	}
}

func TestLockReleaseDoesNotDeleteSuccessorLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth.lock")
	release, err := acquireLockDir(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	release()
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("successor lock was deleted: info=%v err=%v", info, err)
	}
}

func TestClaudeFetchHonorsRetryAfter(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"claudeAiOauth":{"accessToken":"secret-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("retry-after", "7")
		http.Error(w, "rate", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	p := New(config.ClaudeOAuthConfig{CredentialsPath: credPath, EndpointURL: srv.URL, BetaHeader: "oauth-test"})
	res, err := p.Fetch(context.Background(), time.UnixMilli(1000))
	if err == nil {
		t.Fatal("expected error")
	}
	if res.RetryAfter != 7*time.Second {
		t.Fatalf("retryAfter=%s", res.RetryAfter)
	}
}

func TestClaudeFetchFallsBackToAnthropicResetHeaders(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"claudeAiOauth":{"accessToken":"secret-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("anthropic-ratelimit-requests-remaining", "0")
		w.Header().Set("anthropic-ratelimit-requests-reset", now.Add(23*time.Second).Format(time.RFC3339))
		http.Error(w, "rate", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	p := New(config.ClaudeOAuthConfig{CredentialsPath: credPath, EndpointURL: srv.URL, BetaHeader: "oauth-test"})
	res, err := p.Fetch(context.Background(), now)
	if err == nil {
		t.Fatal("expected error")
	}
	if res.RetryAfter != 23*time.Second {
		t.Fatalf("retryAfter=%s", res.RetryAfter)
	}
}

func TestClaudeSkipsDisabledExtraUsage(t *testing.T) {
	items := Normalize(usagePayload{ExtraUsage: &extraUsage{IsEnabled: false}}, time.UnixMilli(1000), 1000, 2000)
	if len(items) != 0 {
		t.Fatalf("items=%+v", items)
	}
}

func TestClaudeDoesNotFabricateMissingBuckets(t *testing.T) {
	payload := usagePayload{Limits: []limit{{Kind: "session"}}}
	items := Normalize(payload, time.UnixMilli(1000), 1000, 2000)
	if len(items) != 1 || items[0].ID != "claude-code-oauth-session" {
		t.Fatalf("items=%+v", items)
	}
}
