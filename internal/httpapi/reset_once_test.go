package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jeprecated/usagent/internal/app"
	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/model"
)

func resetAPI(t *testing.T) (*app.App, http.Handler) {
	t.Helper()
	cfg := config.Default()
	cfg.Server.StatePath = filepath.Join(t.TempDir(), "snapshot.json")
	cfg.Providers.ChatGPT.Enabled = true
	cfg.Providers.ChatGPT.AuthPath = filepath.Join(t.TempDir(), "auth.json")
	// Tests must never have access to a real provider or OAuth credential.
	cfg.Providers.ChatGPT.TokenEnv = ""
	cfg.Providers.ChatGPT.AccountIDEnv = ""
	cfg.Providers.ChatGPT.EndpointURL = "http://127.0.0.1:1/usage"
	cfg.Providers.ChatGPT.ResetConsumeEndpointURL = "http://127.0.0.1:1/consume"
	if err := os.WriteFile(cfg.Providers.ChatGPT.AuthPath, []byte(`{"tokens":{"access_token":"fake","account_id":"account-a"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfg, nil)
	a.EnableResetOnce()
	return a, New(a, "")
}
func resetRequest(action string) *http.Request {
	r := httptest.NewRequest("POST", "http://127.0.0.1/v1/chatgpt/reset-once/"+action, strings.NewReader(fmt.Sprintf(`{"confirm":%q}`, action+"-chatgpt-reset-once")))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Usagent-Action", action+"-chatgpt-reset-once")
	return r
}

func TestResetOnceAPIArmStatusCancel(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "0.0.0.0", "::"} {
		t.Run(host, func(t *testing.T) {
			a, h := resetAPI(t)
			a.Cfg.Server.Host = host
			for _, action := range []string{"arm", "arm", "cancel"} {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, resetRequest(action))
				want := "armed"
				if action == "cancel" {
					want = "cancelled"
				}
				var state model.ChatGPTResetOnce
				if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &state) != nil || state.Status != want {
					t.Fatalf("%s: %d %s", action, rec.Code, rec.Body.String())
				}
			}
			if a.Cfg.Providers.ChatGPT.AllowResetConsume {
				t.Fatal("arming must not enable general consumption")
			}
			r := httptest.NewRequest("GET", "http://localhost/v1/chatgpt/reset-once", nil)
			r.RemoteAddr = "[::1]:1234"
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"cancelled"`) {
				t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestResetOnceAPIRejectsUnsafeRequests(t *testing.T) {
	for _, action := range []string{"arm", "cancel"} {
		for name, mutate := range map[string]func(*http.Request){
			"remote":             func(r *http.Request) { r.RemoteAddr = "192.0.2.1:1234" },
			"invalid-peer":       func(r *http.Request) { r.RemoteAddr = "localhost:1234" },
			"dns-rebinding":      func(r *http.Request) { r.Host = "attacker.example" },
			"browser-origin":     func(r *http.Request) { r.Header.Set("Origin", "http://localhost") },
			"forwarded":          func(r *http.Request) { r.Header.Set("Forwarded", "for=192.0.2.1") },
			"forwarded-for":      func(r *http.Request) { r.Header.Set("X-Forwarded-For", "127.0.0.1") },
			"forwarded-host":     func(r *http.Request) { r.Header.Set("X-Forwarded-Host", "localhost") },
			"real-ip":            func(r *http.Request) { r.Header.Set("X-Real-IP", "127.0.0.1") },
			"missing-header":     func(r *http.Request) { r.Header.Del("X-Usagent-Action") },
			"wrong-header":       func(r *http.Request) { r.Header.Set("X-Usagent-Action", "consume-chatgpt-reset-credit") },
			"wrong-content-type": func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") },
			"fake-content-type":  func(r *http.Request) { r.Header.Set("Content-Type", "application/json-evil") },
			"missing-confirm":    func(r *http.Request) { r.Body = http.NoBody },
			"unknown-field": func(r *http.Request) {
				r.Body = io.NopCloser(strings.NewReader(`{"confirm":"arm-chatgpt-reset-once","typo":true}`))
			},
			"extra-json": func(r *http.Request) {
				r.Body = io.NopCloser(strings.NewReader(`{"confirm":"arm-chatgpt-reset-once"} {}`))
			},
		} {
			t.Run(action+"/"+name, func(t *testing.T) {
				a, h := resetAPI(t)
				a.Cfg.Server.Host = "0.0.0.0"
				r := resetRequest(action)
				mutate(r)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, r)
				if rec.Code < 400 {
					t.Fatalf("unsafe request accepted: %d %s", rec.Code, rec.Body.String())
				}
				if a.ChatGPTResetOnceStatus().Status != "off" {
					t.Fatal("unsafe request mutated state")
				}
			})
		}
	}
	t.Run("get-cannot-arm", func(t *testing.T) {
		a, h := resetAPI(t)
		r := resetRequest("arm")
		r.Method = "GET"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != 405 || a.ChatGPTResetOnceStatus().Status != "off" {
			t.Fatal("GET could arm")
		}
	})
}
