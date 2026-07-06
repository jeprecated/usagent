package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/jmalloc/usagent/internal/app"
	"github.com/jmalloc/usagent/internal/model"
)

type API struct {
	App        *app.App
	ConfigPath string
}

func New(a *app.App, configPath string) http.Handler {
	return API{App: a, ConfigPath: configPath}.routes()
}

func (api API) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.healthz)
	mux.HandleFunc("GET /readyz", api.readyz)
	mux.HandleFunc("GET /v1/usage", api.usage)
	mux.HandleFunc("GET /v1/providers", api.providers)
	return withJSON(mux)
}

func (api API) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "usagent"})
}
func (api API) readyz(w http.ResponseWriter, r *http.Request) {
	_, err := os.Stat(api.ConfigPath)
	writeJSON(w, http.StatusOK, map[string]any{"ready": true, "configPath": api.ConfigPath, "configExists": err == nil})
}
func (api API) usage(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, api.App.Usage(time.Now()))
}
func (api API) providers(w http.ResponseWriter, r *http.Request) {
	u := api.App.Usage(time.Now())
	writeJSON(w, http.StatusOK, model.ProvidersResponse{Providers: u.Providers})
}

func withJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
