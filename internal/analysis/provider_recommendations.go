package analysis

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jmalloc/usagent/internal/model"
)

const (
	TaskProfileGeneral            = "general"
	TaskProfileCheap              = "cheap"
	TaskProfileLongRunning        = "long-running"
	TaskProfileLatencyInsensitive = "latency-insensitive"
	TaskProfileUseItOrLoseIt      = "use-it-or-lose-it"
)

type ProviderRecommendationOptions struct {
	Now                     time.Time
	TaskProfile             string
	Providers               map[string]bool
	Tiers                   map[string]bool
	Tags                    map[string]bool
	MinimumRemainingPercent float64
	Unit                    string
	BurnRates               map[string]BurnRateEstimate
}

type ProviderRecommendationResponse struct {
	GeneratedAt      int64                             `json:"generatedAt"`
	TaskProfile      string                            `json:"taskProfile"`
	SelectedProvider string                            `json:"selectedProvider,omitempty"`
	Selected         *ProviderRecommendationCandidate  `json:"selected,omitempty"`
	RankedCandidates []ProviderRecommendationCandidate `json:"rankedCandidates"`
	Reasons          []string                          `json:"reasons"`
	Caveats          []string                          `json:"caveats"`
}

type ProviderRecommendationCandidate struct {
	Provider           string                     `json:"provider"`
	Label              string                     `json:"label,omitempty"`
	State              string                     `json:"state,omitempty"`
	Score              float64                    `json:"score"`
	AvailabilityScore  float64                    `json:"availabilityScore"`
	HeadroomScore      float64                    `json:"headroomScore"`
	LocalQuotaScore    float64                    `json:"localQuotaScore"`
	ExpiringUsageScore float64                    `json:"expiringUsageScore"`
	Confidence         string                     `json:"confidence"`
	ConstrainingItem   *RecommendationQuotaSignal `json:"constrainingItem,omitempty"`
	BestExpiringUsage  *ExpiringUsageOpportunity  `json:"bestExpiringUsage,omitempty"`
	Reasons            []string                   `json:"reasons"`
	Caveats            []string                   `json:"caveats"`
}

type RecommendationQuotaSignal struct {
	ItemID           string  `json:"itemId"`
	Label            string  `json:"label"`
	Unit             string  `json:"unit"`
	Remaining        float64 `json:"remaining"`
	PercentRemaining float64 `json:"percentRemaining"`
	State            string  `json:"state,omitempty"`
	Severity         string  `json:"severity,omitempty"`
}

