package app_test

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

	"github.com/jeprecated/usagent/internal/app"
	"github.com/jeprecated/usagent/internal/cli"
	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/httpapi"
	"github.com/jeprecated/usagent/internal/model"
)

func TestAnthropicCachedUsageEndToEnd(t *testing.T) {
	now := time.Now().UTC()
	start := now.Truncate(24 * time.Hour)
	var calls, status atomic.Int32
	status.Store(200)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if status.Load() != 200 {
			w.Header().Set("retry-after", "1200")
			w.WriteHeader(int(status.Load()))
			fmt.Fprint(w, r.Header.Get("x-api-key"))
			return
		}
		fmt.Fprintf(w, `{"data":[{"starting_at":%q,"ending_at":%q,"results":[{"amount":"350","currency":"USD"}]}],"has_more":false}`, start.Format(time.RFC3339), start.Add(24*time.Hour).Format(time.RFC3339))
	}))
	defer upstream.Close()
	cfg := config.Default()
	cfg.Server.StatePath = filepath.Join(t.TempDir(), "snapshot.json")
	balance, baseline := 75.0, 1.5
	cfg.Providers.Anthropic = config.AnthropicConfig{Enabled: true, CostsEndpoint: upstream.URL, APIKeyEnv: "TEST_ANTHROPIC_KEY", Checkpoint: &config.AnthropicCheckpoint{BalanceUSD: &balance, ReportStart: start.Format(time.RFC3339), ReportedCostUSD: &baseline}, Metadata: config.ProviderMetadataConfig{Tags: []string{"api"}}}
	t.Setenv("TEST_ANTHROPIC_KEY", "private-admin-key")
	cfg, err := config.Normalize(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(cfg, nil)
	var index = -1
	for i, p := range a.Providers {
		if p.ID() == "anthropic" {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("provider not wired")
	}
	a.RefreshOne(context.Background(), a.Providers[index], now)

	// Enabling is sufficient: no separate usageView change is required. HTTP
	// and CLI consume the same cached item; reads never spend/poll upstream.
	handler := httpapi.New(a, "")
	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest("GET", "/v1/usage", nil))
		if rr.Code != 200 {
			t.Fatalf("HTTP %d", rr.Code)
		}
		var usage model.Usage
		if err := json.Unmarshal(rr.Body.Bytes(), &usage); err != nil {
			t.Fatal(err)
		}
		if len(usage.QuotaItems) != 1 {
			t.Fatalf("usage=%+v", usage)
		}
		item := usage.QuotaItems[0]
		if item.Remaining != 73 || item.Used != 2 || !item.Estimated || item.Refresh == nil || item.State != "fresh" || len(item.ProviderTags) != 1 {
			t.Fatalf("item=%+v", item)
		}
		table := cli.FormatUsage(usage)
		if !strings.Contains(table, "Anthropic API") || !strings.Contains(table, "estimated") || !strings.Contains(table, "$73") {
			t.Fatalf("table=%s", table)
		}
		if strings.Contains(rr.Body.String(), "private-admin-key") {
			t.Fatal("key exposed")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("reads polled upstream: %d", calls.Load())
	}

	loaded := app.New(cfg, nil)
	if err := loaded.LoadState(); err != nil {
		t.Fatal(err)
	}
	if !loaded.Usage(now).QuotaItems[0].Estimated {
		t.Fatal("estimate marker lost on restart")
	}
	status.Store(429)
	a.RefreshOne(context.Background(), a.Providers[index], now.Add(time.Minute))
	item := a.Usage(now.Add(time.Minute)).QuotaItems[0]
	if item.State != "stale" || item.Remaining != 73 || item.Error == nil || item.Refresh.NextRefreshAt != now.Add(21*time.Minute).UnixMilli() {
		t.Fatalf("failed refresh discarded cache/backoff: %+v", item)
	}
	b, err := os.ReadFile(cfg.Server.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "private-admin-key") {
		t.Fatal("key persisted")
	}

	// A new checkpoint is explicit config, not a sticky in-memory starting
	// balance: the next successful refresh recomputes even with an old cache.
	balance, baseline = 100, 3.5
	status.Store(200)
	reconciled := app.New(cfg, nil)
	if err := reconciled.LoadState(); err != nil {
		t.Fatal(err)
	}
	for _, p := range reconciled.Providers {
		if p.ID() == "anthropic" {
			reconciled.RefreshOne(context.Background(), p, now.Add(time.Hour))
		}
	}
	item = reconciled.Usage(now.Add(time.Hour)).QuotaItems[0]
	if item.Used != 0 || item.Remaining != 100 {
		t.Fatalf("checkpoint not reconciled: %+v", item)
	}
}
