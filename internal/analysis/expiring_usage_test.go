package analysis

import (
	"testing"
	"time"

	"github.com/jmalloc/usagent/internal/model"
)

func TestExpiringUsageHighOpportunity(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	usage := model.Usage{QuotaItems: []model.QuotaItem{
		quotaItem("claude-code", "claude-code-oauth-session", "Claude 5h", 20, 80, now.Add(time.Hour)),
	}}

	res := ExpiringUsage(usage, ExpiringUsageOptions{Now: now, Within: 24 * time.Hour, MinimumRemainingPercent: 10})
	if len(res.Opportunities) != 1 {
		t.Fatalf("opportunities=%+v", res.Opportunities)
	}
	op := res.Opportunities[0]
	if op.Provider != "claude-code" || op.ItemID != "claude-code-oauth-session" || op.Urgency != "extreme" || op.Confidence != ConfidenceMedium {
		t.Fatalf("op=%+v", op)
	}
	if op.EstimatedNaturalUseBeforeReset != 5 || op.EstimatedWastedAmount != 75 || op.EstimatedWastedPercent != 75 {
		t.Fatalf("unexpected estimates: %+v", op)
	}
	if op.OpportunityScore <= 0.5 {
		t.Fatalf("score too low: %+v", op)
	}
}

func TestExpiringUsagePrefersHistoricalBurnRate(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	usage := model.Usage{QuotaItems: []model.QuotaItem{
		quotaItem("claude-code", "claude-code-weekly", "Claude weekly", 1, 99, now.Add(24*time.Hour)),
	}}

	res := ExpiringUsage(usage, ExpiringUsageOptions{Now: now, BurnRates: map[string]BurnRateEstimate{
		BurnRateKey("claude-code", "claude-code-weekly"): {BurnPerMs: 80 / float64((24 * time.Hour).Milliseconds()), Source: "1h local history burn rate", Confidence: ConfidenceHigh, Samples: 4},
	}})
	if len(res.Opportunities) != 1 {
		t.Fatalf("opportunities=%+v", res.Opportunities)
	}
	op := res.Opportunities[0]
	if op.Confidence != ConfidenceHigh || op.EstimatedNaturalUseBeforeReset != 80 || op.EstimatedWastedAmount != 19 {
		t.Fatalf("historical estimate not used: %+v", op)
	}
	if !contains(op.Caveats, "natural use estimated from 1h local history burn rate (4 samples)") {
		t.Fatalf("missing history caveat: %+v", op.Caveats)
	}
}

func TestExpiringUsageFiltersLowRemainingQuota(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	usage := model.Usage{QuotaItems: []model.QuotaItem{
		quotaItem("chatgpt", "chatgpt-primary", "ChatGPT 5h", 95, 5, now.Add(time.Hour)),
	}}

	res := ExpiringUsage(usage, ExpiringUsageOptions{Now: now, MinimumRemainingPercent: 10})
	if len(res.Opportunities) != 0 {
		t.Fatalf("expected low remaining item to be filtered, got %+v", res.Opportunities)
	}
}

func TestExpiringUsageSkipsItemsWithoutResetTime(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	item := quotaItem("chatgpt", "chatgpt-primary", "ChatGPT 5h", 20, 80, now.Add(time.Hour))
	item.Reset = nil
	item.Window.ResetAt = nil
	usage := model.Usage{QuotaItems: []model.QuotaItem{item}}

	res := ExpiringUsage(usage, ExpiringUsageOptions{Now: now, IncludeLowConfidence: true})
	if len(res.Opportunities) != 0 {
		t.Fatalf("expected no reset time to be skipped, got %+v", res.Opportunities)
	}
}

func TestExpiringUsageStaleItemsAreLowConfidence(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	item := quotaItem("chatgpt", "chatgpt-primary", "ChatGPT 5h", 20, 80, now.Add(time.Hour))
	item.State = "stale"
	usage := model.Usage{QuotaItems: []model.QuotaItem{item}}

	res := ExpiringUsage(usage, ExpiringUsageOptions{Now: now})
	if len(res.Opportunities) != 0 {
		t.Fatalf("expected stale low-confidence item to be excluded by default, got %+v", res.Opportunities)
	}
	res = ExpiringUsage(usage, ExpiringUsageOptions{Now: now, IncludeLowConfidence: true})
	if len(res.Opportunities) != 1 || res.Opportunities[0].Confidence != ConfidenceLow {
		t.Fatalf("expected stale item with low confidence when requested, got %+v", res.Opportunities)
	}
	if !contains(res.Opportunities[0].Caveats, "cached quota item is stale") {
		t.Fatalf("missing stale caveat: %+v", res.Opportunities[0].Caveats)
	}
}

