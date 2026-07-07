package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmalloc/usagent/internal/app"
	"github.com/jmalloc/usagent/internal/config"
	"github.com/jmalloc/usagent/internal/model"
)

func TestHTTPContract(t *testing.T) {
	cfg := config.Default()
	cfg.Providers.ClaudeOAuth.Enabled = false
	a := app.New(cfg, nil)
	a.Store.UpsertSuccess("claude-code", "Claude", []model.QuotaItem{{ID: "claude-code-oauth-session", Provider: "claude-code", Label: "Claude 5h", Window: model.Window{ID: "session", Label: "S", Kind: "rolling"}, Unit: "percent", Limit: 100, Used: 20, Remaining: 80, PercentUsed: 20, Visible: true}}, time.UnixMilli(1000), 1000, 2000)
	h := New(a, "config.example.yaml")
	for _, path := range []string{"/healthz", "/readyz", "/v1/usage", "/v1/providers"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/config/raw", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("debug config endpoint should be absent, got %d", rec.Code)
	}
	var usage struct {
		SchemaVersion int               `json:"schemaVersion"`
		Service       string            `json:"service"`
		Providers     []model.Provider  `json:"providers"`
		QuotaItems    []model.QuotaItem `json:"quotaItems"`
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/usage", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &usage); err != nil {
		t.Fatal(err)
	}
	if usage.SchemaVersion != 2 || usage.Service != "usagent" || len(usage.Providers) != 3 || len(usage.QuotaItems) != 1 {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestChatGPTResetCreditsEndpoints(t *testing.T) {
	t.Setenv("CHATGPT_ACCESS_TOKEN", "token")
	t.Setenv("CHATGPT_ACCOUNT_ID", "acct")
	var consumed bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/credits":
			fmt.Fprint(w, `{"available_count":1,"credits":[{"id":"credit-1","status":"available","expires_at":"2026-07-12T01:33:14Z"}]}`)
		case "/consume":
			consumed = true
			fmt.Fprint(w, `{"windows_reset":1,"code":"reset","redeemed_at":"2026-06-13T13:12:31Z"}`)
		case "/usage":
			fmt.Fprint(w, `{"rate_limit":{"primary_window":{"used_percent":0,"limit_window_seconds":18000},"secondary_window":{"used_percent":0,"limit_window_seconds":604800}},"rate_limit_reset_credits":{"available_count":0}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	cfg := config.Default()
	cfg.Server.StatePath = filepath.Join(t.TempDir(), "snapshot.json")
	cfg.Providers.ChatGPT.Enabled = true
	cfg.Providers.ChatGPT.TokenEnv = "CHATGPT_ACCESS_TOKEN"
	cfg.Providers.ChatGPT.AccountIDEnv = "CHATGPT_ACCOUNT_ID"
	cfg.Providers.ChatGPT.EndpointURL = provider.URL + "/usage"
	cfg.Providers.ChatGPT.ResetCreditsEndpointURL = provider.URL + "/credits"
	cfg.Providers.ChatGPT.ResetConsumeEndpointURL = provider.URL + "/consume"
	a := app.New(cfg, nil)
	h := New(a, "config.example.yaml")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/chatgpt/reset-credits", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var listed model.ChatGPTResetCreditsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil || listed.AvailableCount != 1 || len(listed.Credits) != 1 {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}

	body := []byte(`{"creditId":"credit-1","redeemRequestId":"req-1","confirm":"consume-chatgpt-reset-credit"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chatgpt/reset-credits/consume", bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-usagent-action", "consume-chatgpt-reset-credit")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || consumed {
		t.Fatalf("consume without opt-in status=%d consumed=%v body=%s", rec.Code, consumed, rec.Body.String())
	}

	cfg.Providers.ChatGPT.AllowResetConsume = true
	a = app.New(cfg, nil)
	h = New(a, "config.example.yaml")
	req = httptest.NewRequest(http.MethodPost, "/v1/chatgpt/reset-credits/consume", bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-usagent-action", "consume-chatgpt-reset-credit")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !consumed {
		t.Fatalf("consume status=%d consumed=%v body=%s", rec.Code, consumed, rec.Body.String())
	}
}
