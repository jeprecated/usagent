package analysis

import (
	"strings"
	"testing"
	"time"

	"github.com/jmalloc/usagent/internal/model"
)

func TestRecommendProviderRanksHealthyHeadroom(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	usage := model.Usage{
		Providers: []model.Provider{
			{ID: "claude-code", Label: "Claude", State: model.ProviderStateFresh},
			{ID: "chatgpt", Label: "ChatGPT", State: model.ProviderStateFresh},
		},
		QuotaItems: []model.QuotaItem{
			quotaItemWithWindow("claude-code", "claude-code-oauth-weekly-all", "Claude weekly", "weekly", "W", "weekly", 60, 40, now.Add(6*24*time.Hour)),
			quotaItemWithWindow("chatgpt", "chatgpt-secondary", "ChatGPT weekly", "weekly", "W", "weekly", 20, 80, now.Add(6*24*time.Hour)),
		},
	}

	res := RecommendProvider(usage, ProviderRecommendationOptions{Now: now})
	if res.SelectedProvider != "chatgpt" || len(res.RankedCandidates) != 2 {
		t.Fatalf("response=%+v", res)
	}
	if res.RankedCandidates[0].Score <= res.RankedCandidates[1].Score || res.RankedCandidates[0].HeadroomScore != 0.8 {
		t.Fatalf("bad ranking/scores: %+v", res.RankedCandidates)
	}
}

func TestRecommendProviderPenalizesStaleAndErrorProviders(t *testing.T) {
	now := time.UnixMilli(2_000_000)
	stale := quotaItemWithWindow("claude-code", "claude-code-oauth-weekly-all", "Claude weekly", "weekly", "W", "weekly", 1, 99, now.Add(6*24*time.Hour))
	stale.State = "stale"
	errorItem := quotaItemWithWindow("z-ai", "z-ai-weekly", "z.ai weekly", "weekly", "W", "weekly", 1, 99, now.Add(6*24*time.Hour))
	errorItem.Severity = "error"
	errorItem.Error = &model.ItemError{Provider: "z-ai", Code: "refresh_failed", Message: "boom", LastOccurredAt: now.UnixMilli(), Recoverable: true}
	usage := model.Usage{
		Providers: []model.Provider{
			{ID: "chatgpt", Label: "ChatGPT", State: model.ProviderStateFresh},
			{ID: "claude-code", Label: "Claude", State: model.ProviderStateStale},
			{ID: "z-ai", Label: "z.ai", State: model.ProviderStateError, Error: errorItem.Error},
		},
		QuotaItems: []model.QuotaItem{
			quotaItemWithWindow("chatgpt", "chatgpt-secondary", "ChatGPT weekly", "weekly", "W", "weekly", 40, 60, now.Add(6*24*time.Hour)),
			stale,
			errorItem,
		},
	}

	res := RecommendProvider(usage, ProviderRecommendationOptions{Now: now})
	if res.SelectedProvider != "chatgpt" {
		t.Fatalf("stale/error providers should not outrank fresh provider: %+v", res.RankedCandidates)
	}
	byProvider := recommendationCandidatesByProvider(res.RankedCandidates)
	if byProvider["claude-code"].AvailabilityScore >= 1 || byProvider["claude-code"].Confidence != ConfidenceLow {
		t.Fatalf("stale provider was not penalized: %+v", byProvider["claude-code"])
	}
	if byProvider["z-ai"].AvailabilityScore >= byProvider["claude-code"].AvailabilityScore || !recommendationContains(byProvider["z-ai"].Caveats, "boom") {
		t.Fatalf("error provider was not penalized/explained: %+v", byProvider["z-ai"])
	}
}

func TestRecommendProviderCheapProtectsConstrainedWeeklyQuota(t *testing.T) {
	now := time.UnixMilli(3_000_000)
	usage := model.Usage{
		Providers: []model.Provider{
			{ID: "claude-code", Label: "Claude", State: model.ProviderStateFresh},
			{ID: "chatgpt", Label: "ChatGPT", State: model.ProviderStateFresh},
		},
		QuotaItems: []model.QuotaItem{
			quotaItemWithWindow("claude-code", "claude-code-oauth-session", "Claude 5h", "session", "S", "rolling", 10, 90, now.Add(time.Hour)),
			quotaItemWithWindow("claude-code", "claude-code-oauth-weekly-all", "Claude weekly", "weekly", "W", "weekly", 90, 10, now.Add(24*time.Hour)),
			quotaItemWithWindow("chatgpt", "chatgpt-secondary", "ChatGPT weekly", "weekly", "W", "weekly", 45, 55, now.Add(4*24*time.Hour)),
		},
	}

	res := RecommendProvider(usage, ProviderRecommendationOptions{Now: now, TaskProfile: TaskProfileCheap})
	if res.SelectedProvider != "chatgpt" {
		t.Fatalf("cheap should protect constrained weekly quota: %+v", res.RankedCandidates)
	}
	claude := recommendationCandidatesByProvider(res.RankedCandidates)["claude-code"]
	if claude.ConstrainingItem == nil || claude.ConstrainingItem.ItemID != "claude-code-oauth-weekly-all" || claude.LocalQuotaScore >= 0.2 {
		t.Fatalf("claude weekly constraint not reflected: %+v", claude)
	}
	if !recommendationContains(claude.Caveats, "scarce local quota") || !recommendationContains(res.Caveats, "not true monetary price") {
		t.Fatalf("missing cheap/scarcity caveats: res=%+v claude=%+v", res.Caveats, claude.Caveats)
	}
}

