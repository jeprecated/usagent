package analysis

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jmalloc/usagent/internal/model"
)

const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

type ExpiringUsageOptions struct {
	Now                     time.Time
	Within                  time.Duration
	MinimumRemainingPercent float64
	Providers               map[string]bool
	IncludeLowConfidence    bool
	BurnRates               map[string]BurnRateEstimate
}

type BurnRateEstimate struct {
	BurnPerMs    float64
	Source       string
	Confidence   string
	Since        int64
	Until        int64
	Samples      int
	WindowToDate bool
}

func BurnRateKey(provider, itemID string) string { return provider + "\x00" + itemID }

type ExpiringUsageResponse struct {
	GeneratedAt   int64                      `json:"generatedAt"`
	Opportunities []ExpiringUsageOpportunity `json:"opportunities"`
}

type ExpiringUsageOpportunity struct {
	Provider                       string   `json:"provider"`
	ItemID                         string   `json:"itemId"`
	Label                          string   `json:"label"`
	Unit                           string   `json:"unit"`
	Remaining                      float64  `json:"remaining"`
	PercentRemaining               float64  `json:"percentRemaining"`
	ProviderTier                   string   `json:"providerTier,omitempty"`
	ProviderTags                   []string `json:"providerTags,omitempty"`
	ResetAt                        int64    `json:"resetAt"`
	TimeRemainingMs                int64    `json:"timeRemainingMs"`
	EstimatedNaturalUseBeforeReset float64  `json:"estimatedNaturalUseBeforeReset"`
	EstimatedWastedAmount          float64  `json:"estimatedWastedAmount"`
	EstimatedWastedPercent         float64  `json:"estimatedWastedPercent"`
	OpportunityScore               float64  `json:"opportunityScore"`
	Urgency                        string   `json:"urgency"`
	Confidence                     string   `json:"confidence"`
	Reasons                        []string `json:"reasons"`
	Caveats                        []string `json:"caveats"`
}

func ExpiringUsage(usage model.Usage, opts ExpiringUsageOptions) ExpiringUsageResponse {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	out := ExpiringUsageResponse{GeneratedAt: now.UnixMilli(), Opportunities: []ExpiringUsageOpportunity{}}
	for _, item := range usage.QuotaItems {
		op, ok := expiringOpportunity(item, opts, now)
		if ok {
			out.Opportunities = append(out.Opportunities, op)
		}
	}
	sort.SliceStable(out.Opportunities, func(i, j int) bool {
		left, right := out.Opportunities[i], out.Opportunities[j]
		if left.OpportunityScore == right.OpportunityScore {
			if left.ResetAt == right.ResetAt {
				return left.ItemID < right.ItemID
			}
			return left.ResetAt < right.ResetAt
		}
		return left.OpportunityScore > right.OpportunityScore
	})
	return out
}

func expiringOpportunity(item model.QuotaItem, opts ExpiringUsageOptions, now time.Time) (ExpiringUsageOpportunity, bool) {
	if !item.Visible || item.Remaining <= 0 || isResetCreditBankItem(item) {
		return ExpiringUsageOpportunity{}, false
	}
	if len(opts.Providers) > 0 && !opts.Providers[item.Provider] {
		return ExpiringUsageOpportunity{}, false
	}
	percentRemaining := percentRemaining(item)
	if percentRemaining <= 0 || percentRemaining < opts.MinimumRemainingPercent {
		return ExpiringUsageOpportunity{}, false
	}
	resetAt, ok := resetAtMs(item)
	if !ok {
		return ExpiringUsageOpportunity{}, false
	}
	timeRemaining := time.Duration(resetAt-now.UnixMilli()) * time.Millisecond
	if timeRemaining <= 0 || (opts.Within > 0 && timeRemaining > opts.Within) {
		return ExpiringUsageOpportunity{}, false
	}

	confidence := ConfidenceLow
	caveats := []string{}
	natural := 0.0
	rateSource := ""
	if rate, ok := opts.BurnRates[BurnRateKey(item.Provider, item.ID)]; ok && rate.BurnPerMs >= 0 {
		natural = rate.BurnPerMs * float64(timeRemaining.Milliseconds())
		confidence = normalizedConfidence(rate.Confidence, ConfidenceHigh)
		rateSource = rate.Source
		if rateSource == "" {
			rateSource = "local history burn rate"
		}
		caveats = append(caveats, fmt.Sprintf("natural use estimated from %s (%d samples)", rateSource, rate.Samples))
	} else if duration, inferred := inferWindowDuration(item); inferred {
		elapsed := duration - timeRemaining
		if elapsed > 0 {
			burnPerMs := item.Used / float64(elapsed.Milliseconds())
			natural = burnPerMs * float64(timeRemaining.Milliseconds())
			confidence = ConfidenceMedium
			caveats = append(caveats, "burn rate inferred from window elapsed ratio, not historical samples")
		} else {
			caveats = append(caveats, "window duration was inferred but the elapsed window could not be derived")
		}
	} else {
		caveats = append(caveats, "no window duration or history is available, so natural use before reset is low-confidence")
	}
	if item.State == "stale" {
		confidence = ConfidenceLow
		caveats = append(caveats, "cached quota item is stale")
	}
	if !opts.IncludeLowConfidence && confidence == ConfidenceLow {
		return ExpiringUsageOpportunity{}, false
	}

	natural = clamp(natural, 0, item.Remaining+item.Used)
	wasted := math.Max(0, item.Remaining-natural)
	if wasted <= 0 {
		return ExpiringUsageOpportunity{}, false
	}
	wastedPercent := estimatedWastedPercent(item, wasted)
	urgency := urgencyFor(timeRemaining)
	score := opportunityScore(percentRemaining, item.Remaining, wasted, urgency, confidence)
	if score <= 0 {
		return ExpiringUsageOpportunity{}, false
	}

	unit := item.Unit
	if unit == "" {
		unit = "units"
	}
	reasons := []string{
		fmt.Sprintf("%s remains with about %s until reset", formatAmount(item.Remaining, unit), formatDurationApprox(timeRemaining)),
	}
	if rateSource != "" {
		reasons = append(reasons, "recent local history burn rate is unlikely to consume the remainder")
	} else if confidence == ConfidenceMedium {
		reasons = append(reasons, "current inferred burn rate is unlikely to consume the remainder")
	} else {
		reasons = append(reasons, "remaining quota may expire unused, but burn rate confidence is low")
	}

	return ExpiringUsageOpportunity{
		Provider:                       item.Provider,
		ItemID:                         item.ID,
		Label:                          item.Label,
		Unit:                           unit,
		Remaining:                      round3(item.Remaining),
		PercentRemaining:               round3(percentRemaining),
		ResetAt:                        resetAt,
		TimeRemainingMs:                timeRemaining.Milliseconds(),
		EstimatedNaturalUseBeforeReset: round3(natural),
		EstimatedWastedAmount:          round3(wasted),
		EstimatedWastedPercent:         round3(wastedPercent),
		OpportunityScore:               round3(score),
		Urgency:                        urgency,
		Confidence:                     confidence,
		Reasons:                        reasons,
		Caveats:                        caveats,
	}, true
}

