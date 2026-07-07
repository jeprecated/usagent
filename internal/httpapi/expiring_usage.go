package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jmalloc/usagent/internal/analysis"
)

func (api API) expiringUsage(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	opts, err := parseExpiringUsageOptions(r, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	usage := api.App.Usage(now)
	opts.BurnRates = api.App.BurnRateEstimates(usage.QuotaItems, now)
	writeJSON(w, http.StatusOK, analysis.ExpiringUsage(usage, opts))
}

func parseExpiringUsageOptions(r *http.Request, now time.Time) (analysis.ExpiringUsageOptions, error) {
	q := r.URL.Query()
	opts := analysis.ExpiringUsageOptions{Now: now}
	if raw := strings.TrimSpace(q.Get("within")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < 0 {
			return opts, fmt.Errorf("within must be a valid non-negative duration")
		}
		opts.Within = d
	}
	if raw := strings.TrimSpace(q.Get("withinMs")); raw != "" {
		ms, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || ms < 0 {
			return opts, fmt.Errorf("withinMs must be a non-negative integer")
		}
		d := time.Duration(ms) * time.Millisecond
		if opts.Within == 0 || d < opts.Within {
			opts.Within = d
		}
	}
	if raw := strings.TrimSpace(q.Get("minimumRemainingPercent")); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v < 0 || v > 100 {
			return opts, fmt.Errorf("minimumRemainingPercent must be a number from 0 to 100")
		}
		opts.MinimumRemainingPercent = v
	}
	if raw := strings.TrimSpace(q.Get("providers")); raw != "" {
		opts.Providers = map[string]bool{}
		for _, part := range strings.Split(raw, ",") {
			provider := strings.TrimSpace(part)
			if provider != "" {
				opts.Providers[provider] = true
			}
		}
	}
	if raw := strings.TrimSpace(q.Get("includeLowConfidence")); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return opts, fmt.Errorf("includeLowConfidence must be true or false")
		}
		opts.IncludeLowConfidence = v
	}
	return opts, nil
}
