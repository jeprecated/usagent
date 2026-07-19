package analysis

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jeprecated/usagent/internal/model"
)

type ResetCalendarResponse struct {
	GeneratedAt int64       `json:"generatedAt"`
	Resets      []ResetItem `json:"resets"`
}

type ResetItem struct {
	Provider         string       `json:"provider"`
	ItemID           string       `json:"itemId"`
	Label            string       `json:"label"`
	Window           model.Window `json:"window"`
	Unit             string       `json:"unit"`
	Remaining        float64      `json:"remaining"`
	PercentRemaining float64      `json:"percentRemaining"`
	PercentUsed      float64      `json:"percentUsed"`
	State            string       `json:"state"`
	ResetAt          int64        `json:"resetAt"`
	ResetSource      string       `json:"resetSource"`
}

type AvailabilityResponse struct {
	GeneratedAt int64                  `json:"generatedAt"`
	Providers   []ProviderAvailability `json:"providers"`
}

type ProviderAvailability struct {
	Provider          string                   `json:"provider"`
	Label             string                   `json:"label"`
	State             model.ProviderState      `json:"state"`
	Available         bool                     `json:"available"`
	Severity          string                   `json:"severity"`
	Blockers          []string                 `json:"blockers,omitempty"`
	ConstrainedBy     []AvailabilityConstraint `json:"constrainedBy,omitempty"`
	NextImprovementAt *int64                   `json:"nextImprovementAt,omitempty"`
	Summary           string                   `json:"summary"`
}

type AvailabilityConstraint struct {
	ItemID           string  `json:"itemId"`
	Label            string  `json:"label"`
	Unit             string  `json:"unit"`
	Remaining        float64 `json:"remaining"`
	PercentRemaining float64 `json:"percentRemaining"`
	ResetAt          *int64  `json:"resetAt,omitempty"`
	Reason           string  `json:"reason"`
}

func ResetCalendar(usage model.Usage, opts UsageAnalysisOptions) ResetCalendarResponse {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	out := ResetCalendarResponse{GeneratedAt: now.UnixMilli(), Resets: []ResetItem{}}
	for _, item := range usage.QuotaItems {
		if !item.Visible {
			continue
		}
		resetAt, source, ok := resetInfo(item)
		if !ok || resetAt <= 0 {
			continue
		}
		out.Resets = append(out.Resets, ResetItem{
			Provider:         item.Provider,
			ItemID:           item.ID,
			Label:            item.Label,
			Window:           item.Window,
			Unit:             unitOrDefault(item.Unit),
			Remaining:        round3(item.Remaining),
			PercentRemaining: round3(percentRemaining(item)),
			PercentUsed:      round3(item.PercentUsed),
			State:            item.State,
			ResetAt:          resetAt,
			ResetSource:      source,
		})
	}
	sort.SliceStable(out.Resets, func(i, j int) bool {
		if out.Resets[i].ResetAt == out.Resets[j].ResetAt {
			if out.Resets[i].Provider == out.Resets[j].Provider {
				return out.Resets[i].ItemID < out.Resets[j].ItemID
			}
			return out.Resets[i].Provider < out.Resets[j].Provider
		}
		return out.Resets[i].ResetAt < out.Resets[j].ResetAt
	})
	return out
}

func ProviderAvailabilitySummary(usage model.Usage, opts UsageAnalysisOptions) AvailabilityResponse {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	analysis := AnalyzeUsage(usage, UsageAnalysisOptions{Now: now})
	itemsByProvider := map[string][]UsageAnalysisItem{}
	for _, item := range analysis.Items {
		itemsByProvider[item.Provider] = append(itemsByProvider[item.Provider], item)
	}
	out := AvailabilityResponse{GeneratedAt: now.UnixMilli(), Providers: make([]ProviderAvailability, 0, len(usage.Providers))}
	for _, provider := range usage.Providers {
		out.Providers = append(out.Providers, availabilityForProvider(provider, itemsByProvider[provider.ID]))
	}
	return out
}

func availabilityForProvider(provider model.Provider, items []UsageAnalysisItem) ProviderAvailability {
	state := provider.State
	severity := "ok"
	available := true
	blockers := []string{}
	constraints := []AvailabilityConstraint{}
	var nextImprovement *int64

	if len(items) == 0 {
		available = false
		severity = "unknown"
		blockers = append(blockers, "no cached quota data is available")
	}
	if state == model.ProviderStateStale {
		available = false
		if severity == "ok" {
			severity = "warning"
		}
		blockers = append(blockers, "cached provider data is stale")
	}
	if state == model.ProviderStateError {
		available = false
		severity = "error"
		if provider.Error != nil && strings.TrimSpace(provider.Error.Message) != "" {
			blockers = append(blockers, "provider refresh error: "+provider.Error.Message)
		} else {
			blockers = append(blockers, "provider has refresh error metadata")
		}
	}

	visible := 0
	usable := 0
	for _, item := range items {
		if !item.Visible {
			continue
		}
		visible++
		if item.Remaining > 0 && item.State != "error" && item.Severity != "error" {
			usable++
		}
		if item.PercentRemaining <= 10 || item.Remaining <= 0 || item.Severity == "warning" || item.Severity == "error" || item.State == "stale" || item.State == "error" {
			reason := constraintReason(item)
			constraints = append(constraints, AvailabilityConstraint{ItemID: item.ItemID, Label: item.Label, Unit: item.Unit, Remaining: item.Remaining, PercentRemaining: item.PercentRemaining, ResetAt: item.ResetAt, Reason: reason})
			if item.ResetAt != nil && (nextImprovement == nil || *item.ResetAt < *nextImprovement) {
				v := *item.ResetAt
				nextImprovement = &v
			}
		}
	}
	if visible == 0 && len(items) > 0 {
		available = false
		if severity == "ok" {
			severity = "unknown"
		}
		blockers = append(blockers, "no visible quota items are available")
	}
	if usable == 0 && visible > 0 {
		available = false
		if severity == "ok" {
			severity = "critical"
		}
		blockers = append(blockers, "all visible quota items are exhausted or in error")
	}
	if len(constraints) > 0 && severity == "ok" {
		severity = "warning"
	}
	return ProviderAvailability{Provider: provider.ID, Label: provider.Label, State: state, Available: available, Severity: severity, Blockers: blockers, ConstrainedBy: constraints, NextImprovementAt: nextImprovement, Summary: availabilitySummary(available, severity, len(items), len(constraints))}
}

func constraintReason(item UsageAnalysisItem) string {
	switch {
	case item.Remaining <= 0:
		return "quota item is exhausted"
	case item.Severity == "error" || item.State == "error":
		return "quota item has error state"
	case item.State == "stale":
		return "quota item is stale"
	case item.Severity == "warning":
		return "quota item has warning severity"
	case item.PercentRemaining <= 10:
		return fmt.Sprintf("quota item has low headroom (%g%% remaining)", round3(item.PercentRemaining))
	default:
		return "quota item may constrain availability"
	}
}

func availabilitySummary(available bool, severity string, itemCount, constraintCount int) string {
	if !available {
		return fmt.Sprintf("unavailable (%s): %d cached quota items, %d constraints", severity, itemCount, constraintCount)
	}
	if constraintCount > 0 {
		return fmt.Sprintf("available with constraints: %d cached quota items, %d constraints", itemCount, constraintCount)
	}
	return fmt.Sprintf("available: %d cached quota items and no constraints", itemCount)
}
