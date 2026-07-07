package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jmalloc/usagent/internal/analysis"
)

func (api API) providerRecommendations(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	opts, err := parseProviderRecommendationOptions(r, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	usage := api.App.Usage(now)
	opts.BurnRates = api.App.BurnRateEstimates(usage.QuotaItems, now)
	writeJSON(w, http.StatusOK, analysis.RecommendProvider(usage, opts))
}

func parseProviderRecommendationOptions(r *http.Request, now time.Time) (analysis.ProviderRecommendationOptions, error) {
	q := r.URL.Query()
	opts := analysis.ProviderRecommendationOptions{Now: now}
	if raw := strings.TrimSpace(q.Get("task")); raw != "" {
		task, err := parseRecommendationTaskProfile(raw)
		if err != nil {
			return opts, err
		}
		opts.TaskProfile = task
	}
	if raw := strings.TrimSpace(q.Get("profile")); raw != "" {
		task, err := parseRecommendationTaskProfile(raw)
		if err != nil {
			return opts, err
		}
		opts.TaskProfile = task
	}
	if raw := strings.TrimSpace(q.Get("taskProfile")); raw != "" {
		task, err := parseRecommendationTaskProfile(raw)
		if err != nil {
			return opts, err
		}
		opts.TaskProfile = task
	}
	if raw := strings.TrimSpace(q.Get("providers")); raw != "" {
		opts.Providers = parseCSVSet(raw, false)
	}
	if raw := firstNonEmpty(q.Get("tiers"), q.Get("tier")); raw != "" {
		opts.Tiers = parseCSVSet(raw, true)
	}
	if raw := firstNonEmpty(q.Get("tags"), q.Get("tag")); raw != "" {
		opts.Tags = parseCSVSet(raw, true)
	}
	if raw := strings.TrimSpace(q.Get("minimumRemainingPercent")); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v < 0 || v > 100 {
			return opts, fmt.Errorf("minimumRemainingPercent must be a number from 0 to 100")
		}
		opts.MinimumRemainingPercent = v
	}
	if raw := strings.TrimSpace(q.Get("unit")); raw != "" {
		opts.Unit = raw
	}
	return opts, nil
}

func parseRecommendationTaskProfile(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", analysis.TaskProfileGeneral:
		return analysis.TaskProfileGeneral, nil
	case analysis.TaskProfileCheap:
		return analysis.TaskProfileCheap, nil
	case analysis.TaskProfileLongRunning:
		return analysis.TaskProfileLongRunning, nil
	case analysis.TaskProfileLatencyInsensitive:
		return analysis.TaskProfileLatencyInsensitive, nil
	case analysis.TaskProfileUseItOrLoseIt, "use_it_or_lose_it", "use-it", "expiring":
		return analysis.TaskProfileUseItOrLoseIt, nil
	default:
		return "", fmt.Errorf("task must be one of general, cheap, long-running, latency-insensitive, use-it-or-lose-it")
	}
}
