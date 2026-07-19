package httpapi

import (
	"net/http"
	"time"

	"github.com/jeprecated/usagent/internal/analysis"
)

func (api API) resets(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	usage := api.App.Usage(now)
	writeJSON(w, http.StatusOK, analysis.ResetCalendar(usage, analysis.UsageAnalysisOptions{Now: now}))
}

func (api API) availability(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	usage := api.App.Usage(now)
	writeJSON(w, http.StatusOK, analysis.ProviderAvailabilitySummary(usage, analysis.UsageAnalysisOptions{Now: now}))
}
