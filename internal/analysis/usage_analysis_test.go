package analysis

import (
	"strings"
	"testing"
	"time"

	"github.com/jmalloc/usagent/internal/model"
)

func TestUsageAnalysisKnownFixedWindow(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	resetAt := now.Add(time.Hour).UnixMilli()
	item := analysisQuotaItem("claude-code", "claude-code-oauth-session", "Claude 5h", "percent", 100, 80, 20, resetAt)

	res := AnalyzeUsage(model.Usage{GeneratedAt: 999_000, QuotaItems: []model.QuotaItem{item}}, UsageAnalysisOptions{Now: now})
	if res.SchemaVersion != 1 || res.GeneratedAt != now.UnixMilli() || res.UsageGeneratedAt != 999_000 || len(res.Items) != 1 {
		t.Fatalf("response=%+v", res)
	}
	got := res.Items[0]
	if got.PercentRemaining != 20 || got.Confidence != ConfidenceMedium {
		t.Fatalf("unexpected percent/confidence: %+v", got)
	}
	wantDuration := int64((5 * time.Hour) / time.Millisecond)
	wantStartedAt := resetAt - wantDuration
	if got.WindowDurationMs == nil || *got.WindowDurationMs != wantDuration || got.WindowStartedAt == nil || *got.WindowStartedAt != wantStartedAt {
		t.Fatalf("bad window inference: %+v", got)
	}
	if got.WindowElapsedMs == nil || *got.WindowElapsedMs != int64((4*time.Hour)/time.Millisecond) {
		t.Fatalf("bad elapsed: %+v", got)
	}
	if got.ElapsedRatio == nil || *got.ElapsedRatio != 0.8 || got.TimeRemainingRatio == nil || *got.TimeRemainingRatio != 0.2 {
		t.Fatalf("bad ratios: %+v", got)
	}
	if got.Pace == nil || got.Pace.ObservedUsedPerHour != 20 || got.Pace.RequiredRemainingPerHour != 20 {
		t.Fatalf("bad pace: %+v", got.Pace)
	}
	if got.Pressure == nil || got.Pressure.Value != 1 || got.Pressure.Level != "steady" {
		t.Fatalf("bad pressure: %+v", got.Pressure)
	}
	if got.ProjectedExhaustion == nil || got.ProjectedExhaustion.ExhaustsAtMs != resetAt || !got.ProjectedExhaustion.BeforeReset {
		t.Fatalf("bad projection: %+v", got.ProjectedExhaustion)
	}
}

func TestUsageAnalysisProviderResetTimePreferred(t *testing.T) {
	now := time.UnixMilli(2_000_000)
	windowReset := now.Add(2 * time.Hour).UnixMilli()
	providerReset := now.Add(time.Hour).UnixMilli()
	item := analysisQuotaItem("chatgpt", "chatgpt-primary", "ChatGPT 5h", "percent", 100, 25, 75, windowReset)
	item.Reset = &model.Reset{ResetAt: providerReset, ResetWindowID: "session", Source: "provider"}

	got := AnalyzeUsage(model.Usage{QuotaItems: []model.QuotaItem{item}}, UsageAnalysisOptions{Now: now}).Items[0]
	if got.ResetAt == nil || *got.ResetAt != providerReset || got.ResetSource != "provider" {
		t.Fatalf("provider reset was not preferred: %+v", got)
	}
	if got.TimeRemainingMs == nil || *got.TimeRemainingMs != int64(time.Hour/time.Millisecond) {
		t.Fatalf("bad time remaining: %+v", got)
	}
}

