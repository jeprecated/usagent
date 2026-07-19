package analysis

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jeprecated/usagent/internal/model"
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
	Tiers                   map[string]bool
	Tags                    map[string]bool
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
	Provider                       string                       `json:"provider"`
	ItemID                         string                       `json:"itemId"`
	Label                          string                       `json:"label"`
	Unit                           string                       `json:"unit"`
	Remaining                      float64                      `json:"remaining"`
	PercentRemaining               float64                      `json:"percentRemaining"`
	ProviderTier                   string                       `json:"providerTier,omitempty"`
	ProviderTags                   []string                     `json:"providerTags,omitempty"`
	ModelTier                      string                       `json:"modelTier,omitempty"`
	ModelTags                      []string                     `json:"modelTags,omitempty"`
	ResetAt                        int64                        `json:"resetAt"`
	TimeRemainingMs                int64                        `json:"timeRemainingMs"`
	EstimatedNaturalUseBeforeReset float64                      `json:"estimatedNaturalUseBeforeReset"`
	EstimatedWastedAmount          float64                      `json:"estimatedWastedAmount"`
	EstimatedWastedPercent         float64                      `json:"estimatedWastedPercent"`
	RawEstimatedWastedAmount       float64                      `json:"rawEstimatedWastedAmount"`
	RawEstimatedWastedPercent      float64                      `json:"rawEstimatedWastedPercent"`
	ActionableWasteAmount          float64                      `json:"actionableWasteAmount"`
	ActionableWastePercent         float64                      `json:"actionableWastePercent"`
	OpportunityScore               float64                      `json:"opportunityScore"`
	Urgency                        string                       `json:"urgency"`
	Confidence                     string                       `json:"confidence"`
	Reasons                        []string                     `json:"reasons"`
	Caveats                        []string                     `json:"caveats"`
	OverlapContext                 *ExpiringUsageOverlapContext `json:"overlapContext,omitempty"`
}

type ExpiringUsageOverlapContext struct {
	GroupKey                              string  `json:"groupKey"`
	Relationship                          string  `json:"relationship"`
	Confidence                            string  `json:"confidence"`
	ParentItemID                          string  `json:"parentItemId"`
	ParentLabel                           string  `json:"parentLabel"`
	ParentWindowKind                      string  `json:"parentWindowKind"`
	ParentResetAt                         int64   `json:"parentResetAt"`
	ParentTimeRemainingMs                 int64   `json:"parentTimeRemainingMs"`
	ParentRemaining                       float64 `json:"parentRemaining"`
	ParentPercentRemaining                float64 `json:"parentPercentRemaining"`
	ParentEstimatedNaturalUseBeforeReset  float64 `json:"parentEstimatedNaturalUseBeforeReset"`
	FutureShortWindowsBeforeParentReset   int     `json:"futureShortWindowsBeforeParentReset"`
	FutureShortWindowCapacityBeforeParent float64 `json:"futureShortWindowCapacityBeforeParent"`
	ParentCapacityLimitedActionableWaste  bool    `json:"parentCapacityLimitedActionableWaste"`
	FreshParentFutureCapacityDownweighted bool    `json:"freshParentFutureCapacityDownweighted"`
	NearParentResetOrExhaustionUpweighted bool    `json:"nearParentResetOrExhaustionUpweighted"`
}

type expiringCandidate struct {
	item model.QuotaItem
	op   ExpiringUsageOpportunity
	info quotaWindowInfo
}

type quotaWindowInfo struct {
	item              model.QuotaItem
	resetAt           int64
	timeRemaining     time.Duration
	duration          time.Duration
	startedAt         int64
	elapsed           time.Duration
	familyKey         string
	familyConfidence  string
	familyHeuristic   bool
	hasOverlapFamily  bool
	parentDemand      float64
	percentRemaining  float64
	windowDescription string
}

