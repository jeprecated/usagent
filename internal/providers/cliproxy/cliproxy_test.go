package cliproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/model"
)

const testKey = "management-secret"

type apiCall struct {
	AuthIndex string            `json:"auth_index"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Header    map[string]string `json:"header"`
	Data      string            `json:"data"`
}

// fakeProxy is a CLIProxyAPI management API. upstream maps an api-call URL
// prefix to its upstream status and body.
type fakeProxy struct {
	t        *testing.T
	files    []map[string]any
	upstream map[string]func(apiCall) (int, string)
	download map[string]string
	mu       sync.Mutex
	calls    []apiCall
}

func (f *fakeProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+testKey {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v8/management/credentials":
		_ = json.NewEncoder(w).Encode(map[string]any{"files": f.files})
	case r.Method == http.MethodGet && r.URL.Path == "/v8/management/credentials/download":
		body, ok := f.download[r.URL.Query().Get("name")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	case r.Method == http.MethodPost && r.URL.Path == "/v8/management/requests/api-call":
		var call apiCall
		if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
			f.t.Fatalf("decode api-call: %v", err)
		}
		f.mu.Lock()
		f.calls = append(f.calls, call)
		f.mu.Unlock()
		for prefix, respond := range f.upstream {
			if strings.HasPrefix(call.URL, prefix) {
				status, body := respond(call)
				_ = json.NewEncoder(w).Encode(map[string]any{"status_code": status, "header": map[string][]string{}, "body": body})
				return
			}
		}
		f.t.Errorf("unexpected upstream %s", call.URL)
		_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 599, "body": ""})
	default:
		f.t.Errorf("unexpected management request %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusNotFound)
	}
}

func fixed(status int, body string) func(apiCall) (int, string) {
	return func(apiCall) (int, string) { return status, body }
}

func newProvider(t *testing.T, proxy *fakeProxy) *Provider {
	t.Helper()
	srv := httptest.NewServer(proxy)
	t.Cleanup(srv.Close)
	t.Setenv("CLIPROXY_MANAGEMENT_KEY", testKey)
	return NewWithClient(config.CLIProxyConfig{Enabled: true, BaseURL: srv.URL, ManagementKeyEnv: "CLIPROXY_MANAGEMENT_KEY", RefreshMs: 300000, StaleMs: 900000}, srv.Client())
}

func itemsByID(items []model.QuotaItem) map[string]model.QuotaItem {
	out := map[string]model.QuotaItem{}
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func TestFetchReadsEveryAccountTypeThroughManagementAPI(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	proxy := &fakeProxy{t: t,
		files: []map[string]any{
			{"auth_index": "c1", "name": "claude-a.json", "provider": "claude", "email": "alice@example.com", "account_type": "oauth"},
			{"auth_index": "x1", "name": "codex-b.json", "provider": "codex", "email": "bob@example.com", "id_token": map[string]any{"chatgpt_account_id": "acct-1"}},
			{"auth_index": "g1", "name": "ag.json", "provider": "antigravity", "email": "gina@example.com", "project_id": "proj-1"},
			{"auth_index": "d1", "name": "devin.json", "provider": "devin", "note": "Work Devin"},
			{"auth_index": "k1", "name": "kimi.json", "provider": "kimi-ai", "email": "kim@example.com"},
			{"auth_index": "m1", "name": "meta.json", "provider": "meta", "email": "max@example.com"},
			{"auth_index": "r1", "name": "grok.json", "provider": "grok", "email": "rae@example.com"},
			{"auth_index": "off", "name": "claude-off.json", "provider": "claude", "disabled": true},
			{"auth_index": "key", "name": "claude-key", "provider": "claude", "account_type": "api_key", "account": "sk-secret"},
			{"auth_index": "q1", "name": "qwen.json", "provider": "qwen"},
		},
		download: map[string]string{"meta.json": `{"dca_token":"dca:meta-token"}`},
		upstream: map[string]func(apiCall) (int, string){
			"https://api.anthropic.com/api/oauth/usage":                 fixed(200, `{"limits":[{"kind":"session","percent":25,"resets_at":"2026-10-13T14:00:00Z"},{"kind":"weekly_all","percent":40}]}`),
			"https://chatgpt.com/backend-api/wham/usage":                fixed(200, `{"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000,"reset_after_seconds":60},"secondary_window":{"used_percent":50,"limit_window_seconds":604800,"reset_after_seconds":3600}}}`),
			"https://daily-cloudcode-pa.googleapis.com/":                fixed(500, `{}`),
			"https://daily-cloudcode-pa.sandbox.googleapis.com/":        fixed(200, `{"groups":[{"displayName":"Gemini","buckets":[{"bucketId":"g-5h","window":"5h","remainingFraction":"75%","resetTime":"2026-10-13T15:00:00Z"},{"bucketId":"g-w","window":"weekly","remainingFraction":0.5}]}]}`),
			"https://server.codeium.com/":                               fixed(200, `{"userStatus":{"planStatus":{"dailyQuotaRemainingPercent":"80","dailyQuotaResetAtUnix":"1791900000","weeklyQuotaRemainingPercent":30}}}`),
			"https://api.kimi.ai/coding/v1/usages":                      fixed(200, `{"limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},"detail":{"limit":"100","remaining":"40","resetTime":"2026-10-13T16:00:00Z"}}],"usage":{"limit":1000,"used":100}}`),
			"https://api.meta.ai/muse-code/key":                         fixed(200, `{"subs_usage":{"window":{"used_percent":12,"window_duration_mins":300,"resets_at":1791900000},"weekly":{"used_percent":33}}}`),
			"https://cli-chat-proxy.grok.com/v1/billing?format=credits": fixed(200, `{"config":{"creditUsagePercent":20,"currentPeriod":{"type":"BILLING_PERIOD_WEEKLY","start":"2026-10-10T00:00:00Z","end":"2026-10-17T00:00:00Z"}}}`),
			"https://cli-chat-proxy.grok.com/v1/billing":                fixed(200, `{"config":{"monthlyLimit":{"val":"5000"},"used":1000,"billingPeriodStart":"2026-10-01T00:00:00Z","billingPeriodEnd":"2026-11-01T00:00:00Z"}}`),
		},
	}
	p := newProvider(t, proxy)

	result, err := p.Fetch(context.Background(), now)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	items := itemsByID(result.Items)
	for _, item := range result.Items {
		if item.Provider != ProviderID {
			t.Fatalf("item %s has provider %q", item.ID, item.Provider)
		}
		if item.Error != nil {
			t.Fatalf("unexpected item error %s: %s", item.ID, item.Error.Message)
		}
	}

	expect := map[string]struct {
		label     string
		window    string
		remaining float64
	}{
		"cliproxy-claude-c1-oauth-session":    {"Claude a***@example.com 5h", "session", 75},
		"cliproxy-claude-c1-oauth-weekly-all": {"Claude a***@example.com weekly", "weekly", 60},
		"cliproxy-codex-x1-primary":           {"Codex b***@example.com 5h", "session", 90},
		"cliproxy-codex-x1-secondary":         {"Codex b***@example.com weekly", "weekly", 50},
		"cliproxy-antigravity-g1-g-5h":        {"Antigravity g***@example.com Gemini 5h", "session", 75},
		"cliproxy-antigravity-g1-g-w":         {"Antigravity g***@example.com Gemini weekly", "weekly", 50},
		"cliproxy-devin-d1-daily":             {"Devin Work Devin daily", "daily", 80},
		"cliproxy-devin-d1-weekly":            {"Devin Work Devin weekly", "weekly", 30},
		"cliproxy-kimi-k1-limit-0":            {"Kimi k***@example.com 5h", "session", 40},
		"cliproxy-kimi-k1-summary":            {"Kimi k***@example.com weekly", "weekly", 90},
		"cliproxy-meta-m1-window":             {"Meta m***@example.com 5h", "session", 88},
		"cliproxy-meta-m1-weekly":             {"Meta m***@example.com weekly", "weekly", 67},
		"cliproxy-xai-r1-credits":             {"Grok r***@example.com credits", "weekly", 80},
		"cliproxy-xai-r1-billing":             {"Grok r***@example.com monthly", "monthly", 80},
	}
	for id, want := range expect {
		item, ok := items[id]
		if !ok {
			t.Errorf("missing item %s; got %v", id, keys(items))
			continue
		}
		if item.Label != want.label || item.Window.ID != want.window || item.Remaining != want.remaining {
			t.Errorf("%s = label %q window %q remaining %v; want %q %q %v", id, item.Label, item.Window.ID, item.Remaining, want.label, want.window, want.remaining)
		}
	}
	if len(result.Items) != len(expect) {
		t.Errorf("got %d items, want %d: %v", len(result.Items), len(expect), keys(items))
	}
	if reset := items["cliproxy-claude-c1-oauth-session"].Reset; reset == nil || reset.ResetAt != time.Date(2026, 10, 13, 14, 0, 0, 0, time.UTC).UnixMilli() {
		t.Errorf("claude session reset = %+v", reset)
	}

	for _, call := range proxy.calls {
		if call.AuthIndex == "off" || call.AuthIndex == "key" || call.AuthIndex == "q1" {
			t.Errorf("skipped credential %s was queried", call.AuthIndex)
		}
		switch {
		case strings.HasPrefix(call.URL, "https://api.anthropic.com/"):
			if call.AuthIndex != "c1" || call.Header["Authorization"] != "Bearer $TOKEN$" || !strings.Contains(call.URL, "cedar_ember=1") {
				t.Errorf("claude call = %+v", call)
			}
		case strings.HasPrefix(call.URL, "https://chatgpt.com/"):
			if call.Header["chatgpt-account-id"] != "acct-1" {
				t.Errorf("codex call missing account id: %+v", call)
			}
		case strings.HasPrefix(call.URL, "https://server.codeium.com/"):
			if !strings.Contains(call.Data, `"apiKey":"$TOKEN$"`) {
				t.Errorf("devin call body = %s", call.Data)
			}
		case strings.HasPrefix(call.URL, "https://api.meta.ai/"):
			if call.Header["Authorization"] != "Bearer dca:meta-token" {
				t.Errorf("meta call header = %v", call.Header)
			}
		}
	}
}

