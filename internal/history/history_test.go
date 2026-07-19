package history

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/model"
)

func TestStoreSaveLoadRetentionAndItemDisappearance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage-history.json")
	store := NewStoreWithLimits(path, time.Hour, 2)
	base := time.UnixMilli(10_000)
	resetAt := base.Add(time.Hour).UnixMilli()

	store.Record("claude-code", []model.QuotaItem{historyItem("claude-code", "a", "percent", 100, 1, 99, resetAt)}, base.Add(-2*time.Hour))
	store.Record("claude-code", []model.QuotaItem{historyItem("claude-code", "a", "percent", 100, 2, 98, resetAt)}, base.Add(-30*time.Minute))
	store.Record("claude-code", []model.QuotaItem{historyItem("claude-code", "a", "percent", 100, 3, 97, resetAt)}, base.Add(-20*time.Minute))
	store.Record("claude-code", []model.QuotaItem{historyItem("claude-code", "b", "percent", 100, 10, 90, resetAt)}, base.Add(-10*time.Minute))

	samples := store.Samples()
	if len(samples) != 3 {
		t.Fatalf("samples=%+v", samples)
	}
	for _, sample := range samples {
		if sample.ItemID == "a" && sample.RecordedAt == base.Add(-2*time.Hour).UnixMilli() {
			t.Fatalf("old sample was not pruned: %+v", samples)
		}
	}
	if err := store.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded := NewStoreWithLimits(path, time.Hour, 2)
	if err := loaded.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(loaded.Samples()); got != 3 {
		t.Fatalf("loaded samples=%d", got)
	}
}

func TestRatesPreferRecentHorizonsAndUseRemainingWhenUsedFlat(t *testing.T) {
	store := NewStore("")
	base := time.UnixMilli(100_000)
	resetAt := base.Add(time.Hour).UnixMilli()
	store.Record("chatgpt", []model.QuotaItem{historyItem("chatgpt", "session", "percent", 100, 0, 90, resetAt)}, base.Add(-14*time.Minute))
	store.Record("chatgpt", []model.QuotaItem{historyItem("chatgpt", "session", "percent", 100, 0, 80, resetAt)}, base.Add(-10*time.Minute))
	current := historyItem("chatgpt", "session", "percent", 100, 0, 80, resetAt)

	rates := store.Rates([]model.QuotaItem{current}, base)
	rate, ok := rates[Key("chatgpt", "session")]
	if !ok {
		t.Fatalf("missing rate: %+v", rates)
	}
	want := 10 / float64((4*time.Minute)/time.Millisecond)
	if math.Abs(rate.BurnPerMs-want) > 0.000001 {
		t.Fatalf("burnPerMs=%g want %g", rate.BurnPerMs, want)
	}
	if rate.Source != "15m local history burn rate" {
		t.Fatalf("source=%q", rate.Source)
	}
}

func TestRatesDoNotCrossCounterResetOrResetBoundary(t *testing.T) {
	store := NewStore("")
	base := time.UnixMilli(200_000)
	oldReset := base.Add(30 * time.Minute).UnixMilli()
	newReset := base.Add(2 * time.Hour).UnixMilli()
	store.Record("z-ai", []model.QuotaItem{historyItem("z-ai", "daily", "tokens", 100, 80, 20, oldReset)}, base.Add(-40*time.Minute))
	store.Record("z-ai", []model.QuotaItem{historyItem("z-ai", "daily", "tokens", 100, 90, 10, oldReset)}, base.Add(-30*time.Minute))
	store.Record("z-ai", []model.QuotaItem{historyItem("z-ai", "daily", "tokens", 100, 5, 95, newReset)}, base.Add(-20*time.Minute))
	current := historyItem("z-ai", "daily", "tokens", 100, 8, 92, newReset)

	if rates := store.Rates([]model.QuotaItem{current}, base); len(rates) != 0 {
		t.Fatalf("expected no current-window rate with only one compatible sample, got %+v", rates)
	}

	store.Record("z-ai", []model.QuotaItem{current}, base.Add(-10*time.Minute))
	rates := store.Rates([]model.QuotaItem{current}, base)
	rate, ok := rates[Key("z-ai", "daily")]
	if !ok {
		t.Fatalf("missing new-window rate: %+v", rates)
	}
	want := 3 / float64((10*time.Minute)/time.Millisecond)
	if math.Abs(rate.BurnPerMs-want) > 0.000001 {
		t.Fatalf("burnPerMs=%g want %g", rate.BurnPerMs, want)
	}
}

func TestRatesFallBackToWindowToDate(t *testing.T) {
	store := NewStore("")
	base := time.UnixMilli(10_000_000_000)
	resetAt := base.Add(24 * time.Hour).UnixMilli()
	store.Record("openai", []model.QuotaItem{historyItem("openai", "monthly-usd", "usd", 20, 1, 19, resetAt)}, base.Add(-7*time.Hour))
	store.Record("openai", []model.QuotaItem{historyItem("openai", "monthly-usd", "usd", 20, 3, 17, resetAt)}, base.Add(-5*time.Hour))
	current := historyItem("openai", "monthly-usd", "usd", 20, 3, 17, resetAt)

	rates := store.Rates([]model.QuotaItem{current}, base)
	rate, ok := rates[Key("openai", "monthly-usd")]
	if !ok {
		t.Fatalf("missing rate: %+v", rates)
	}
	if !rate.WindowToDate || rate.Source != "window-to-date local history burn rate" {
		t.Fatalf("rate=%+v", rate)
	}
}

func historyItem(provider, id, unit string, limit, used, remaining float64, resetAt int64) model.QuotaItem {
	return model.QuotaItem{
		ID:          id,
		Provider:    provider,
		Label:       id,
		Window:      model.Window{ID: "window", Label: "Window", Kind: "rolling", ResetAt: &resetAt},
		Unit:        unit,
		Limit:       limit,
		Used:        used,
		Remaining:   remaining,
		PercentUsed: used,
		Visible:     true,
		Reset:       &model.Reset{ResetAt: resetAt, ResetWindowID: "window", Source: "provider"},
	}
}
