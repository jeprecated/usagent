package cursor

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/config"
)

func TestFetchTracksCursorModelAndOtherModelPools(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	token := "x." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"auth0|user-1"}`)) + ".x"
	if err := os.WriteFile(authPath, []byte(fmt.Sprintf(`{"accessToken":%q}`, token)), 0o600); err != nil {
		t.Fatal(err)
	}
	reset := int64(1_800_000_000_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/usage":
			if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("Connect-Protocol-Version") != "1" {
				t.Fatalf("request: method=%s authorization=%q connect=%q", r.Method, r.Header.Get("Authorization"), r.Header.Get("Connect-Protocol-Version"))
			}
			fmt.Fprint(w, `{"billingCycleEnd":"1800000000000","planUsage":{"autoPercentUsed":12.345,"apiPercentUsed":56.789}}`)
		case "/balance":
			cookie, err := r.Cookie("WorkosCursorSessionToken")
			if err != nil || r.Method != http.MethodGet || cookie.Value != "user-1%3A%3A"+token {
				t.Fatalf("balance request: method=%s cookie=%v err=%v", r.Method, cookie, err)
			}
			fmt.Fprint(w, `{"customerBalance":-5000}`)
		default:
			t.Fatalf("path=%s", r.URL.Path)
		}
	}))
	defer srv.Close()

	result, err := NewWithClient(config.CursorConfig{AuthPath: authPath, EndpointURL: srv.URL + "/usage", BalanceEndpointURL: srv.URL + "/balance"}, srv.Client()).Fetch(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("items=%+v", result.Items)
	}
	models, other, credits := result.Items[0], result.Items[1], result.Items[2]
	if models.ID != "cursor-models" || models.Used != 12.35 || models.Remaining != 87.65 || models.PercentUsed != 12.35 || models.Window.ResetAt == nil || *models.Window.ResetAt != reset || models.Reset == nil || models.Reset.ResetAt != reset {
		t.Fatalf("models=%+v", models)
	}
	if other.ID != "cursor-other-models" || other.Used != 56.79 || other.Remaining != 43.21 || other.PercentUsed != 56.79 {
		t.Fatalf("other=%+v", other)
	}
	if credits.ID != "cursor-extra-usage-credits" || credits.Unit != "usd" || credits.Remaining != 50 || credits.Window.ID != "extraCredits" {
		t.Fatalf("credits=%+v", credits)
	}
}

func TestFetchHidesEmptyExtraUsageBalance(t *testing.T) {
	token := "x." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"auth0|user-1"}`)) + ".x"
	t.Setenv("CURSOR_ACCESS_TOKEN", token)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/balance" {
			fmt.Fprint(w, `{"customerBalance":0}`)
			return
		}
		fmt.Fprint(w, `{"planUsage":{"autoPercentUsed":12,"apiPercentUsed":34}}`)
	}))
	defer srv.Close()

	result, err := NewWithClient(config.CursorConfig{EndpointURL: srv.URL + "/usage", BalanceEndpointURL: srv.URL + "/balance", TokenEnv: "CURSOR_ACCESS_TOKEN"}, srv.Client()).Fetch(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("empty balance should be hidden: items=%+v", result.Items)
	}
}

func TestFetchRejectsIncompleteUsageResponse(t *testing.T) {
	t.Setenv("CURSOR_ACCESS_TOKEN", "env-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"planUsage":{"autoPercentUsed":12}}`)
	}))
	defer srv.Close()

	_, err := NewWithClient(config.CursorConfig{EndpointURL: srv.URL, TokenEnv: "CURSOR_ACCESS_TOKEN"}, srv.Client()).Fetch(context.Background(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "missing plan usage percentages") {
		t.Fatalf("err=%v", err)
	}
}

func TestFetchUsesTokenEnvAndHonorsRetryAfter(t *testing.T) {
	t.Setenv("CURSOR_ACCESS_TOKEN", "env-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer env-token" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Retry-After", "4")
		http.Error(w, "nope", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	result, err := NewWithClient(config.CursorConfig{AuthPath: "/missing", EndpointURL: srv.URL, TokenEnv: "CURSOR_ACCESS_TOKEN"}, srv.Client()).Fetch(context.Background(), time.Now())
	if err == nil || result.RetryAfter != 4*time.Second {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
