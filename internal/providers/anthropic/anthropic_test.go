package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/config"
)

var testNow = time.Date(2026, 5, 3, 13, 30, 0, 0, time.UTC)

func testConfig() config.AnthropicConfig {
	balance, reported := 75.0, 1.0
	return config.AnthropicConfig{APIKeyEnv: "USAGENT_TEST_ANTHROPIC_KEY", Checkpoint: &config.AnthropicCheckpoint{
		BalanceUSD: &balance, ReportStart: "2026-05-01T00:00:00Z", ReportedCostUSD: &reported,
	}}
}

func bucket(day int, amounts ...string) map[string]any {
	results := []map[string]string{}
	for _, amount := range amounts {
		results = append(results, map[string]string{"amount": amount, "currency": "USD"})
	}
	return map[string]any{
		"starting_at": fmt.Sprintf("2026-05-%02dT00:00:00Z", day),
		"ending_at":   fmt.Sprintf("2026-05-%02dT00:00:00Z", day+1), "results": results,
	}
}

func TestFetchCostsPaginationAndCheckpoint(t *testing.T) {
	cfg := testConfig()
	t.Setenv(cfg.APIKeyEnv, "test-admin-key")
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if r.Method != "GET" || r.Header.Get("x-api-key") != "test-admin-key" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("user-agent") == "" {
			t.Errorf("incorrect method/auth/version/user-agent")
		}
		if q.Get("starting_at") != cfg.Checkpoint.ReportStart || q.Get("ending_at") != "2026-05-04T00:00:00Z" || q.Get("bucket_width") != "1d" || q.Get("limit") != "31" || q.Has("group_by[]") {
			t.Errorf("query=%v", q)
		}
		switch q.Get("page") {
		case "":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{bucket(1, "100")}, "has_more": true, "next_page": "opaque +/="})
		case "opaque +/=":
			// Multiple service/workspace costs and fractional cents: sum before rounding.
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{bucket(2), bucket(3, "123.78912", "76.21088")}, "has_more": false, "next_page": nil})
		default:
			t.Errorf("unexpected page %q", q.Get("page"))
		}
	}))
	defer srv.Close()
	cfg.CostsEndpoint = srv.URL
	p := New(cfg)
	result, err := p.Fetch(context.Background(), testNow.In(time.FixedZone("local", -7*3600)))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(result.Items) != 1 {
		t.Fatalf("calls=%d result=%+v", calls, result)
	}
	item := result.Items[0]
	if item.Provider != "anthropic" || item.Limit != 75 || item.Used != 2 || item.Remaining != 73 || item.PercentUsed != 3 || item.Unit != "usd" || !item.Estimated || !strings.Contains(item.Label, "estimated") {
		t.Fatalf("item=%+v", item)
	}
	if item.Reset != nil || item.Window.ResetAt != nil || item.Window.Kind != "custom" {
		t.Fatalf("credits must not reset monthly: %+v", item)
	}
}

func TestFetchBalances(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		balance, baseline        float64
		amount                   string
		used, remaining, percent float64
		severity                 string
	}{
		{"unused", 50, 2, "200", 0, 50, 0, "ok"},
		{"warning", 50, 0, "4000", 40, 10, 80, "warning"},
		{"exhausted", 50, 0, "5000", 50, 0, 100, "critical"},
		{"overspent", 50, 0, "6000", 60, 0, 100, "critical"},
		{"zero balance", 0, 0, "0", 0, 0, 100, "critical"},
		{"fractional cent", 50, 0, "123.78912", 1.24, 48.76, 2, "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			t.Setenv(cfg.APIKeyEnv, "key")
			cfg.Checkpoint.BalanceUSD = &tc.balance
			cfg.Checkpoint.ReportedCostUSD = &tc.baseline
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{bucket(3, tc.amount)}, "has_more": false})
			}))
			defer srv.Close()
			cfg.CostsEndpoint = srv.URL
			res, err := New(cfg).Fetch(context.Background(), testNow)
			if err != nil {
				t.Fatal(err)
			}
			it := res.Items[0]
			if it.Used != tc.used || it.Remaining != tc.remaining || it.PercentUsed != tc.percent || it.Severity != tc.severity {
				t.Fatalf("item=%+v", it)
			}
		})
	}
}

