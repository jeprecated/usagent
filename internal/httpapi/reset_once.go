package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jeprecated/usagent/internal/app"
	"github.com/jeprecated/usagent/internal/model"
)

// Reset-once grants spending authority without a standing config permission.
// It must not be exposed through a public listener, proxy or browser origin.
func (api API) localResetControl(w http.ResponseWriter, r *http.Request) bool {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	peerIP := net.ParseIP(peer)
	if err != nil || peerIP == nil || !peerIP.IsLoopback() || !loopbackHost(host) || !loopbackHost(api.App.Cfg.Server.Host) || r.Header.Get("Origin") != "" {
		writeError(w, http.StatusForbidden, "reset-once requires a direct local request to a loopback-only daemon")
		return false
	}
	for _, h := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Real-IP"} {
		if _, ok := r.Header[http.CanonicalHeaderKey(h)]; ok {
			writeError(w, http.StatusForbidden, "reset-once cannot be controlled through a proxy")
			return false
		}
	}
	return true
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func (api API) chatGPTResetOnceStatus(w http.ResponseWriter, r *http.Request) {
	if !api.localResetControl(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, api.App.ChatGPTResetOnceStatus())
}

func (api API) setChatGPTResetOnce(w http.ResponseWriter, r *http.Request) {
	if !api.localResetControl(w, r) {
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/v1/chatgpt/reset-once/")
	confirm := action + "-chatgpt-reset-once"
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
		return
	}
	if r.Header.Get("X-Usagent-Action") != confirm {
		writeError(w, http.StatusBadRequest, "missing explicit reset-once action header")
		return
	}
	var req struct {
		Confirm            string `json:"confirm"`
		AcknowledgeUnknown bool   `json:"acknowledgeUnknown"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid reset-once JSON body")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF || req.Confirm != confirm || (action != "cancel" && req.AcknowledgeUnknown) {
		writeError(w, http.StatusBadRequest, "invalid reset-once confirmation")
		return
	}
	var state model.ChatGPTResetOnce
	if action == "arm" {
		state, err = api.App.ArmChatGPTResetOnce(time.Now())
	} else {
		state, err = api.App.CancelChatGPTResetOnce(req.AcknowledgeUnknown, time.Now())
	}
	if err != nil {
		status := http.StatusConflict
		if !errors.Is(err, app.ErrResetOnceBusy) && state.Status == "" {
			status = http.StatusBadRequest
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}
