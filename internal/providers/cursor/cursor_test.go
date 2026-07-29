package cursor

import (
	"context"
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
	if err := os.WriteFile(authPath, []byte(`{"accessToken":"cursor-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	reset := int64(1_800_000_000_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/usage" || r.Header.Get("Authorization") != "Bearer cursor-token" || r.Header.Get("Connect-Protocol-Version") != "1" {
			t.Fatalf("request: method=%s path=%s authorization=%q connect=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Connect-Protocol-Version"))
		}
		fmt.Fprint(w, `{"billingCycleEnd":"1800000000000","planUsage":{"autoPercentUsed":12.345,"apiPercentUsed":56.789}}`)
	}))
	defer srv.Close()

	result, err := NewWithClient(config.CursorConfig{AuthPath: authPath, EndpointURL: srv.URL + "/usage"}, srv.Client()).Fetch(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items=%+v", result.Items)
	}
	models, other := result.Items[0], result.Items[1]
	if models.ID != "cursor-models" || models.Used != 12.35 || models.Remaining != 87.65 || models.PercentUsed != 12.35 || models.Window.ResetAt == nil || *models.Window.ResetAt != reset || models.Reset == nil || models.Reset.ResetAt != reset {
		t.Fatalf("models=%+v", models)
	}
	if other.ID != "cursor-other-models" || other.Used != 56.79 || other.Remaining != 43.21 || other.PercentUsed != 56.79 {
		t.Fatalf("other=%+v", other)
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