// RecommendProvider ranks providers from the current cached usage snapshot. It
// performs no provider refreshes. The "cheap" profile is deliberately local:
// it means least scarce / lowest opportunity-cost cached quota, not external
// monetary price.
func RecommendProvider(usage model.Usage, opts ProviderRecommendationOptions) ProviderRecommendationResponse {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	profile := normalizeTaskProfile(opts.TaskProfile)
	out := ProviderRecommendationResponse{
		GeneratedAt:      now.UnixMilli(),
		TaskProfile:      profile,
		RankedCandidates: []ProviderRecommendationCandidate{},
		Reasons:          []string{},
		Caveats:          []string{},
	}
	if profile == TaskProfileCheap {
		out.Caveats = append(out.Caveats, "cheap profile means low local quota scarcity/opportunity cost, not true monetary price")
	}
	if opts.Unit != "" {
		out.Caveats = append(out.Caveats, "unit filter is applied only to quota items with matching cached units")
	}

	expiringByProvider := bestExpiringOpportunities(usage, opts, now)
	itemsByProvider := map[string][]model.QuotaItem{}
	for _, item := range usage.QuotaItems {
		itemsByProvider[item.Provider] = append(itemsByProvider[item.Provider], item)
	}

	seen := map[string]bool{}
	for _, provider := range usage.Providers {
		seen[provider.ID] = true
		candidate, ok := recommendationCandidate(provider, itemsByProvider[provider.ID], expiringByProvider[provider.ID], opts, profile)
		if ok {
			out.RankedCandidates = append(out.RankedCandidates, candidate)
		}
	}
	// Be permissive for synthetic snapshots that contain items for a provider not
	// listed in usage.Providers.
	for providerID, items := range itemsByProvider {
		if seen[providerID] {
			continue
		}
		if len(opts.Providers) > 0 && !opts.Providers[providerID] {
			continue
		}
		provider := model.Provider{ID: providerID, Label: providerID, State: model.ProviderStateFresh, Source: "cached-items"}
		candidate, ok := recommendationCandidate(provider, items, expiringByProvider[providerID], opts, profile)
		if ok {
			out.RankedCandidates = append(out.RankedCandidates, candidate)
		}
	}

	sort.SliceStable(out.RankedCandidates, func(i, j int) bool {
		left, right := out.RankedCandidates[i], out.RankedCandidates[j]
		if left.Score == right.Score {
			return left.Provider < right.Provider
		}
		return left.Score > right.Score
	})
	if len(out.RankedCandidates) > 0 && out.RankedCandidates[0].Score > 0 {
		out.SelectedProvider = out.RankedCandidates[0].Provider
		selected := out.RankedCandidates[0]
		out.Selected = &selected
		out.Reasons = append(out.Reasons, fmt.Sprintf("selected %s with score %g", selected.Provider, selected.Score))
		out.Reasons = append(out.Reasons, selected.Reasons...)
	} else {
		out.Caveats = append(out.Caveats, "no provider had positive cached recommendation score")
	}
	return out
}

func normalizeTaskProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "", TaskProfileGeneral:
		return TaskProfileGeneral
	case TaskProfileCheap:
		return TaskProfileCheap
	case TaskProfileLongRunning:
		return TaskProfileLongRunning
	case TaskProfileLatencyInsensitive:
		return TaskProfileLatencyInsensitive
	case TaskProfileUseItOrLoseIt, "use_it_or_lose_it", "use-it", "expiring":
		return TaskProfileUseItOrLoseIt
	default:
		return TaskProfileGeneral
	}
}

func bestExpiringOpportunities(usage model.Usage, opts ProviderRecommendationOptions, now time.Time) map[string]ExpiringUsageOpportunity {
	expiring := ExpiringUsage(usage, ExpiringUsageOptions{
		Now:                     now,
		MinimumRemainingPercent: opts.MinimumRemainingPercent,
		Providers:               opts.Providers,
		Tiers:                   opts.Tiers,
		Tags:                    opts.Tags,
		IncludeLowConfidence:    true,
		BurnRates:               opts.BurnRates,
	})
	out := map[string]ExpiringUsageOpportunity{}
	for _, op := range expiring.Opportunities {
		if opts.Unit != "" && !strings.EqualFold(op.Unit, opts.Unit) {
			continue
		}
		best, ok := out[op.Provider]
		if !ok || op.OpportunityScore > best.OpportunityScore {
			out[op.Provider] = op
		}
	}
	return out
}

