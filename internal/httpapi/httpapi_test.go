package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