func TestExpiringUsageInfersRollingSessionWindow(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	item := quotaItem("chatgpt", "chatgpt-primary", "ChatGPT 5h", 10, 90, now.Add(30*time.Minute))
	item.Window = model.Window{ID: "session", Label: "S", Kind: "rolling", ResetAt: item.Window.ResetAt}
	usage := model.Usage{QuotaItems: []model.QuotaItem{item}}

	res := ExpiringUsage(usage, ExpiringUsageOptions{Now: now, Within: time.Hour})
	if len(res.Opportunities) != 1 {
		t.Fatalf("opportunities=%+v", res.Opportunities)
	}
	op := res.Opportunities[0]
	if op.Confidence != ConfidenceMedium || op.EstimatedNaturalUseBeforeReset < 1 || op.EstimatedNaturalUseBeforeReset > 2 {
		t.Fatalf("rolling/session inference failed: %+v", op)
	}
}

func TestExpiringUsageIncludesAndFiltersMetadata(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	chat := quotaItem("chatgpt", "chatgpt-primary", "ChatGPT 5h", 20, 80, now.Add(time.Hour))
	chat.ProviderTier = "high"
	chat.ProviderTags = []string{"chat", "subscription"}
	chat.ModelTier = "extra-high"
	chat.ModelTags = []string{"codex"}
	zai := quotaItem("z-ai", "z-ai-daily", "z.ai Daily", 20, 80, now.Add(time.Hour))
	zai.ProviderTier = "medium"
	zai.ProviderTags = []string{"api"}
	usage := model.Usage{QuotaItems: []model.QuotaItem{chat, zai}}

	res := ExpiringUsage(usage, ExpiringUsageOptions{Now: now, Tiers: map[string]bool{"extra-high": true}})
	if len(res.Opportunities) != 1 || res.Opportunities[0].Provider != "chatgpt" || res.Opportunities[0].ModelTier != "extra-high" || res.Opportunities[0].ProviderTags[1] != "subscription" {
		t.Fatalf("tier filtered response=%+v", res.Opportunities)
	}
	res = ExpiringUsage(usage, ExpiringUsageOptions{Now: now, Tags: map[string]bool{"api": true}})
	if len(res.Opportunities) != 1 || res.Opportunities[0].Provider != "z-ai" || res.Opportunities[0].ProviderTier != "medium" {
		t.Fatalf("tag filtered response=%+v", res.Opportunities)
	}
	res = ExpiringUsage(usage, ExpiringUsageOptions{Now: now, Tiers: map[string]bool{"low": true}})
	if len(res.Opportunities) != 0 {
		t.Fatalf("expected tier filter to exclude all, got %+v", res.Opportunities)
	}
}

func TestExpiringUsageFiltersProvidersAndResetCreditBankItems(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	usage := model.Usage{QuotaItems: []model.QuotaItem{
		quotaItem("chatgpt", "chatgpt-rate-limit-reset-credits", "Reset credits", 0, 1, now.Add(time.Hour)),
		quotaItem("z-ai", "z-ai-daily", "z.ai Daily", 20, 80, now.Add(time.Hour)),
		quotaItem("claude-code", "claude-code-oauth-session", "Claude 5h", 20, 80, now.Add(time.Hour)),
	}}

	res := ExpiringUsage(usage, ExpiringUsageOptions{Now: now, Providers: map[string]bool{"claude-code": true}})
	if len(res.Opportunities) != 1 || res.Opportunities[0].Provider != "claude-code" {
		t.Fatalf("unexpected provider filtering/reset credit exclusion: %+v", res.Opportunities)
	}
}

func TestExpiringUsageFreshParentDownweightsShortWindowWaste(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	usage := model.Usage{QuotaItems: []model.QuotaItem{
		quotaItemWithWindow("chatgpt", "chatgpt-primary", "ChatGPT 5h", "session", "S", "rolling", 20, 80, now.Add(time.Hour)),
		quotaItemWithWindow("chatgpt", "chatgpt-secondary", "ChatGPT weekly", "weekly", "W", "weekly", 5, 95, now.Add(6*24*time.Hour)),
	}}

	res := ExpiringUsage(usage, ExpiringUsageOptions{Now: now, Within: 24 * time.Hour})
	if len(res.Opportunities) != 1 {
		t.Fatalf("opportunities=%+v", res.Opportunities)
	}
	op := res.Opportunities[0]
	if op.RawEstimatedWastedAmount != 75 || op.EstimatedWastedAmount != 75 {
		t.Fatalf("raw waste should remain visible: %+v", op)
	}
	if op.ActionableWasteAmount >= 40 || op.ActionableWasteAmount <= 0 {
		t.Fatalf("fresh parent should substantially downweight actionable waste: %+v", op)
	}
	if op.OpportunityScore >= 0.2 || op.OpportunityScore <= 0 {
		t.Fatalf("score should be based on actionable waste, not raw expiring unused: %+v", op)
	}
	if op.OverlapContext == nil || !op.OverlapContext.FreshParentFutureCapacityDownweighted || op.OverlapContext.ParentItemID != "chatgpt-secondary" {
		t.Fatalf("missing fresh parent overlap context: %+v", op.OverlapContext)
	}
	if !contains(op.Caveats, "parent-window adjustment uses percent quota signals only and does not claim exact request or token capacity") {
		t.Fatalf("missing percent-only caveat: %+v", op.Caveats)
	}
}

