package analysis

import (
	"math"
	"strings"
	"time"

	"github.com/jeprecated/usagent/internal/model"
)

// UsageAnalysisOptions controls pure derivation of helper fields from a cached
// model.Usage snapshot.
type UsageAnalysisOptions struct {
	Now time.Time
}

// UsageAnalysisResponse contains derived quota analysis. It is intentionally
// separate from model.Usage so /v1/usage stays schema-stable.
type UsageAnalysisResponse struct {
	SchemaVersion    int                 `json:"schemaVersion"`
	GeneratedAt      int64               `json:"generatedAt"`
	UsageGeneratedAt int64               `json:"usageGeneratedAt"`
	Items            []UsageAnalysisItem `json:"items"`
}

type UsageAnalysisItem struct {
	Provider         string  `json:"provider"`
	ItemID           string  `json:"itemId"`
	Label            string  `json:"label"`
	Unit             string  `json:"unit"`
	State            string  `json:"state"`
	Severity         string  `json:"severity"`
	Visible          bool    `json:"visible"`
	Limit            float64 `json:"limit"`
	Used             float64 `json:"used"`
	Remaining        float64 `json:"remaining"`
	PercentRemaining float64 `json:"percentRemaining"`

	ResetAt            *int64   `json:"resetAt,omitempty"`
	ResetSource        string   `json:"resetSource,omitempty"`
	TimeRemainingMs    *int64   `json:"timeRemainingMs,omitempty"`
	WindowDurationMs   *int64   `json:"windowDurationMs,omitempty"`
	WindowStartedAt    *int64   `json:"windowStartedAt,omitempty"`
	WindowElapsedMs    *int64   `json:"windowElapsedMs,omitempty"`
	ElapsedRatio       *float64 `json:"elapsedRatio,omitempty"`
	TimeRemainingRatio *float64 `json:"timeRemainingRatio,omitempty"`

	Pace                *UsagePace       `json:"pace,omitempty"`
	Pressure            *UsagePressure   `json:"pressure,omitempty"`
	ProjectedExhaustion *UsageProjection `json:"projectedExhaustion,omitempty"`
	Confidence          string           `json:"confidence"`
	Caveats             []string         `json:"caveats"`
}

type UsagePace struct {
	ObservedUsedPerHour      float64 `json:"observedUsedPerHour"`
	RequiredRemainingPerHour float64 `json:"requiredRemainingPerHour"`
}

type UsagePressure struct {
	Value float64 `json:"value"`
	Level string  `json:"level"`
}

type UsageProjection struct {
	ExhaustsAtMs int64 `json:"exhaustsAt"`
	InMs         int64 `json:"inMs"`
	BeforeReset  bool  `json:"beforeReset"`
}

// AnalyzeUsage derives analysis-only quota helper fields from the supplied
// snapshot. It has no side effects and never refreshes providers.
func AnalyzeUsage(usage model.Usage, opts UsageAnalysisOptions) UsageAnalysisResponse {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	out := UsageAnalysisResponse{
		SchemaVersion:    1,
		GeneratedAt:      now.UnixMilli(),
		UsageGeneratedAt: usage.GeneratedAt,
		Items:            make([]UsageAnalysisItem, 0, len(usage.QuotaItems)),
	}
	for _, item := range usage.QuotaItems {
		out.Items = append(out.Items, analyzeItem(item, now))
	}
	return out
}

func analyzeItem(item model.QuotaItem, now time.Time) UsageAnalysisItem {
	unit := item.Unit
	if unit == "" {
		unit = "units"
	}
	percent := round3(percentRemaining(item))
	caveats := []string{"pace and exhaustion are inferred from the current cached snapshot, not historical samples"}
	confidence := ConfidenceMedium
	if percent == 0 && item.Remaining > 0 {
		confidence = ConfidenceLow
		caveats = append(caveats, "percent remaining could not be derived reliably")
	}
	if item.State == "stale" {
		confidence = ConfidenceLow
		caveats = append(caveats, "cached quota item is stale")
	}
	if item.State == "error" || item.Severity == "error" || item.Error != nil {
		confidence = ConfidenceLow
		if item.Error != nil && strings.TrimSpace(item.Error.Message) != "" {
			caveats = append(caveats, "cached quota item has refresh error: "+item.Error.Message)
		} else {
			caveats = append(caveats, "cached quota item has refresh error metadata")
		}
	}

	out := UsageAnalysisItem{
		Provider:         item.Provider,
		ItemID:           item.ID,
		Label:            item.Label,
		Unit:             unit,
		State:            item.State,
		Severity:         item.Severity,
		Visible:          item.Visible,
		Limit:            round3(item.Limit),
		Used:             round3(item.Used),
		Remaining:        round3(item.Remaining),
		PercentRemaining: percent,
	}

	resetAt, resetSource, hasReset := resetInfo(item)
	var timeRemaining time.Duration
	if hasReset {
		out.ResetAt = int64Ptr(resetAt)
		out.ResetSource = resetSource
		timeRemaining = time.Duration(resetAt-now.UnixMilli()) * time.Millisecond
		out.TimeRemainingMs = int64Ptr(timeRemaining.Milliseconds())
		if timeRemaining <= 0 {
			confidence = ConfidenceLow
			caveats = append(caveats, "reset time is not in the future")
		}
	} else {
		confidence = ConfidenceLow
		caveats = append(caveats, "no reset time is available in the cached item")
	}

	duration, inferredDuration := inferAnalysisWindowDuration(item, hasReset)
	if inferredDuration {
		out.WindowDurationMs = int64Ptr(duration.Milliseconds())
		if hasReset {
			startedAt := resetAt - duration.Milliseconds()
			out.WindowStartedAt = int64Ptr(startedAt)
			elapsed := time.Duration(now.UnixMilli()-startedAt) * time.Millisecond
			out.WindowElapsedMs = int64Ptr(elapsed.Milliseconds())
			if elapsed <= 0 {
				confidence = ConfidenceLow
				caveats = append(caveats, "window start was inferred after the analysis time")
			} else {
				elapsedRatio := clamp(float64(elapsed)/float64(duration), 0, 1)
				remainingRatio := 0.0
				if timeRemaining > 0 {
					remainingRatio = clamp(float64(timeRemaining)/float64(duration), 0, 1)
				}
				out.ElapsedRatio = float64Ptr(round3(elapsedRatio))
				out.TimeRemainingRatio = float64Ptr(round3(remainingRatio))
				out.Pace, out.Pressure, out.ProjectedExhaustion = pacePressureProjection(item, elapsed, timeRemaining, now, resetAt)
			}
		}
	} else {
		confidence = ConfidenceLow
		caveats = append(caveats, "window duration could not be inferred, so pace and exhaustion are unavailable")
	}

	out.Confidence = confidence
	out.Caveats = caveats
	return out
}