func recommendationCandidate(provider model.Provider, items []model.QuotaItem, bestExpiring ExpiringUsageOpportunity, opts ProviderRecommendationOptions, profile string) (ProviderRecommendationCandidate, bool) {
	if provider.ID == "" {
		return ProviderRecommendationCandidate{}, false
	}
	if len(opts.Providers) > 0 && !opts.Providers[provider.ID] {
		return ProviderRecommendationCandidate{}, false
	}
	providerMatchesMetadata := recommendationMetadataMatches(provider, model.QuotaItem{}, opts)
	filtered := make([]model.QuotaItem, 0, len(items))
	for _, item := range items {
		if !item.Visible || isResetCreditBankItem(item) {
			continue
		}
		if hasRecommendationMetadataFilters(opts) && !recommendationMetadataMatches(provider, item, opts) {
			continue
		}
		if opts.Unit != "" && !strings.EqualFold(unitOrDefault(item.Unit), opts.Unit) {
			continue
		}
		filtered = append(filtered, item)
	}
	if opts.Unit != "" && len(filtered) == 0 {
		return ProviderRecommendationCandidate{}, false
	}
	if hasRecommendationMetadataFilters(opts) && len(filtered) == 0 && !providerMatchesMetadata {
		return ProviderRecommendationCandidate{}, false
	}

	candidate := ProviderRecommendationCandidate{
		Provider:   provider.ID,
		Label:      provider.Label,
		State:      string(provider.State),
		Confidence: ConfidenceMedium,
		Reasons:    []string{},
		Caveats:    []string{},
	}
	availability := availabilityScore(provider, filtered, &candidate)
	candidate.AvailabilityScore = round3(availability)

	headroom, avgHeadroom, constraining, hasQuota := headroomSignals(filtered)
	if hasQuota {
		candidate.HeadroomScore = round3(headroom)
		candidate.LocalQuotaScore = round3(headroom * availability)
		candidate.ConstrainingItem = constraining
		candidate.Reasons = append(candidate.Reasons, fmt.Sprintf("most constraining cached quota item %s has %g%% remaining", constraining.ItemID, constraining.PercentRemaining))
		if opts.MinimumRemainingPercent > 0 && constraining.PercentRemaining < opts.MinimumRemainingPercent {
			return ProviderRecommendationCandidate{}, false
		}
		if constraining.PercentRemaining <= 15 {
			candidate.Caveats = append(candidate.Caveats, "provider has scarce local quota; recommendation protects constrained quota")
		}
	} else {
		if opts.MinimumRemainingPercent > 0 {
			return ProviderRecommendationCandidate{}, false
		}
		candidate.Confidence = ConfidenceLow
		candidate.Caveats = append(candidate.Caveats, "no visible cached quota items are available for this provider")
	}

	if bestExpiring.Provider != "" {
		op := bestExpiring
		candidate.BestExpiringUsage = &op
		candidate.ExpiringUsageScore = round3(op.OpportunityScore)
		candidate.Reasons = append(candidate.Reasons, fmt.Sprintf("best expiring-usage signal %s has opportunity score %g", op.ItemID, op.OpportunityScore))
		if op.Confidence == ConfidenceLow {
			candidate.Caveats = append(candidate.Caveats, "expiring-usage signal is low confidence")
		}
	}

	score := recommendationScore(profile, headroom, avgHeadroom, availability, candidate.ExpiringUsageScore)
	candidate.Score = round3(score)
	if profile == TaskProfileCheap {
		candidate.Reasons = append(candidate.Reasons, "cheap ranking uses local headroom and expiring quota, not external price")
	}
	if profile == TaskProfileUseItOrLoseIt {
		if candidate.ExpiringUsageScore > 0 {
			candidate.Reasons = append(candidate.Reasons, "use-it-or-lose-it ranking prioritizes genuinely expiring actionable quota")
		} else {
			candidate.Caveats = append(candidate.Caveats, "no actionable expiring-usage opportunity was found for this provider")
		}
	}
	return candidate, true
}

func hasRecommendationMetadataFilters(opts ProviderRecommendationOptions) bool {
	return len(opts.Tiers) > 0 || len(opts.Tags) > 0
}

func recommendationMetadataMatches(provider model.Provider, item model.QuotaItem, opts ProviderRecommendationOptions) bool {
	if len(opts.Tiers) > 0 && !recommendationMatchesTier(provider, item, opts.Tiers) {
		return false
	}
	if len(opts.Tags) > 0 && !recommendationMatchesTags(provider, item, opts.Tags) {
		return false
	}
	return true
}

func recommendationMatchesTier(provider model.Provider, item model.QuotaItem, tiers map[string]bool) bool {
	return tiers[strings.ToLower(provider.Tier)] || tiers[strings.ToLower(item.ProviderTier)] || tiers[strings.ToLower(item.ModelTier)]
}

