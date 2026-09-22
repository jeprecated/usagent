package httpapi

import (
	"net/http"
	"strconv"
)

func (api API) resetEvents(w http.ResponseWriter, r *http.Request) {
	var after uint64
	if value := r.URL.Query().Get("after"); value != "" {
		var err error
		after, err = strconv.ParseUint(value, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "after must be an unsigned event ID")
			return
		}
	}
	page, err := api.App.ResetEvents.Page(r.URL.Query().Get("stream"), after)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, page)
}