func TestExpiringUsageParentNearResetKeepsShortWindowActionable(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	usage := model.Usage{QuotaItems: []model.QuotaItem{
		quotaItemWithWindow("chatgpt", "chatgpt-primary", "ChatGPT 5h", "session", "S", "rolling", 5, 95, now.Add(4*time.Hour)),
		quotaItemWithWindow("chatgpt", "chatgpt-secondary", "ChatGPT weekly", "weekly", "W", "weekly", 10, 90, now.Add(time.Hour)),
	}}

	res := ExpiringUsage(usage, ExpiringUsageOptions{Now: now, Within: 24 * time.Hour})
	op, ok := opportunityByID(res, "chatgpt-primary")
	if !ok {
		t.Fatalf("missing short-window opportunity: %+v", res.Opportunities)
	}
	if op.RawEstimatedWastedAmount != 75 || op.ActionableWasteAmount != 75 {
		t.Fatalf("near parent reset should leave current short-window waste actionable: %+v", op)
	}
	if op.Urgency != "extreme" {
		t.Fatalf("parent reset should increase urgency: %+v", op)
	}
	if op.OverlapContext == nil || !op.OverlapContext.NearParentResetOrExhaustionUpweighted {
		t.Fatalf("missing near-reset overlap context: %+v", op.OverlapContext)
	}
}

func TestExpiringUsageConstrainedParentCapsSafeShortWindowSpend(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	usage := model.Usage{QuotaItems: []model.QuotaItem{
		quotaItemWithWindow("claude-code", "claude-code-oauth-session", "Claude 5h", "session", "S", "rolling", 20, 80, now.Add(time.Hour)),
		quotaItemWithWindow("claude-code", "claude-code-oauth-weekly-all", "Claude weekly", "weekly", "W", "weekly", 88, 12, now.Add(24*time.Hour)),
	}}

	res := ExpiringUsage(usage, ExpiringUsageOptions{Now: now, Within: 24 * time.Hour})
	if len(res.Opportunities) != 1 {
		t.Fatalf("opportunities=%+v", res.Opportunities)
	}
	op := res.Opportunities[0]
	if op.RawEstimatedWastedAmount != 75 || op.ActionableWasteAmount != 12 {
		t.Fatalf("parent remaining quota should cap safe actionable waste: %+v", op)
	}
	if op.OverlapContext == nil || !op.OverlapContext.ParentCapacityLimitedActionableWaste || op.OverlapContext.ParentRemaining != 12 {
		t.Fatalf("missing constrained parent context: %+v", op.OverlapContext)
	}
}

func TestExpiringUsageIndependentWindowsStayIndependentlyScored(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	usage := model.Usage{QuotaItems: []model.QuotaItem{
		quotaItemWithWindow("openai", "openai-cost-weekly-usd", "OpenAI weekly", "week", "W", "weekly", 20, 80, now.Add(time.Hour)),
		quotaItemWithWindow("openai", "openai-cost-monthly-usd", "OpenAI monthly", "month", "M", "monthly", 20, 80, now.Add(time.Hour)),
	}}

	res := ExpiringUsage(usage, ExpiringUsageOptions{Now: now, Within: 24 * time.Hour})
	if len(res.Opportunities) != 2 {
		t.Fatalf("opportunities=%+v", res.Opportunities)
	}
	for _, op := range res.Opportunities {
		if op.OverlapContext != nil || op.ActionableWasteAmount != op.RawEstimatedWastedAmount {
			t.Fatalf("independent item was overlap-adjusted: %+v", op)
		}
	}
}

func quotaItem(provider, id, label string, used, remaining float64, resetAt time.Time) model.QuotaItem {
	return quotaItemWithWindow(provider, id, label, "session", "S", "rolling", used, remaining, resetAt)
}

func quotaItemWithWindow(provider, id, label, windowID, windowLabel, windowKind string, used, remaining float64, resetAt time.Time) model.QuotaItem {
	resetMs := resetAt.UnixMilli()
	return model.QuotaItem{
		ID:          id,
		Provider:    provider,
		Label:       label,
		Window:      model.Window{ID: windowID, Label: windowLabel, Kind: windowKind, ResetAt: &resetMs},
		Unit:        "percent",
		Limit:       100,
		Used:        used,
		Remaining:   remaining,
		PercentUsed: used,
		State:       "fresh",
		Severity:    "ok",
		Visible:     true,
		Reset:       &model.Reset{ResetAt: resetMs, ResetWindowID: windowID, Source: "provider"},
	}
}

func opportunityByID(res ExpiringUsageResponse, id string) (ExpiringUsageOpportunity, bool) {
	for _, op := range res.Opportunities {
		if op.ItemID == id {
			return op, true
		}
	}
	return ExpiringUsageOpportunity{}, false
}

func contains(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}
