package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jmalloc/usagent/internal/config"
)

func TestOpenAICostNormalizationWeeklyMonthlyMultiBucketAndPagination(t *testing.T) {
	t.Setenv("OPENAI_ADMIN_KEY", "test-admin-key")
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("Authorization"); got != "Bearer test-admin-key" {
			t.Fatalf("Authorization=%q", got)
		}
		if r.URL.Query().Get("bucket_width") != "1d" || r.URL.Query().Get("start_time") == "" || r.URL.Query().Get("end_time") == "" || r.URL.Query().Get("limit") == "" {
			t.Fatalf("query=%s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("after") != "" {
			t.Fatalf("unexpected after cursor: query=%s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("page") == "" {
			fmt.Fprint(w, `{"data":[{"start_time":1699913600,"end_time":1700000000,"results":[{"amount":{"value":2.25,"currency":"usd"}},{"amount":{"value":1.25,"currency":"usd"}}]}],"next_page":"p2"}`)
			return
		}
		if r.URL.Query().Get("page") != "p2" {
			t.Fatalf("page cursor=%q", r.URL.Query().Get("page"))
		}
		fmt.Fprint(w, `{"data":[{"start_time":1697408000,"end_time":1697494400,"results":[{"amount":{"value":3.50,"currency":"usd"}}]}]}`)
	}))
	defer srv.Close()
	cfg := config.OpenAIConfig{Enabled: true, CostsEndpoint: srv.URL, APIKeyEnv: "OPENAI_ADMIN_KEY", Budgets: []config.Budget{
		{ID: "weekly-usd", Unit: "usd", Limit: 14, Window: map[string]any{"id": "week", "kind": "weekly"}},
		{ID: "monthly-usd", Unit: "usd", Limit: 28, Window: map[string]any{"id": "month", "kind": "monthly"}},
	}}
	res, err := NewWithClient(cfg, srv.Client()).Fetch(context.Background(), time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("requests=%d", requests)
	}
	if len(res.Items) != 2 {
		t.Fatalf("items=%+v", res.Items)
	}
	weekly := res.Items[0]
	if weekly.ID != "openai-cost-weekly-usd" || weekly.Provider != "openai" || weekly.Unit != "usd" || weekly.Limit != 14 || weekly.Used != 3.5 || weekly.Remaining != 10.5 || weekly.PercentUsed != 25 || weekly.Window.Kind != "weekly" || weekly.Window.Label != "W" {
		t.Fatalf("weekly=%+v", weekly)
	}
	monthly := res.Items[1]
	if monthly.Used != 7 || monthly.Remaining != 21 || monthly.PercentUsed != 25 || monthly.Window.Kind != "monthly" || monthly.Window.Label != "M" {
		t.Fatalf("monthly=%+v", monthly)
	}
}

func TestOpenAINon2xxRetryAfter(t *testing.T) {
	t.Setenv("OPENAI_ADMIN_KEY", "test-admin-key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	cfg := config.OpenAIConfig{Enabled: true, CostsEndpoint: srv.URL, APIKeyEnv: "OPENAI_ADMIN_KEY", Budgets: []config.Budget{{ID: "weekly-usd", Unit: "usd", Limit: 10, Window: map[string]any{"id": "week", "kind": "weekly"}}}}
	res, err := NewWithClient(cfg, srv.Client()).Fetch(context.Background(), time.Unix(1_700_000_000, 0))
	if err == nil || !strings.Contains(err.Error(), "HTTP 429") {
		t.Fatalf("err=%v", err)
	}
	if res.RetryAfter != 7*time.Second {
		t.Fatalf("retryAfter=%s", res.RetryAfter)
	}
}

func TestOpenAIMissingAPIKey(t *testing.T) {
	t.Setenv("OPENAI_ADMIN_KEY", "")
	cfg := config.OpenAIConfig{Enabled: true, CostsEndpoint: "http://127.0.0.1", APIKeyEnv: "OPENAI_ADMIN_KEY", Budgets: []config.Budget{{ID: "weekly-usd", Unit: "usd", Limit: 10}}}
	_, err := NewWithClient(cfg, http.DefaultClient).Fetch(context.Background(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "OPENAI_ADMIN_KEY") {
		t.Fatalf("err=%v", err)
	}
}
