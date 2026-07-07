package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
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
	mux.HandleFunc("GET /v1/chatgpt/reset-credits", api.chatGPTResetCredits)
	mux.HandleFunc("POST /v1/chatgpt/reset-credits/consume", api.consumeChatGPTResetCredit)
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

func (api API) chatGPTResetCredits(w http.ResponseWriter, r *http.Request) {
	res, err := api.App.ChatGPTResetCredits(r.Context(), time.Now())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (api API) consumeChatGPTResetCredit(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("content-type")), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
		return
	}
	if r.Header.Get("x-usagent-action") != "consume-chatgpt-reset-credit" {
		writeError(w, http.StatusBadRequest, "missing x-usagent-action: consume-chatgpt-reset-credit")
		return
	}
	var req struct {
		CreditID        string `json:"creditId"`
		RedeemRequestID string `json:"redeemRequestId"`
		Confirm         string `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Confirm != "consume-chatgpt-reset-credit" {
		writeError(w, http.StatusBadRequest, "confirm must be consume-chatgpt-reset-credit")
		return
	}
	res, err := api.App.ConsumeChatGPTResetCredit(r.Context(), req.CreditID, req.RedeemRequestID, time.Now())
	if err != nil {
		status := http.StatusBadGateway
		if strings.Contains(err.Error(), "disabled") {
			status = http.StatusForbidden
		} else if strings.Contains(err.Error(), "not enabled") || strings.Contains(err.Error(), "required") {
			status = http.StatusBadRequest
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
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

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": message}})
}
