package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/app"
	"github.com/jeprecated/usagent/internal/cli"
	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/httpapi"
	"github.com/jeprecated/usagent/internal/model"
)

func TestChatGPTCreditsCachedUsageEndToEnd(t *testing.T) {
	now := time.Now()
	var calls, status atomic.Int32
	status.Store(http.StatusOK)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.Header.Get("authorization") != "Bearer private-test-token" {
			t.Error("unexpected request method or credentials")
		}
		if status.Load() != http.StatusOK {
			w.WriteHeader(int(status.Load()))
			fmt.Fprint(w, "slow down")
			return
		}
		fmt.Fprint(w, `{"rate_limit":{"primary_window":{"used_percent":25,"limit_window_seconds":18000,"reset_after_seconds":3600}},"credits":{"has_credits":true,"unlimited":false,"balance":"62500"},"rate_limit_reset_credits":{"available_count":2}}`)
	}))
	defer upstream.Close()
	t.Setenv("TEST_CHATGPT_TOKEN", "private-test-token")
	cfg := config.Default()
	cfg.Server.StatePath = filepath.Join(t.TempDir(), "snapshot.json")
	cfg.UsageView.Providers = []string{"chatgpt"}
	cfg.Providers.ClaudeOAuth.Enabled = false
	cfg.Providers.ChatGPT.Enabled = true
	cfg.Providers.ChatGPT.EndpointURL = upstream.URL
	cfg.Providers.ChatGPT.TokenEnv = "TEST_CHATGPT_TOKEN"
	cfg.Providers.ChatGPT.AccountIDEnv = "TEST_CHATGPT_ACCOUNT"
	t.Setenv("TEST_CHATGPT_ACCOUNT", "test-account")
	cfg, err := config.Normalize(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(cfg, nil)
	a.RefreshOne(context.Background(), a.Providers[0], now)
	handler := httpapi.New(a, "")
	for range 2 {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/usage", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", rr.Code, rr.Body.String())
		}
		var usage model.Usage
		if err := json.Unmarshal(rr.Body.Bytes(), &usage); err != nil {
			t.Fatal(err)
		}
		if len(usage.QuotaItems) != 3 {
			t.Fatalf("missing quota, spending credits, or resets: %+v", usage)
		}
		table := cli.FormatUsage(usage)
		for _, want := range []string{"spending credits", "62500 credits", "resets", "2 credits", "75%"} {
			if !strings.Contains(table, want) {
				t.Fatalf("table missing %q: %s", want, table)
			}
		}
		if strings.Contains(table, "62500 credits (") || strings.Contains(rr.Body.String(), "private-test-token") {
			t.Fatalf("invented percentage or leaked token: %s", table)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("cached reads polled upstream: %d", calls.Load())
	}
	loaded := app.New(cfg, nil)
	if err := loaded.LoadState(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cli.FormatUsage(loaded.Usage(now)), "62500 credits") {
		t.Fatal("credit balance lost on restart")
	}
	status.Store(http.StatusTooManyRequests)
	a.RefreshOne(context.Background(), a.Providers[0], now.Add(time.Minute))
	for _, item := range a.Usage(now.Add(time.Minute)).QuotaItems {
		if item.ID == "chatgpt-spending-credits" {
			if item.State != "stale" || item.Remaining != 62500 || item.Error == nil {
				t.Fatalf("failed refresh discarded credit balance: %+v", item)
			}
			return
		}
	}
	t.Fatal("failed refresh dropped credit balance")
}