func TestFetchKeepsHealthyAccountsWhenOneFails(t *testing.T) {
	proxy := &fakeProxy{t: t,
		files: []map[string]any{
			{"auth_index": "a", "provider": "claude", "email": "a@example.com"},
			{"auth_index": "b", "provider": "claude", "email": "b@example.com"},
		},
		upstream: map[string]func(apiCall) (int, string){
			"https://api.anthropic.com/": func(call apiCall) (int, string) {
				if call.AuthIndex == "b" {
					return 401, `{"error":{"message":"token sk-should-not-leak expired"}}`
				}
				return 200, `{"limits":[{"kind":"session","percent":10}]}`
			},
		},
	}
	result, err := newProvider(t, proxy).Fetch(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	items := itemsByID(result.Items)
	if items["cliproxy-claude-a-oauth-session"].Remaining != 90 {
		t.Fatalf("healthy account missing: %v", keys(items))
	}
	failed, ok := items["cliproxy-claude-b-status"]
	if !ok || failed.Error == nil || failed.State != "error" || failed.Label != "Claude b***@example.com" {
		t.Fatalf("failed account item = %+v", failed)
	}
	if failed.Error.Message != "claude oauth usage returned HTTP 401" {
		t.Fatalf("error message = %q", failed.Error.Message)
	}
}

func TestFetchFailsWhenEveryAccountFails(t *testing.T) {
	proxy := &fakeProxy{t: t,
		files:    []map[string]any{{"auth_index": "a", "provider": "codex"}},
		upstream: map[string]func(apiCall) (int, string){"https://chatgpt.com/": fixed(503, "")},
	}
	_, err := newProvider(t, proxy).Fetch(context.Background(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "codex usage returned HTTP 503") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchRejectedManagementKeyBacksOff(t *testing.T) {
	p := newProvider(t, &fakeProxy{t: t})
	t.Setenv("CLIPROXY_MANAGEMENT_KEY", "wrong")
	result, err := p.Fetch(context.Background(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "management key rejected") || result.RetryAfter < time.Hour {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestFetchRequiresManagementKey(t *testing.T) {
	p := NewWithClient(config.CLIProxyConfig{BaseURL: "http://127.0.0.1:1", ManagementKeyEnv: "USAGENT_TEST_UNSET_KEY"}, nil)
	if _, err := p.Fetch(context.Background(), time.Now()); err == nil || !strings.Contains(err.Error(), "USAGENT_TEST_UNSET_KEY") {
		t.Fatalf("err = %v", err)
	}
}

func TestSelectAccountsDisambiguatesMaskedNames(t *testing.T) {
	accounts := selectAccounts([]credential{
		{AuthIndex: "aaaa1111", Provider: "claude", Email: "jo@example.com"},
		{AuthIndex: "bbbb2222", Provider: "claude", Email: "jules@example.com"},
	})
	if len(accounts) != 2 || accounts[0].name == accounts[1].name || !strings.HasPrefix(accounts[0].name, "j***@example.com #") {
		t.Fatalf("names = %q, %q", accounts[0].name, accounts[1].name)
	}
}

func keys(m map[string]model.QuotaItem) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}