func TestUsageAnalysisMissingResetTime(t *testing.T) {
	now := time.UnixMilli(3_000_000)
	item := analysisQuotaItem("openai", "openai-cost-weekly-usd", "OpenAI W", "usd", 50, 12.5, 37.5, now.Add(time.Hour).UnixMilli())
	item.Window = model.Window{ID: "weekly", Label: "W", Kind: "weekly"}
	item.Reset = nil

	got := AnalyzeUsage(model.Usage{QuotaItems: []model.QuotaItem{item}}, UsageAnalysisOptions{Now: now}).Items[0]
	if got.ResetAt != nil || got.TimeRemainingMs != nil || got.WindowStartedAt != nil || got.Pace != nil || got.ProjectedExhaustion != nil {
		t.Fatalf("missing reset should not fabricate reset/start/pace: %+v", got)
	}
	if got.WindowDurationMs == nil || *got.WindowDurationMs != int64((7*24*time.Hour)/time.Millisecond) {
		t.Fatalf("weekly duration should still be inferred: %+v", got)
	}
	if got.Confidence != ConfidenceLow || !analysisCaveatsContain(got.Caveats, "no reset time") {
		t.Fatalf("missing reset caveat/confidence absent: %+v", got)
	}
}

func TestUsageAnalysisStaleAndErrorItemsAreLowConfidence(t *testing.T) {
	now := time.UnixMilli(4_000_000)
	item := analysisQuotaItem("z-ai", "z-ai-daily", "z.ai Daily", "count", 10, 2, 8, now.Add(12*time.Hour).UnixMilli())
	item.Window = model.Window{ID: "daily", Label: "D", Kind: "daily", ResetAt: item.Window.ResetAt}
	item.State = "stale"
	item.Severity = "error"
	item.Error = &model.ItemError{Provider: "z-ai", Code: "refresh_failed", Message: "rate limited", LastOccurredAt: now.UnixMilli(), Recoverable: true}

	got := AnalyzeUsage(model.Usage{QuotaItems: []model.QuotaItem{item}}, UsageAnalysisOptions{Now: now}).Items[0]
	if got.Confidence != ConfidenceLow {
		t.Fatalf("expected low confidence: %+v", got)
	}
	if !analysisCaveatsContain(got.Caveats, "stale") || !analysisCaveatsContain(got.Caveats, "rate limited") {
		t.Fatalf("expected stale/error caveats: %+v", got.Caveats)
	}
}

func TestUsageAnalysisPercentCurrencyAndCountUnits(t *testing.T) {
	now := time.UnixMilli(5_000_000)
	resetAt := now.Add(time.Hour).UnixMilli()
	percent := analysisQuotaItem("chatgpt", "percent", "Percent", "percent", 0, 20, 80, resetAt)
	currency := analysisQuotaItem("openai", "usd", "USD", "usd", 50, 12.5, 37.5, resetAt)
	count := analysisQuotaItem("z-ai", "count", "Count", "count", 10, 7, 3, resetAt)

	res := AnalyzeUsage(model.Usage{QuotaItems: []model.QuotaItem{percent, currency, count}}, UsageAnalysisOptions{Now: now})
	if got := res.Items[0].PercentRemaining; got != 80 {
		t.Fatalf("percent unit remaining=%v item=%+v", got, res.Items[0])
	}
	if got := res.Items[1].PercentRemaining; got != 75 {
		t.Fatalf("currency remaining=%v item=%+v", got, res.Items[1])
	}
	if got := res.Items[2].PercentRemaining; got != 30 {
		t.Fatalf("count remaining=%v item=%+v", got, res.Items[2])
	}
}

func analysisQuotaItem(provider, id, label, unit string, limit, used, remaining float64, resetAt int64) model.QuotaItem {
	return model.QuotaItem{
		ID:          id,
		Provider:    provider,
		Label:       label,
		Window:      model.Window{ID: "session", Label: "S", Kind: "rolling", ResetAt: &resetAt},
		Unit:        unit,
		Limit:       limit,
		Used:        used,
		Remaining:   remaining,
		PercentUsed: used,
		State:       "fresh",
		Severity:    "ok",
		Visible:     true,
		Reset:       &model.Reset{ResetAt: resetAt, ResetWindowID: "session", Source: "provider"},
	}
}

func analysisCaveatsContain(list []string, substr string) bool {
	for _, got := range list {
		if strings.Contains(got, substr) {
			return true
		}
	}
	return false
}