func recommendationMatchesTags(provider model.Provider, item model.QuotaItem, tags map[string]bool) bool {
	for _, tag := range provider.Tags {
		if tags[strings.ToLower(tag)] {
			return true
		}
	}
	for _, tag := range item.ProviderTags {
		if tags[strings.ToLower(tag)] {
			return true
		}
	}
	for _, tag := range item.ModelTags {
		if tags[strings.ToLower(tag)] {
			return true
		}
	}
	return false
}

func availabilityScore(provider model.Provider, items []model.QuotaItem, candidate *ProviderRecommendationCandidate) float64 {
	score := 1.0
	switch provider.State {
	case model.ProviderStateError:
		score = 0.1
		candidate.Confidence = ConfidenceLow
		if provider.Error != nil && strings.TrimSpace(provider.Error.Message) != "" {
			candidate.Caveats = append(candidate.Caveats, "provider refresh error: "+provider.Error.Message)
		} else {
			candidate.Caveats = append(candidate.Caveats, "provider is in error state")
		}
	case model.ProviderStateStale:
		score = 0.45
		candidate.Confidence = ConfidenceLow
		candidate.Caveats = append(candidate.Caveats, "provider cache is stale")
	case model.ProviderStateFresh, "":
		score = 1
	default:
		score = 0.7
		candidate.Caveats = append(candidate.Caveats, "provider state is unknown")
	}
	for _, item := range items {
		if item.State == "error" || item.Severity == "error" || item.Error != nil {
			score = minFloat(score, 0.15)
			candidate.Confidence = ConfidenceLow
			if item.Error != nil && strings.TrimSpace(item.Error.Message) != "" {
				candidate.Caveats = append(candidate.Caveats, "quota item refresh error: "+item.Error.Message)
			} else {
				candidate.Caveats = append(candidate.Caveats, "quota item has error metadata")
			}
		}
		if item.State == "stale" {
			score = minFloat(score, 0.45)
			candidate.Confidence = ConfidenceLow
			candidate.Caveats = append(candidate.Caveats, "quota item cache is stale")
		}
	}
	return score
}

func headroomSignals(items []model.QuotaItem) (minScore float64, avgScore float64, constraining *RecommendationQuotaSignal, ok bool) {
	if len(items) == 0 {
		return 0, 0, nil, false
	}
	minPercent := 101.0
	sum := 0.0
	var chosen model.QuotaItem
	for _, item := range items {
		pct := percentRemaining(item)
		if pct < minPercent {
			minPercent = pct
			chosen = item
		}
		sum += pct
	}
	if minPercent == 101 {
		return 0, 0, nil, false
	}
	unit := unitOrDefault(chosen.Unit)
	return clamp(minPercent/100, 0, 1), clamp((sum/float64(len(items)))/100, 0, 1), &RecommendationQuotaSignal{
		ItemID:           chosen.ID,
		Label:            chosen.Label,
		Unit:             unit,
		Remaining:        round3(chosen.Remaining),
		PercentRemaining: round3(minPercent),
		State:            chosen.State,
		Severity:         chosen.Severity,
	}, true
}

func recommendationScore(profile string, headroom, avgHeadroom, availability, expiring float64) float64 {
	safeHeadroom := headroom * availability
	safeAverage := avgHeadroom * availability
	safeExpiring := expiring * availability
	switch profile {
	case TaskProfileCheap:
		return 0.80*safeHeadroom + 0.20*safeExpiring
	case TaskProfileLongRunning:
		return 0.90*safeHeadroom + 0.05*safeAverage + 0.05*safeExpiring
	case TaskProfileLatencyInsensitive:
		return 0.70*safeHeadroom + 0.30*safeExpiring
	case TaskProfileUseItOrLoseIt:
		return 0.80*safeExpiring + 0.15*safeHeadroom + 0.05*availability
	default:
		return 0.75*safeHeadroom + 0.15*safeAverage + 0.10*safeExpiring
	}
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
