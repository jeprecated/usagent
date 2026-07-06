package zai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jmalloc/usagent/internal/config"
)

func TestZAINormalizesDocumentedFixtureHidesTimeLimitAndReset(t *testing.T) {
	t.Setenv("ZAI_API_KEY", "zai-token")
	reset := int64(1773596236982)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer zai-token" {
			t.Fatalf("Authorization=%q", got)
		}
		fmt.Fprint(w, `{"code":200,"msg":"ok","data":{"limits":[{"type":"TIME_LIMIT","unit":5,"number":1,"usage":1000,"currentValue":0,"remaining":1000,"percentage":0,"nextResetTime":1773596236982},{"type":"TOKENS_LIMIT","currentValue":250,"remaining":750,"percentage":25,"nextResetTime":1773596236982},{"type":"SESSION_LIMIT","currentValue":2,"remaining":8},{"type":"MYSTERY_LIMIT","currentValue":1,"remaining":3,"percentage":25}]}}`)
	}))
	defer srv.Close()
	res, err := NewWithClient(config.ZAIConfig{EndpointURL: srv.URL, TokenEnv: "ZAI_API_KEY", AuthScheme: "bearer"}, srv.Client()).Fetch(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 {
		t.Fatalf("items=%+v", res.Items)
	}
	first := res.Items[0]
	if first.ID != "z-ai-tokens-limit" || first.Unit != "tokens" || first.Used != 250 || first.Remaining != 750 || first.Limit != 1000 || first.PercentUsed != 25 || first.Window.ResetAt == nil || *first.Window.ResetAt != reset {
		t.Fatalf("tokens=%+v", first)
	}
	if res.Items[1].Window.ID != "session" || res.Items[1].PercentUsed != 20 {
		t.Fatalf("session=%+v", res.Items[1])
	}
	if res.Items[2].ID != "z-ai-mystery-limit" || !res.Items[2].Visible {
		t.Fatalf("unknown=%+v", res.Items[2])
	}
}

func TestZAIFallbackTokenAndHeaderOverride(t *testing.T) {
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("GLM_API_KEY", "glm-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Api-Key"); got != "glm-token" {
			t.Fatalf("X-Api-Key=%q", got)
		}
		fmt.Fprint(w, `{"code":200,"data":{"limits":[]}}`)
	}))
	defer srv.Close()
	_, err := NewWithClient(config.ZAIConfig{EndpointURL: srv.URL, TokenEnv: "ZAI_API_KEY", AuthScheme: "header", AuthHeader: "X-Api-Key"}, srv.Client()).Fetch(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
}

func TestZAINon2xxRetryAfterAndAPICodeError(t *testing.T) {
	t.Setenv("ZAI_API_KEY", "zai-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "4")
		http.Error(w, "nope", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	res, err := NewWithClient(config.ZAIConfig{EndpointURL: srv.URL, TokenEnv: "ZAI_API_KEY"}, srv.Client()).Fetch(context.Background(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "HTTP 429") || res.RetryAfter != 4*time.Second {
		t.Fatalf("err=%v retry=%s", err, res.RetryAfter)
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":401,"msg":"bad token","data":{"limits":[]}}`)
	}))
	defer srv2.Close()
	_, err = NewWithClient(config.ZAIConfig{EndpointURL: srv2.URL, TokenEnv: "ZAI_API_KEY"}, srv2.Client()).Fetch(context.Background(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "code 401") {
		t.Fatalf("err=%v", err)
	}
}
