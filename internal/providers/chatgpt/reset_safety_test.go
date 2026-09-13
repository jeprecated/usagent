package chatgpt

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/config"
)

func TestBindAccountPinsCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	writeAuth := func(account, token string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"tokens":{"account_id":%q,"access_token":%q}}`, account, token)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeAuth("account-a", "token-a")
	var headersOK atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headersOK.Store(r.Header.Get("Authorization") == "Bearer token-a" && r.Header.Get("ChatGPT-Account-Id") == "account-a")
		fmt.Fprint(w, `{"windows_reset":1,"code":"reset","redeemed_at":"2026-06-13T13:12:31Z"}`)
	}))
	defer srv.Close()
	p := NewWithClient(config.ChatGPTConfig{AuthPath: path, ResetConsumeEndpointURL: srv.URL}, srv.Client())
	bound, account, err := p.BindAccount("account-a")
	if err != nil || account != "account-a" {
		t.Fatalf("account=%q err=%v", account, err)
	}
	writeAuth("account-b", "token-b")
	if _, _, err := p.BindAccount("account-a"); err == nil {
		t.Fatal("account change accepted")
	}
	if _, err := bound.ConsumeResetCredit(context.Background(), "credit", "request", time.Now()); err != nil {
		t.Fatal(err)
	}
	if !headersOK.Load() {
		t.Fatal("bound provider re-read changed credentials")
	}
	writeAuth("", "token")
	if _, _, err := p.BindAccount(""); err == nil {
		t.Fatal("missing account ID accepted")
	}
}

func TestConsumeDoesNotFollowRedirects(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			t.Setenv("RESET_TEST_TOKEN", "fake")
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if r.URL.Path == "/consume" {
					http.Redirect(w, r, "/second", code)
					return
				}
				fmt.Fprint(w, `{"windows_reset":1,"code":"reset","redeemed_at":"2026-06-13T13:12:31Z"}`)
			}))
			defer srv.Close()
			p := NewWithClient(config.ChatGPTConfig{TokenEnv: "RESET_TEST_TOKEN", ResetConsumeEndpointURL: srv.URL + "/consume"}, srv.Client())
			if _, err := p.ConsumeResetCredit(context.Background(), "credit", "request", time.Now()); err == nil {
				t.Error("redirect accepted")
			}
			if hits.Load() != 1 {
				t.Fatalf("sent %d requests, want exactly one", hits.Load())
			}
		})
	}
}

func TestConsumeRequiresPositiveConfirmation(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"code":"reset","windows_reset":0}`, `{"code":"other","windows_reset":1}`, `{"code":"reset","windows_reset":1,"redeemed_at":"not-a-time"}`, `{"code":"reset","windows_reset":1}`, `{"code":"reset","windows_reset":2,"credit":{"redeemed_at":"not-a-time"}}`, `{"code":"nothing_to_reset","windows_reset":0,"credit":{"status":"available"}}`} {
		t.Run(body, func(t *testing.T) {
			t.Setenv("RESET_TEST_TOKEN", "fake")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer srv.Close()
			p := NewWithClient(config.ChatGPTConfig{TokenEnv: "RESET_TEST_TOKEN", ResetConsumeEndpointURL: srv.URL}, srv.Client())
			if _, err := p.ConsumeResetCredit(context.Background(), "credit", "request", time.Now()); err == nil {
				t.Fatal("unconfirmed consume accepted")
			}
		})
	}
}

func TestConsumeAcceptsObservedWHAMSuccess(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"nested-redeemed-at", `{"code":"reset","windows_reset":2,"credit":{"id":"RateLimitResetCredit_1","status":"redeemed","redeemed_at":"2026-07-10T12:30:00Z"}}`},
		{"already-redeemed", `{"code":"already_redeemed","windows_reset":0,"credit":{"id":"RateLimitResetCredit_1","status":"redeemed","redeemed_at":"2026-07-10T12:30:00Z"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RESET_TEST_TOKEN", "fake")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
			defer srv.Close()
			p := NewWithClient(config.ChatGPTConfig{TokenEnv: "RESET_TEST_TOKEN", ResetConsumeEndpointURL: srv.URL}, srv.Client())
			res, err := p.ConsumeResetCredit(context.Background(), "RateLimitResetCredit_1", "request", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if res.Code == "" || res.RedeemedAt == "" {
				t.Fatalf("incomplete confirmation: %+v", res)
			}
		})
	}
}