func ExpiringUsage(usage model.Usage, opts ExpiringUsageOptions) ExpiringUsageResponse {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	out := ExpiringUsageResponse{GeneratedAt: now.UnixMilli(), Opportunities: []ExpiringUsageOpportunity{}}
	infos := quotaWindowInfos(usage.QuotaItems, now)
	for _, item := range usage.QuotaItems {
		candidate, ok := expiringOpportunity(item, opts, now)
		if !ok {
			continue
		}
		applyOverlapAdjustments(&candidate, infos)
		if candidate.op.ActionableWasteAmount <= 0 || candidate.op.OpportunityScore <= 0 {
			continue
		}
		out.Opportunities = append(out.Opportunities, candidate.op)
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

func expiringOpportunity(item model.QuotaItem, opts ExpiringUsageOptions, now time.Time) (expiringCandidate, bool) {
	if !item.Visible || item.Remaining <= 0 || isResetCreditBankItem(item) {
		return expiringCandidate{}, false
	}
	if len(opts.Providers) > 0 && !opts.Providers[item.Provider] {
		return expiringCandidate{}, false
	}
	if len(opts.Tiers) > 0 && !matchesTier(item, opts.Tiers) {
		return expiringCandidate{}, false
	}
	if len(opts.Tags) > 0 && !matchesTags(item, opts.Tags) {
		return expiringCandidate{}, false
	}
	percentRemaining := percentRemaining(item)
	if percentRemaining <= 0 || percentRemaining < opts.MinimumRemainingPercent {
		return expiringCandidate{}, false
	}
	info, ok := quotaWindowInfoForItem(item, now)
	if !ok {
		return expiringCandidate{}, false
	}
	if info.timeRemaining <= 0 || (opts.Within > 0 && info.timeRemaining > opts.Within) {
		return expiringCandidate{}, false
	}

	confidence := ConfidenceLow
	caveats := []string{}
	natural := 0.0
	rateSource := ""
	if rate, ok := opts.BurnRates[BurnRateKey(item.Provider, item.ID)]; ok && rate.BurnPerMs >= 0 {
		natural = rate.BurnPerMs * float64(info.timeRemaining.Milliseconds())
		confidence = normalizedConfidence(rate.Confidence, ConfidenceHigh)
		rateSource = rate.Source
		if rateSource == "" {
			rateSource = "local history burn rate"
		}
		caveats = append(caveats, fmt.Sprintf("natural use estimated from %s (%d samples)", rateSource, rate.Samples))
	} else if info.duration > 0 {
		if info.elapsed > 0 {
			natural = estimateNaturalUse(item, info.duration, info.timeRemaining)
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
		return expiringCandidate{}, false
	}

	natural = clamp(natural, 0, item.Remaining+item.Used)
	wasted := math.Max(0, item.Remaining-natural)
	if wasted <= 0 {
		return expiringCandidate{}, false
	}
	wastedPercent := estimatedWastedPercent(item, wasted)
	urgency := urgencyFor(info.timeRemaining)
	score := opportunityScore(percentRemaining, item.Remaining, wasted, urgency, confidence)
	if score <= 0 {
		return expiringCandidate{}, false
	}

	unit := item.Unit
	if unit == "" {
		unit = "units"
	}
	reasons := []string{
		fmt.Sprintf("%s remains with about %s until reset", formatAmount(item.Remaining, unit), formatDurationApprox(info.timeRemaining)),
	}
	if rateSource != "" {
		reasons = append(reasons, "recent local history burn rate is unlikely to consume the remainder")
	} else if confidence == ConfidenceMedium {
		reasons = append(reasons, "current inferred burn rate is unlikely to consume the remainder")
	} else {
		reasons = append(reasons, "remaining quota may expire unused, but burn rate confidence is low")
	}

	op := ExpiringUsageOpportunity{
		Provider:                       item.Provider,
		ItemID:                         item.ID,
		Label:                          item.Label,
		Unit:                           unit,
		Remaining:                      round3(item.Remaining),
		PercentRemaining:               round3(percentRemaining),
		ProviderTier:                   item.ProviderTier,
		ProviderTags:                   cloneStrings(item.ProviderTags),
		ModelTier:                      item.ModelTier,
		ModelTags:                      cloneStrings(item.ModelTags),
		ResetAt:                        info.resetAt,
		TimeRemainingMs:                info.timeRemaining.Milliseconds(),
		EstimatedNaturalUseBeforeReset: round3(natural),
		EstimatedWastedAmount:          round3(wasted),
		EstimatedWastedPercent:         round3(wastedPercent),
		RawEstimatedWastedAmount:       round3(wasted),
		RawEstimatedWastedPercent:      round3(wastedPercent),
		ActionableWasteAmount:          round3(wasted),
		ActionableWastePercent:         round3(wastedPercent),
		OpportunityScore:               round3(score),
		Urgency:                        urgency,
		Confidence:                     confidence,
		Reasons:                        reasons,
		Caveats:                        caveats,
	}
	return expiringCandidate{item: item, op: op, info: info}, true
}

func quotaWindowInfos(items []model.QuotaItem, now time.Time) []quotaWindowInfo {
	infos := make([]quotaWindowInfo, 0, len(items))
	for _, item := range items {
		if !item.Visible || item.Remaining < 0 || isResetCreditBankItem(item) {
			continue
		}
		info, ok := quotaWindowInfoForItem(item, now)
		if ok && info.timeRemaining > 0 && info.duration > 0 {
			infos = append(infos, info)
		}
	}
	return infos
}

func quotaWindowInfoForItem(item model.QuotaItem, now time.Time) (quotaWindowInfo, bool) {
	resetAt, ok := resetAtMs(item)
	if !ok {
		return quotaWindowInfo{}, false
	}
	duration, inferred := inferWindowDuration(item)
	if !inferred {
		duration = 0
	}
	timeRemaining := time.Duration(resetAt-now.UnixMilli()) * time.Millisecond
	startedAt := int64(0)
	elapsed := time.Duration(0)
	if duration > 0 {
		startedAt = resetAt - duration.Milliseconds()
		elapsed = time.Duration(now.UnixMilli()-startedAt) * time.Millisecond
	}
	familyKey, familyConfidence, heuristic, hasFamily := overlapFamily(item)
	return quotaWindowInfo{
		item:              item,
		resetAt:           resetAt,
		timeRemaining:     timeRemaining,
		duration:          duration,
		startedAt:         startedAt,
		elapsed:           elapsed,
		familyKey:         familyKey,
		familyConfidence:  familyConfidence,
		familyHeuristic:   heuristic,
		hasOverlapFamily:  hasFamily,
		parentDemand:      estimateNaturalUse(item, duration, timeRemaining),
		percentRemaining:  percentRemaining(item),
		windowDescription: windowDescription(item),
	}, true
}

func applyOverlapAdjustments(candidate *expiringCandidate, infos []quotaWindowInfo) {
	child := candidate.info
	parent, found := bestParentWindow(child, infos)
	if !found {
		if isShortWindow(child.item, child.duration) {
			candidate.op.Caveats = append(candidate.op.Caveats, "no overlapping longer-window quota item with the same provider/model family is available in cached usage; actionable waste equals the raw estimate")
		}
		return
	}

	rawWaste := candidate.op.RawEstimatedWastedAmount
	actionable := rawWaste
	parentDemand := clamp(parent.parentDemand, 0, parent.item.Remaining+parent.item.Used)
	parentRemaining := math.Max(0, parent.item.Remaining)
	parentPercentRemaining := percentRemaining(parent.item)
	futureWindows := futureShortWindows(child, parent)
	futureCapacity := futureShortCapacity(child, futureWindows)
	parentRemainingRatio := 0.0
	if parent.duration > 0 {
		parentRemainingRatio = clamp(float64(parent.timeRemaining)/float64(parent.duration), 0, 1)
	}
	parentNearReset := parent.timeRemaining <= child.duration || parentRemainingRatio <= 0.15
	parentConstrained := parentPercentRemaining > 0 && parentPercentRemaining <= 25

	ctx := &ExpiringUsageOverlapContext{
		GroupKey:                              child.familyKey,
		Relationship:                          "inferred-overlap",
		Confidence:                            minConfidence(child.familyConfidence, parent.familyConfidence),
		ParentItemID:                          parent.item.ID,
		ParentLabel:                           parent.item.Label,
		ParentWindowKind:                      parent.item.Window.Kind,
		ParentResetAt:                         parent.resetAt,
		ParentTimeRemainingMs:                 parent.timeRemaining.Milliseconds(),
		ParentRemaining:                       round3(parentRemaining),
		ParentPercentRemaining:                round3(parentPercentRemaining),
		ParentEstimatedNaturalUseBeforeReset:  round3(parentDemand),
		FutureShortWindowsBeforeParentReset:   futureWindows,
		FutureShortWindowCapacityBeforeParent: round3(futureCapacity),
	}

	candidate.op.Caveats = append(candidate.op.Caveats, overlapCaveats(child, parent)...)
	candidate.op.Reasons = append(candidate.op.Reasons, fmt.Sprintf("overlaps longer %s quota %s with %s remaining", parent.windowDescription, displayName(parent.item), formatAmount(parentRemaining, unitOrDefault(parent.item.Unit))))

	if parentRemaining < actionable {
		actionable = parentRemaining
		ctx.ParentCapacityLimitedActionableWaste = true
		candidate.op.Reasons = append(candidate.op.Reasons, "actionable waste is capped by the overlapping parent window's remaining capacity")
	}

	freshParent := parentRemainingRatio >= 0.50 && parentPercentRemaining >= 40 && !parentNearReset && !parentConstrained
	futureCanSatisfyDemand := futureWindows > 0 && futureCapacity >= math.Max(parentDemand, child.item.Remaining)
	if freshParent && futureCanSatisfyDemand {
		actionable *= 0.25
		ctx.FreshParentFutureCapacityDownweighted = true
		candidate.op.Reasons = append(candidate.op.Reasons, "fresh parent window and future short-window resets can likely satisfy forecast demand, so raw expiring unused is down-weighted for actionable waste")
	}

	if parentNearReset || parentConstrained {
		ctx.NearParentResetOrExhaustionUpweighted = true
		candidate.op.Reasons = append(candidate.op.Reasons, "parent window is near reset or capacity-constrained, so current short-window capacity is more likely to matter")
		candidate.op.Urgency = moreUrgent(candidate.op.Urgency, urgencyFor(parent.timeRemaining))
	}

	actionable = clamp(actionable, 0, rawWaste)
	candidate.op.ActionableWasteAmount = round3(actionable)
	candidate.op.ActionableWastePercent = round3(estimatedWastedPercent(child.item, actionable))
	// Preserve estimatedWasted* as the raw snapshot-only expiring amount for
	// compatibility; rank by the parent-adjusted actionable amount.
	candidate.op.EstimatedWastedAmount = candidate.op.RawEstimatedWastedAmount
	candidate.op.EstimatedWastedPercent = candidate.op.RawEstimatedWastedPercent
	candidate.op.OpportunityScore = round3(opportunityScore(candidate.op.PercentRemaining, child.item.Remaining, actionable, candidate.op.Urgency, candidate.op.Confidence))
	candidate.op.OverlapContext = ctx
}

func bestParentWindow(child quotaWindowInfo, infos []quotaWindowInfo) (quotaWindowInfo, bool) {
	if !child.hasOverlapFamily || child.duration <= 0 {
		return quotaWindowInfo{}, false
	}
	parents := []quotaWindowInfo{}
	for _, candidate := range infos {
		if candidate.item.ID == child.item.ID || candidate.item.Provider != child.item.Provider || !candidate.hasOverlapFamily {
			continue
		}
		if candidate.familyKey != child.familyKey || !compatibleUnits(child.item.Unit, candidate.item.Unit) {
			continue
		}
		if candidate.duration <= child.duration || !windowsOverlap(child, candidate) {
			continue
		}
		parents = append(parents, candidate)
	}
	if len(parents) == 0 {
		return quotaWindowInfo{}, false
	}
	sort.SliceStable(parents, func(i, j int) bool {
		if parents[i].duration == parents[j].duration {
			return parents[i].resetAt < parents[j].resetAt
		}
		return parents[i].duration < parents[j].duration
	})
	return parents[0], true
}

func windowsOverlap(a, b quotaWindowInfo) bool {
	return a.startedAt < b.resetAt && b.startedAt < a.resetAt
}

func overlapFamily(item model.QuotaItem) (key, confidence string, heuristic bool, ok bool) {
	provider := strings.ToLower(strings.TrimSpace(item.Provider))
	id := strings.ToLower(strings.TrimSpace(item.ID))
	windowID := strings.ToLower(strings.TrimSpace(item.Window.ID))
	windowKind := strings.ToLower(strings.TrimSpace(item.Window.Kind))
	if provider == "" || id == "" {
		return "", "", false, false
	}

	switch provider {
	case "chatgpt":
		if id == "chatgpt-primary" || id == "chatgpt-secondary" {
			return provider + ":account", ConfidenceMedium, false, true
		}
		if base, matched := stripAnySuffix(id, "-primary", "-secondary"); matched {
			return provider + ":" + base, ConfidenceMedium, true, true
		}
	case "claude-code":
		if id == "claude-code-oauth-session" || id == "claude-code-oauth-weekly-all" {
			return provider + ":account", ConfidenceMedium, false, true
		}
		if base, matched := stripAnySuffix(id, "-session", "-weekly-all", "-weekly", "-monthly"); matched {
			return provider + ":" + base, ConfidenceLow, true, true
		}
	case "z-ai":
		if strings.HasPrefix(id, "z-ai-tokens-limit-") && (windowID == "session" || windowID == "weekly" || windowKind == "rolling" || windowKind == "weekly") {
			return provider + ":tokens-limit", ConfidenceMedium, true, true
		}
	}

	if base, matched := stripAnySuffix(id, "-primary", "-secondary", "-session", "-rolling", "-5h", "-weekly", "-week", "-monthly", "-month", "-daily", "-day"); matched && base != "" {
		return provider + ":" + base, ConfidenceLow, true, true
	}
	return "", "", false, false
}

func stripAnySuffix(s string, suffixes ...string) (string, bool) {
	for _, suffix := range suffixes {
		if strings.HasSuffix(s, suffix) && len(s) > len(suffix) {
			return strings.TrimSuffix(s, suffix), true
		}
	}
	return s, false
}

func compatibleUnits(a, b string) bool {
	return strings.EqualFold(unitOrDefault(a), unitOrDefault(b))
}

func futureShortWindows(child, parent quotaWindowInfo) int {
	if child.duration <= 0 || parent.resetAt <= child.resetAt {
		return 0
	}
	windows := int(time.Duration(parent.resetAt-child.resetAt) * time.Millisecond / child.duration)
	if windows < 0 {
		return 0
	}
	return windows
}

func futureShortCapacity(child quotaWindowInfo, windows int) float64 {
	if windows <= 0 {
		return 0
	}
	capacity := child.item.Limit
	if capacity <= 0 {
		capacity = child.item.Used + child.item.Remaining
	}
	if capacity <= 0 {
		capacity = child.item.Remaining
	}
	return float64(windows) * capacity
}

func estimateNaturalUse(item model.QuotaItem, duration, timeRemaining time.Duration) float64 {
	if duration <= 0 {
		return 0
	}
	elapsed := duration - timeRemaining
	if elapsed <= 0 {
		return 0
	}
	burnPerMs := item.Used / float64(elapsed.Milliseconds())
	return clamp(burnPerMs*float64(timeRemaining.Milliseconds()), 0, item.Remaining+item.Used)
}

func overlapCaveats(child, parent quotaWindowInfo) []string {
	caveats := []string{}
	if child.familyHeuristic || parent.familyHeuristic || child.familyConfidence == ConfidenceLow || parent.familyConfidence == ConfidenceLow {
		caveats = append(caveats, "overlapping quota relationship is inferred heuristically from provider, item IDs, labels, units, and reset windows")
	} else {
		caveats = append(caveats, "overlapping quota relationship is inferred from cached provider window metadata")
	}
	if child.item.Unit == "percent" || child.item.Unit == "%" || parent.item.Unit == "percent" || parent.item.Unit == "%" {
		caveats = append(caveats, "parent-window adjustment uses percent quota signals only and does not claim exact request or token capacity")
	}
	return caveats
}

func minConfidence(a, b string) string {
	order := map[string]int{ConfidenceLow: 0, ConfidenceMedium: 1, ConfidenceHigh: 2}
	if order[a] <= order[b] {
		return a
	}
	return b
}

func moreUrgent(a, b string) string {
	order := map[string]int{"low": 0, "normal": 1, "high": 2, "extreme": 3}
	if order[b] > order[a] {
		return b
	}
	return a
}

func isShortWindow(item model.QuotaItem, duration time.Duration) bool {
	text := strings.ToLower(strings.Join([]string{item.Window.Kind, item.Window.ID, item.Window.Label, item.ID, item.Label}, " "))
	return duration > 0 && duration <= 6*time.Hour || strings.Contains(text, "session") || strings.Contains(text, "5h")
}

func displayName(item model.QuotaItem) string {
	if strings.TrimSpace(item.Label) != "" {
		return item.Label
	}
	return item.ID
}

func unitOrDefault(unit string) string {
	if unit == "" {
		return "units"
	}
	return unit
}

func windowDescription(item model.QuotaItem) string {
	if strings.TrimSpace(item.Window.Kind) != "" {
		return item.Window.Kind
	}
	if strings.TrimSpace(item.Window.Label) != "" {
		return item.Window.Label
	}
	if strings.TrimSpace(item.Window.ID) != "" {
		return item.Window.ID
	}
	return "parent"
}

func matchesTier(item model.QuotaItem, tiers map[string]bool) bool {
	return tiers[strings.ToLower(item.ProviderTier)] || tiers[strings.ToLower(item.ModelTier)]
}

func matchesTags(item model.QuotaItem, tags map[string]bool) bool {
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

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
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