func resetAtMs(item model.QuotaItem) (int64, bool) {
	if item.Reset != nil && item.Reset.ResetAt > 0 {
		return item.Reset.ResetAt, true
	}
	if item.Window.ResetAt != nil && *item.Window.ResetAt > 0 {
		return *item.Window.ResetAt, true
	}
	return 0, false
}

func percentRemaining(item model.QuotaItem) float64 {
	if item.Limit > 0 {
		return clamp(item.Remaining/item.Limit*100, 0, 100)
	}
	if item.Unit == "percent" || item.Unit == "%" {
		return clamp(item.Remaining, 0, 100)
	}
	if item.PercentUsed > 0 || item.Used > 0 {
		return clamp(100-item.PercentUsed, 0, 100)
	}
	if total := item.Used + item.Remaining; total > 0 {
		return clamp(item.Remaining/total*100, 0, 100)
	}
	return 0
}

func estimatedWastedPercent(item model.QuotaItem, wasted float64) float64 {
	if item.Limit > 0 {
		return clamp(wasted/item.Limit*100, 0, 100)
	}
	if item.Unit == "percent" || item.Unit == "%" {
		return clamp(wasted, 0, 100)
	}
	if total := item.Used + item.Remaining; total > 0 {
		return clamp(wasted/total*100, 0, 100)
	}
	return 0
}

func inferWindowDuration(item model.QuotaItem) (time.Duration, bool) {
	text := strings.ToLower(strings.Join([]string{item.Window.Kind, item.Window.ID, item.Window.Label, item.ID, item.Label}, " "))
	switch {
	case strings.Contains(text, "session"), strings.Contains(text, "rolling"), strings.Contains(text, "5h"):
		return 5 * time.Hour, true
	case strings.Contains(text, "weekly"), strings.Contains(text, "week"):
		return 7 * 24 * time.Hour, true
	case strings.Contains(text, "daily"), strings.Contains(text, "day"):
		return 24 * time.Hour, true
	case strings.Contains(text, "monthly"), strings.Contains(text, "month"):
		return 30 * 24 * time.Hour, true
	default:
		return 0, false
	}
}

func normalizedConfidence(value, fallback string) string {
	switch value {
	case ConfidenceHigh, ConfidenceMedium, ConfidenceLow:
		return value
	default:
		return fallback
	}
}

func isResetCreditBankItem(item model.QuotaItem) bool {
	id := strings.ToLower(item.ID)
	label := strings.ToLower(item.Label)
	return id == "chatgpt-rate-limit-reset-credits" || strings.Contains(id, "reset-credit") || strings.Contains(id, "reset_credits") || strings.Contains(label, "reset credit")
}

func urgencyFor(d time.Duration) string {
	switch {
	case d <= time.Hour:
		return "extreme"
	case d <= 6*time.Hour:
		return "high"
	case d <= 24*time.Hour:
		return "normal"
	default:
		return "low"
	}
}

func opportunityScore(percentRemaining, remaining, wasted float64, urgency, confidence string) float64 {
	remainingScore := clamp(percentRemaining/100, 0, 1)
	underuseScore := 0.0
	if remaining > 0 {
		underuseScore = clamp(wasted/remaining, 0, 1)
	}
	urgencyScore := map[string]float64{"extreme": 1, "high": 0.85, "normal": 0.65, "low": 0.35}[urgency]
	confidenceMultiplier := map[string]float64{ConfidenceHigh: 1, ConfidenceMedium: 0.75, ConfidenceLow: 0.45}[confidence]
	return remainingScore * urgencyScore * underuseScore * confidenceMultiplier
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func formatAmount(v float64, unit string) string {
	if unit == "percent" || unit == "%" {
		return fmt.Sprintf("%g%%", round3(v))
	}
	return fmt.Sprintf("%g %s", round3(v), unit)
}

func formatDurationApprox(d time.Duration) string {
	if d < time.Minute {
		return "less than 1m"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(math.Round(d.Minutes())))
	}
	if d < 48*time.Hour {
		return fmt.Sprintf("%dh", int(math.Round(d.Hours())))
	}
	return fmt.Sprintf("%dd", int(math.Round(d.Hours()/24)))
}
