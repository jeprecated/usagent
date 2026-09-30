package analysis

import (
	"reflect"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/model"
)

func TestCreditBalancesDoNotChangeSubscriptionAvailabilityOrRecommendations(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	for _, remaining := range []float64{0, 80} {
		usage := model.Usage{
			Providers: []model.Provider{{ID: "chatgpt", Label: "ChatGPT", State: model.ProviderStateFresh}},
			QuotaItems: []model.QuotaItem{
				quotaItemWithWindow("chatgpt", "chatgpt-secondary", "ChatGPT weekly", "weekly", "W", "weekly", 100-remaining, remaining, now.Add(time.Hour)),
			},
		}
		availability := ProviderAvailabilitySummary(usage, UsageAnalysisOptions{Now: now})
		recommendation := RecommendProvider(usage, ProviderRecommendationOptions{Now: now})
		for _, balance := range []float64{0, 62500} {
			withCredits := usage
			withCredits.QuotaItems = append(append([]model.QuotaItem{}, usage.QuotaItems...), creditBalance(balance))
			if got := ProviderAvailabilitySummary(withCredits, UsageAnalysisOptions{Now: now}); !reflect.DeepEqual(got, availability) {
				t.Fatalf("balance %g changed availability for %g%% quota: got=%+v want=%+v", balance, remaining, got, availability)
			}
			if got := RecommendProvider(withCredits, ProviderRecommendationOptions{Now: now}); !reflect.DeepEqual(got, recommendation) {
				t.Fatalf("balance %g changed recommendation for %g%% quota: got=%+v want=%+v", balance, remaining, got, recommendation)
			}
		}
	}
}

func TestCreditBalanceHasNoInferredPercentageExpiryOrPace(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	usage := model.Usage{QuotaItems: []model.QuotaItem{creditBalance(62500)}}
	item := AnalyzeUsage(usage, UsageAnalysisOptions{Now: now}).Items[0]
	if item.PercentRemaining != 0 || item.Confidence != ConfidenceLow || !analysisCaveatsContain(item.Caveats, "percent remaining could not be derived") {
		t.Fatalf("invented percentage: %+v", item)
	}
	if item.ResetAt != nil || item.Pace != nil || item.ProjectedExhaustion != nil {
		t.Fatalf("invented expiry/pace: %+v", item)
	}
	if got := ExpiringUsage(usage, ExpiringUsageOptions{Now: now, IncludeLowConfidence: true}); len(got.Opportunities) != 0 {
		t.Fatalf("invented credit expiry: %+v", got)
	}
	if got := ResetCalendar(usage, UsageAnalysisOptions{Now: now}); len(got.Resets) != 0 {
		t.Fatalf("invented credit reset: %+v", got)
	}
}

func creditBalance(balance float64) model.QuotaItem {
	return model.QuotaItem{ID: "chatgpt-spending-credits", Provider: "chatgpt", Label: "ChatGPT spending credits", Window: model.Window{ID: "spendingCredits", Label: "Credits", Kind: "credit"}, Unit: "credits", Remaining: balance, State: "fresh", Severity: "ok", Visible: true}
}
