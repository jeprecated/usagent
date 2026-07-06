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

	"github.com/jmalloc/usagent/internal/config"
)

func TestClaudeFetchNormalizesOAuthLimits(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"claudeAiOauth":{"accessToken":"secret-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"limits": []map[string]any{
			{"kind": "session", "percent": 12.4, "resets_at": "2026-07-06T12:00:00Z"},
			{"kind": "weekly_all", "utilization": 50.9},
			{"kind": "weekly_scoped", "percent": 24, "scope": map[string]any{"model": map[string]any{"display_name": "Fable"}}},
			{"kind": "weekly_scoped", "percent": 99, "scope": map[string]any{"model": map[string]any{"display_name": "Other"}}},
		}})
	}))
	defer srv.Close()
	p := New(config.ClaudeOAuthConfig{CredentialsPath: credPath, EndpointURL: srv.URL, BetaHeader: "oauth-test", RefreshMs: 1000, StaleMs: 2000})
	res, err := p.Fetch(context.Background(), time.UnixMilli(1000))
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer secret-token" {
		t.Fatalf("authorization header not set")
	}
	if len(res.Items) != 3 {
		t.Fatalf("items=%+v", res.Items)
	}
	ids := map[string]bool{}
	for _, it := range res.Items {
		ids[it.ID] = true
	}
	for _, id := range []string{"claude-code-oauth-session", "claude-code-oauth-weekly-all", "claude-code-oauth-fable-weekly"} {
		if !ids[id] {
			t.Fatalf("missing %s in %+v", id, res.Items)
		}
	}
	if res.Items[0].Used != 12 || res.Items[1].Used != 51 {
		t.Fatalf("rounding failed: %+v", res.Items)
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

func TestClaudeDoesNotFabricateMissingBuckets(t *testing.T) {
	payload := usagePayload{Limits: []limit{{Kind: "session"}}}
	items := Normalize(payload, time.UnixMilli(1000), 1000, 2000)
	if len(items) != 1 || items[0].ID != "claude-code-oauth-session" {
		t.Fatalf("items=%+v", items)
	}
}