func resetInfo(item model.QuotaItem) (int64, string, bool) {
	if item.Reset != nil && item.Reset.ResetAt > 0 {
		source := item.Reset.Source
		if source == "" {
			source = "provider"
		}
		return item.Reset.ResetAt, source, true
	}
	if item.Window.ResetAt != nil && *item.Window.ResetAt > 0 {
		return *item.Window.ResetAt, "window", true
	}
	return 0, "", false
}

func inferAnalysisWindowDuration(item model.QuotaItem, hasReset bool) (time.Duration, bool) {
	text := strings.ToLower(strings.Join([]string{item.Window.Kind, item.Window.ID, item.Window.Label, item.ID, item.Label}, " "))
	switch {
	case strings.Contains(text, "session"), strings.Contains(text, "rolling"), strings.Contains(text, "5h"):
		return 5 * time.Hour, true
	case strings.Contains(text, "weekly"), strings.Contains(text, "week"):
		return 7 * 24 * time.Hour, true
	case strings.Contains(text, "daily"), strings.Contains(text, "day"):
		return 24 * time.Hour, true
	case hasReset && (strings.Contains(text, "monthly") || strings.Contains(text, "month") || strings.Contains(text, "extra")):
		return 30 * 24 * time.Hour, true
	default:
		return 0, false
	}
}

func pacePressureProjection(item model.QuotaItem, elapsed, timeRemaining time.Duration, now time.Time, resetAt int64) (*UsagePace, *UsagePressure, *UsageProjection) {
	if elapsed <= 0 {
		return nil, nil, nil
	}
	observedPerHour := item.Used / elapsed.Hours()
	requiredPerHour := 0.0
	if timeRemaining > 0 {
		requiredPerHour = item.Remaining / timeRemaining.Hours()
	}
	pace := &UsagePace{ObservedUsedPerHour: round3(nonNegativeFinite(observedPerHour)), RequiredRemainingPerHour: round3(nonNegativeFinite(requiredPerHour))}

	var pressure *UsagePressure
	if observedPerHour > 0 && requiredPerHour >= 0 && !math.IsInf(observedPerHour, 0) && !math.IsNaN(observedPerHour) {
		value := round3(requiredPerHour / observedPerHour)
		pressure = &UsagePressure{Value: value, Level: pressureLevel(value, timeRemaining)}
	}

	var projection *UsageProjection
	if item.Remaining <= 0 {
		projection = &UsageProjection{ExhaustsAtMs: now.UnixMilli(), InMs: 0, BeforeReset: true}
	} else if observedPerHour > 0 && !math.IsInf(observedPerHour, 0) && !math.IsNaN(observedPerHour) {
		ms := int64((item.Remaining / observedPerHour) * float64(time.Hour/time.Millisecond))
		if ms < 0 {
			ms = 0
		}
		exhaustsAt := now.UnixMilli() + ms
		projection = &UsageProjection{ExhaustsAtMs: exhaustsAt, InMs: ms, BeforeReset: resetAt > 0 && exhaustsAt <= resetAt}
	}
	return pace, pressure, projection
}

func pressureLevel(value float64, timeRemaining time.Duration) string {
	if timeRemaining <= 0 {
		return "expired"
	}
	switch {
	case value <= 0.75:
		return "low"
	case value <= 1.25:
		return "steady"
	case value <= 2:
		return "elevated"
	default:
		return "high"
	}
}

func nonNegativeFinite(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	return v
}

func int64Ptr(v int64) *int64       { return &v }
func float64Ptr(v float64) *float64 { return &v }
