package analysis

import (
	"errors"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/model"
)

func TestResetCalendarSortsVisibleItemsAndSkipsNoReset(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	later := now.Add(2 * time.Hour).UnixMilli()
	soon := now.Add(time.Hour).UnixMilli()
	usage := model.Usage{QuotaItems: []model.QuotaItem{
		resetItem("chatgpt", "weekly", "Weekly", 10, 90, later),
		resetItem("chatgpt", "session", "Session", 20, 80, soon),
		{ID: "no-reset", Provider: "chatgpt", Label: "No reset", Visible: true, Remaining: 1},
		{ID: "hidden", Provider: "chatgpt", Label: "Hidden", Visible: false, Reset: &model.Reset{ResetAt: soon, Source: "provider"}},
	}}

	res := ResetCalendar(usage, UsageAnalysisOptions{Now: now})
	if len(res.Resets) != 2 {
		t.Fatalf("resets=%+v", res.Resets)
	}
	if res.Resets[0].ItemID != "session" || res.Resets[1].ItemID != "weekly" {
		t.Fatalf("not sorted by resetAt: %+v", res.Resets)
	}
	if res.Resets[0].ResetSource != "provider" || res.Resets[0].Window.ID != "session" || res.Resets[0].PercentUsed != 20 {
		t.Fatalf("missing reset details: %+v", res.Resets[0])
	}
}

func TestAvailabilityEmptySnapshotIsConservative(t *testing.T) {
	usage := model.Usage{Providers: []model.Provider{{ID: "chatgpt", Label: "ChatGPT", State: model.ProviderStateStale}}}
	res := ProviderAvailabilitySummary(usage, UsageAnalysisOptions{Now: time.UnixMilli(1_000_000)})
	if len(res.Providers) != 1 || res.Providers[0].Available || res.Providers[0].Severity == "ok" {
		t.Fatalf("availability=%+v", res.Providers)
	}
	if !containsString(res.Providers[0].Blockers, "no cached quota data is available") {
		t.Fatalf("missing no-data blocker: %+v", res.Providers[0])
	}
}

func TestAvailabilityStaleAndErrorProviders(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	usage := model.Usage{
		Providers: []model.Provider{
			{ID: "stale", Label: "Stale", State: model.ProviderStateStale},
			{ID: "err", Label: "Err", State: model.ProviderStateError, Error: &model.ItemError{Message: "rate limited"}},
		},
		QuotaItems: []model.QuotaItem{
			resetItem("stale", "s", "S", 10, 90, now.Add(time.Hour).UnixMilli()),
			resetItem("err", "e", "E", 10, 90, now.Add(time.Hour).UnixMilli()),
		},
	}
	usage.QuotaItems[0].State = "stale"
	usage.QuotaItems[1].Severity = "error"
	usage.QuotaItems[1].Error = &model.ItemError{Message: errors.New("boom").Error()}

	res := ProviderAvailabilitySummary(usage, UsageAnalysisOptions{Now: now})
	if res.Providers[0].Available || res.Providers[0].Severity != "warning" || len(res.Providers[0].ConstrainedBy) == 0 {
		t.Fatalf("stale availability=%+v", res.Providers[0])
	}
	if res.Providers[1].Available || res.Providers[1].Severity != "error" || !containsString(res.Providers[1].Blockers, "provider refresh error: rate limited") {
		t.Fatalf("error availability=%+v", res.Providers[1])
	}
}

func TestAvailabilityMultipleQuotaWindowsConstrainsByLowestHeadroom(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	reset := now.Add(time.Hour).UnixMilli()
	usage := model.Usage{
		Providers: []model.Provider{{ID: "claude-code", Label: "Claude", State: model.ProviderStateFresh}},
		QuotaItems: []model.QuotaItem{
			resetItem("claude-code", "session", "Session", 20, 80, reset),
			resetItem("claude-code", "weekly", "Weekly", 95, 5, reset),
		},
	}
	res := ProviderAvailabilitySummary(usage, UsageAnalysisOptions{Now: now})
	if len(res.Providers) != 1 || !res.Providers[0].Available || res.Providers[0].Severity != "warning" {
		t.Fatalf("availability=%+v", res.Providers)
	}
	if len(res.Providers[0].ConstrainedBy) != 1 || res.Providers[0].ConstrainedBy[0].ItemID != "weekly" || res.Providers[0].NextImprovementAt == nil {
		t.Fatalf("constraints=%+v", res.Providers[0])
	}
}

func resetItem(provider, id, label string, used, remaining float64, resetAt int64) model.QuotaItem {
	return model.QuotaItem{ID: id, Provider: provider, Label: label, Window: model.Window{ID: id, Label: label, Kind: id, ResetAt: &resetAt}, Unit: "percent", Limit: 100, Used: used, Remaining: remaining, PercentUsed: used, State: "fresh", Severity: "ok", Visible: true, Reset: &model.Reset{ResetAt: resetAt, ResetWindowID: id, Source: "provider"}}
}

func containsString(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}