func TestFetchRejectsInvalidReportsWithoutPublishingPartialTotals(t *testing.T) {
	goodBucket, _ := json.Marshal(bucket(1, "200"))
	for name, body := range map[string]string{
		"missing fields":    `{}`,
		"null data":         `{"data":null,"has_more":false}`,
		"missing has_more":  `{"data":[]}`,
		"malformed":         `not JSON`,
		"missing cursor":    `{"data":[],"has_more":true}`,
		"repeated cursor":   `{"data":[],"has_more":true,"next_page":"same"}`,
		"below baseline":    `{"data":[],"has_more":false}`,
		"duplicate buckets": `{"data":[` + string(goodBucket) + `,` + string(goodBucket) + `],"has_more":false}`,
		"invalid bucket":    `{"data":[{"results":[]}],"has_more":false}`,
		"missing results":   `{"data":[{"starting_at":"2026-05-01T00:00:00Z","ending_at":"2026-05-02T00:00:00Z"}],"has_more":false}`,
	} {
		t.Run(name, func(t *testing.T) { assertBadReport(t, body) })
	}
	for _, amount := range []string{"", "NaN", "+Inf", "-1", "invalid", "1e999"} {
		b, _ := json.Marshal(map[string]any{"data": []any{bucket(1, amount)}, "has_more": false})
		t.Run("amount_"+amount, func(t *testing.T) { assertBadReport(t, string(b)) })
	}
	for _, currency := range []string{"EUR", ""} {
		body := strings.ReplaceAll(`{"data":[`+string(goodBucket)+`],"has_more":false}`, `"USD"`, `"`+currency+`"`)
		t.Run("currency_"+currency, func(t *testing.T) { assertBadReport(t, body) })
	}
	for _, date := range []string{"2026-04-30T00:00:00Z", "2026-05-04T00:00:00Z", "2026-05-01T12:00:00Z"} {
		body := strings.ReplaceAll(`{"data":[`+string(goodBucket)+`],"has_more":false}`, "2026-05-01T00:00:00Z", date)
		t.Run("date_"+date, func(t *testing.T) { assertBadReport(t, body) })
	}
}

func assertBadReport(t *testing.T, body string) {
	t.Helper()
	cfg := testConfig()
	t.Setenv(cfg.APIKeyEnv, "key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
	defer srv.Close()
	cfg.CostsEndpoint = srv.URL
	result, err := New(cfg).Fetch(context.Background(), testNow)
	if err == nil || len(result.Items) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestFetchPaginationLimit(t *testing.T) {
	cfg := testConfig()
	t.Setenv(cfg.APIKeyEnv, "key")
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprintf(w, `{"data":[],"has_more":true,"next_page":"%d"}`, calls)
	}))
	defer srv.Close()
	cfg.CostsEndpoint = srv.URL
	res, err := New(cfg).Fetch(context.Background(), testNow)
	if err == nil || !strings.Contains(err.Error(), "pagination limit") || calls != maxPages || len(res.Items) != 0 {
		t.Fatalf("calls=%d result=%+v err=%v", calls, res, err)
	}
}

func TestFetchHTTPFailuresAndRetries(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			cfg := testConfig()
			t.Setenv(cfg.APIKeyEnv, "secret-not-for-errors")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("retry-after", "120")
				w.WriteHeader(status)
				fmt.Fprint(w, r.Header.Get("x-api-key"))
			}))
			defer srv.Close()
			cfg.CostsEndpoint = srv.URL
			res, err := New(cfg).Fetch(context.Background(), testNow)
			if err == nil || !strings.Contains(err.Error(), fmt.Sprint(status)) || strings.Contains(err.Error(), "secret-not-for-errors") || res.RetryAfter != 2*time.Minute || len(res.Items) != 0 {
				t.Fatalf("res=%+v err=%v", res, err)
			}
		})
	}
}

func TestFetchRefusesRedirectAndCancellation(t *testing.T) {
	cfg := testConfig()
	t.Setenv(cfg.APIKeyEnv, "key")
	leaked := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer srv.Close()
	cfg.CostsEndpoint = srv.URL
	client := &http.Client{}
	p := NewWithClient(cfg, client)
	if _, err := p.Fetch(context.Background(), testNow); err == nil || !strings.Contains(err.Error(), "redirect refused") || leaked {
		t.Fatalf("leaked=%v err=%v", leaked, err)
	}
	if client.CheckRedirect != nil || client.Timeout != 0 {
		t.Fatal("mutated caller's client")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Fetch(ctx, testNow); err == nil {
		t.Fatal("expected cancellation")
	}
}

func TestCredentialsFilePrecedenceAndRotation(t *testing.T) {
	cfg := testConfig()
	t.Setenv(cfg.APIKeyEnv, "environment-key")
	cfg.APIKeyFile = filepath.Join(t.TempDir(), "admin-key")
	p := New(cfg)
	for _, key := range []string{"first-key", "rotated-key"} {
		if err := os.WriteFile(cfg.APIKeyFile, []byte("  "+key+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := p.apiKey()
		if err != nil || got != key {
			t.Fatalf("key not re-read from file: %v", err)
		}
	}
	if err := os.Remove(cfg.APIKeyFile); err != nil {
		t.Fatal(err)
	}
	if _, err := p.apiKey(); err == nil {
		t.Fatal("missing explicit file must not fall back to environment")
	}
	cfg.APIKeyFile = ""
	if key, err := New(cfg).apiKey(); err != nil || key != "environment-key" {
		t.Fatal("environment key not loaded")
	}
	t.Setenv(cfg.APIKeyEnv, " \n")
	if _, err := New(cfg).Fetch(context.Background(), testNow); err == nil || !strings.Contains(err.Error(), "key missing") {
		t.Fatalf("err=%v", err)
	}
}

func TestFetchRejectsFutureOrMissingCheckpoint(t *testing.T) {
	cfg := testConfig()
	cfg.Checkpoint.ReportStart = "2026-05-04T00:00:00Z"
	if _, err := New(cfg).Fetch(context.Background(), testNow); err == nil || !strings.Contains(err.Error(), "future") {
		t.Fatalf("err=%v", err)
	}
	cfg.Checkpoint = nil
	if _, err := New(cfg).Fetch(context.Background(), testNow); err == nil {
		t.Fatal("missing checkpoint accepted")
	}
}