func TestRecommendProviderUseItOrLoseItPrioritizesGenuinelyExpiringQuota(t *testing.T) {
	now := time.UnixMilli(4_000_000)
	usage := model.Usage{
		Providers: []model.Provider{
			{ID: "chatgpt", Label: "ChatGPT", State: model.ProviderStateFresh},
			{ID: "z-ai", Label: "z.ai", State: model.ProviderStateFresh},
		},
		QuotaItems: []model.QuotaItem{
			quotaItemWithWindow("chatgpt", "chatgpt-primary", "ChatGPT 5h", "session", "S", "rolling", 20, 80, now.Add(time.Hour)),
			quotaItemWithWindow("z-ai", "z-ai-daily", "z.ai daily", "daily", "D", "daily", 20, 80, now.Add(time.Hour)),
		},
	}

	res := RecommendProvider(usage, ProviderRecommendationOptions{
		Now:         now,
		TaskProfile: TaskProfileUseItOrLoseIt,
		BurnRates: map[string]BurnRateEstimate{
			BurnRateKey("chatgpt", "chatgpt-primary"): {BurnPerMs: 80 / float64(time.Hour.Milliseconds()), Source: "1h local history burn rate", Confidence: ConfidenceHigh, Samples: 3},
		},
	})
	if res.SelectedProvider != "z-ai" {
		t.Fatalf("use-it-or-lose-it should prefer genuinely expiring quota: %+v", res.RankedCandidates)
	}
	byProvider := recommendationCandidatesByProvider(res.RankedCandidates)
	if byProvider["chatgpt"].BestExpiringUsage != nil || byProvider["chatgpt"].ExpiringUsageScore != 0 {
		t.Fatalf("chatgpt should not have an expiring opportunity when history consumes remainder: %+v", byProvider["chatgpt"])
	}
	if byProvider["z-ai"].BestExpiringUsage == nil || byProvider["z-ai"].ExpiringUsageScore <= 0 {
		t.Fatalf("z-ai should expose expiring opportunity: %+v", byProvider["z-ai"])
	}
}

func TestRecommendProviderFiltersProviderMinimumRemainingAndUnit(t *testing.T) {
	now := time.UnixMilli(5_000_000)
	usage := model.Usage{
		Providers: []model.Provider{
			{ID: "chatgpt", Label: "ChatGPT", State: model.ProviderStateFresh},
			{ID: "openai", Label: "OpenAI", State: model.ProviderStateFresh},
		},
		QuotaItems: []model.QuotaItem{
			quotaItemWithWindow("chatgpt", "chatgpt-primary", "ChatGPT 5h", "session", "S", "rolling", 10, 90, now.Add(time.Hour)),
			quotaItemWithWindow("openai", "openai-cost-weekly-usd", "OpenAI weekly", "weekly", "W", "weekly", 10, 90, now.Add(time.Hour)),
		},
	}
	usage.QuotaItems[1].Unit = "usd"
	usage.QuotaItems[1].Limit = 100

	res := RecommendProvider(usage, ProviderRecommendationOptions{
		Now:                     now,
		Providers:               map[string]bool{"chatgpt": true},
		MinimumRemainingPercent: 20,
		Unit:                    "percent",
	})
	if len(res.RankedCandidates) != 1 || res.SelectedProvider != "chatgpt" {
		t.Fatalf("filters not applied: %+v", res)
	}
}

func recommendationCandidatesByProvider(candidates []ProviderRecommendationCandidate) map[string]ProviderRecommendationCandidate {
	out := map[string]ProviderRecommendationCandidate{}
	for _, candidate := range candidates {
		out[candidate.Provider] = candidate
	}
	return out
}

func recommendationContains(list []string, substr string) bool {
	for _, got := range list {
		if strings.Contains(got, substr) {
			return true
		}
	}
	return false
}
