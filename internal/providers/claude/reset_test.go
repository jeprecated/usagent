package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/config"
)

func TestNormalizeCedarEmberResetCredits(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	payload := usagePayload{
		Limits: []limit{{Kind: "session", Percent: floatPtr(10)}},
		CedarEmber: &cedarEmber{
			Eligible: true,
			Grants: []cedarGrant{
				{ID: "grant-a", Label: "Full reset", ResetsLeft: 1, ResetsTotal: 1, StartsAt: "2026-09-22T00:00:00Z", EndsAt: "2026-10-22T00:00:00Z", UsableNow: true, Clears: []string{"five_hour", "seven_day"}},
			},
		},
	}
	items := Normalize(payload, now, 1000, 2000)
	if len(items) != 2 {
		t.Fatalf("items=%+v", items)
	}
	credits := items[1]
	if credits.ID != "claude-code-rate-limit-reset-credits" || credits.Unit != "credits" || credits.Remaining != 1 || credits.Window.ID != "resetCredits" {
		t.Fatalf("credits=%+v", credits)
	}
}

func TestFetchForResetRequiresWeeklyExhaustion(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"claudeAiOauth":{"accessToken":"secret-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cedar_ember") != "1" {
			t.Errorf("missing cedar_ember query: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"limits": []map[string]any{
				{"kind": "session", "percent": 100, "resets_at": "2026-09-22T17:00:00Z"},
				{"kind": "weekly_all", "percent": 100, "resets_at": "2026-09-23T12:00:00Z"},
			},
			"cedar_ember": map[string]any{"eligible": true, "grants": []map[string]any{{"id": "g1", "resets_left": 1, "ends_at": "2026-10-01T00:00:00Z", "usable_now": true}}},
		})
	}))
	defer srv.Close()
	p := New(config.ClaudeOAuthConfig{CredentialsPath: credPath, EndpointURL: srv.URL, BetaHeader: "oauth-test"})
	_, exhausted, err := p.FetchForReset(context.Background(), now)
	if err != nil || !exhausted {
		t.Fatalf("exhausted=%v err=%v", exhausted, err)
	}
}

func TestListResetCreditsMapsGrants(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"claudeAiOauth":{"accessToken":"secret-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cedar_ember": map[string]any{
				"eligible":      true,
				"next_grant_id": "grant-a",
				"grants": []map[string]any{
					{"id": "grant-a", "label": "Full reset", "resets_left": 1, "resets_total": 1, "starts_at": "2026-09-22T00:00:00Z", "ends_at": "2026-10-22T00:00:00Z", "usable_now": true, "clears": []string{"five_hour"}},
					{"id": "grant-b", "resets_left": 1, "ends_at": "2026-09-21T00:00:00Z", "usable_now": true},
				},
			},
		})
	}))
	defer srv.Close()
	p := New(config.ClaudeOAuthConfig{CredentialsPath: credPath, EndpointURL: srv.URL, BetaHeader: "oauth-test"})
	res, err := p.ListResetCredits(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Eligible || res.AvailableCount != 1 || res.NextGrantID != "grant-a" || len(res.Credits) != 2 {
		t.Fatalf("list=%+v", res)
	}
	if res.Credits[0].Status != "available" || res.Credits[1].Status != "expired" {
		t.Fatalf("statuses=%q %q", res.Credits[0].Status, res.Credits[1].Status)
	}
}

func TestConsumePostsCedarEmberClaim(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.json")
	accountPath := filepath.Join(dir, "claude.json")
	if err := os.WriteFile(credPath, []byte(`{"claudeAiOauth":{"accessToken":"secret-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(accountPath, []byte(`{"oauthAccount":{"organizationUuid":"org-1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var gotPath, gotAuth, gotOrg string
	var body map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotOrg = r.URL.Path, r.Header.Get("authorization"), r.Header.Get("x-organization-uuid")
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": "reset", "resets_left": 0, "cleared": []string{"five_hour", "seven_day"}, "weekly_resets_at": "2026-09-29T12:00:00Z"})
	}))
	defer srv.Close()
	p := NewWithClient(config.ClaudeOAuthConfig{CredentialsPath: credPath, AccountPath: accountPath, ResetConsumeEndpointURL: srv.URL + "/api/organizations/{organizationUuid}/reset_rate_limits", BetaHeader: "oauth-test"}, srv.Client())
	res, err := p.ConsumeResetCredit(context.Background(), "grant-a", "req-1", time.UnixMilli(1000))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/organizations/org-1/reset_rate_limits" || gotAuth != "Bearer secret-token" || gotOrg != "org-1" {
		t.Fatalf("path=%s auth=%s org=%s", gotPath, gotAuth, gotOrg)
	}
	if body["program"] != "cedar_ember" || body["grant_id"] != "grant-a" || body["request_id"] != "req-1" {
		t.Fatalf("body=%v", body)
	}
	if res.Code != "reset" || res.WindowsReset != 2 || res.WeeklyResetsAt == "" {
		t.Fatalf("consume=%+v", res)
	}
}

func TestConsumeDoesNotFollowRedirects(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"claudeAiOauth":{"accessToken":"secret-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/consume" {
			http.Redirect(w, r, "/second", http.StatusTemporaryRedirect)
			return
		}
		fmt.Fprint(w, `{"result":"reset","cleared":["five_hour"]}`)
	}))
	defer srv.Close()
	p := NewWithClient(config.ClaudeOAuthConfig{CredentialsPath: credPath, AccountPath: credPath, ResetConsumeEndpointURL: srv.URL + "/consume"}, srv.Client())
	if _, err := p.ConsumeResetCredit(context.Background(), "grant", "request", time.Now()); err == nil {
		t.Fatal("redirect accepted")
	}
	if hits.Load() != 1 {
		t.Fatalf("sent %d requests", hits.Load())
	}
}

func TestConsumeRequiresConfirmedResult(t *testing.T) {
	for _, body := range []string{`{}`, `{"result":"not_limited"}`, `{"result":"ineligible"}`, `{"result":"cooldown"}`} {
		t.Run(body, func(t *testing.T) {
			dir := t.TempDir()
			credPath := filepath.Join(dir, "credentials.json")
			if err := os.WriteFile(credPath, []byte(`{"claudeAiOauth":{"accessToken":"secret-token"}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer srv.Close()
			p := NewWithClient(config.ClaudeOAuthConfig{CredentialsPath: credPath, ResetConsumeEndpointURL: srv.URL}, srv.Client())
			if _, err := p.ConsumeResetCredit(context.Background(), "grant", "request", time.Now()); err == nil {
				t.Fatal("unconfirmed consume accepted")
			}
		})
	}
}

func TestBindAccountPinsOrganization(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.json")
	accountPath := filepath.Join(dir, "claude.json")
	writeAccount := func(org string) {
		t.Helper()
		if err := os.WriteFile(accountPath, []byte(fmt.Sprintf(`{"oauthAccount":{"organizationUuid":%q}}`, org)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(credPath, []byte(`{"claudeAiOauth":{"accessToken":"token-a"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeAccount("org-a")
	p := New(config.ClaudeOAuthConfig{CredentialsPath: credPath, AccountPath: accountPath})
	bound, org, err := p.BindAccount("org-a")
	if err != nil || org != "org-a" {
		t.Fatalf("org=%q err=%v", org, err)
	}
	writeAccount("org-b")
	if _, _, err := p.BindAccount("org-a"); err == nil {
		t.Fatal("organization change accepted")
	}
	if bound.orgUUID != "org-a" || bound.token != "token-a" {
		t.Fatalf("bound mutated: %+v", bound)
	}
}

func TestWeeklyExhaustedIgnoresSessionOnly(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	payload := usagePayload{Limits: []limit{{Kind: "session", Percent: floatPtr(100), ResetsAt: "2026-09-22T17:00:00Z"}}}
	if weeklyExhausted(payload, now) {
		t.Fatal("session exhaustion treated as weekly")
	}
}

func floatPtr(v float64) *float64 { return &v }

func TestUsageURLMergesCedarEmber(t *testing.T) {
	got := usageURL("https://api.anthropic.com/api/oauth/usage")
	if !strings.Contains(got, "cedar_ember=1") {
		t.Fatalf("url=%s", got)
	}
	if usageURL("https://api.anthropic.com/api/oauth/usage?cedar_ember=1&skip_spend=1") != "https://api.anthropic.com/api/oauth/usage?cedar_ember=1&skip_spend=1" {
		t.Fatal("existing query rewritten")
	}
}
