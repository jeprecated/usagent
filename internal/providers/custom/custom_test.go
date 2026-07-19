package custom

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/config"
)

func TestCustomProviderMapsJSONArrayAuthLiteralPathAndUnixMs(t *testing.T) {
	t.Setenv("MINE_TOKEN", "secret-test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-test-token" {
			t.Fatalf("Authorization=%q", got)
		}
		if got := r.Header.Get("X-Client"); got != "usagent-test" {
			t.Fatalf("X-Client=%q", got)
		}
		fmt.Fprint(w, `{"items":[{"id":"alpha","label":"Alpha","window":{"id":"week","label":"W","kind":"weekly"},"unit":"tokens","limit":1000,"used":250,"remaining":750,"percentUsed":25,"resetAt":1773596236982}]}`)
	}))
	defer srv.Close()
	cfg := config.CustomProviderConfig{ID: "my-provider", Label: "Mine", Enabled: true, Endpoints: []config.CustomEndpointConfig{{ID: "quota", URL: srv.URL, Headers: map[string]string{"X-Client": "usagent-test"}, Auth: config.CustomAuthConfig{Type: "bearer", TokenEnv: "MINE_TOKEN"}, ItemsPath: "items", Item: config.CustomItemMapping{ID: "id", Label: "literal:Fixed Label", Window: config.CustomWindowMapping{ID: "window.id", Label: "window.label", Kind: "window.kind", ResetAt: "resetAt", ResetAtFormat: "unixMs"}, Unit: "unit", Limit: "limit", Used: "used", Remaining: "remaining", PercentUsed: "percentUsed", Visible: true}}}}
	res, err := NewWithClient(cfg, srv.Client()).Fetch(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("items=%+v", res.Items)
	}
	item := res.Items[0]
	if item.ID != "my-provider-alpha" || item.Label != "Fixed Label" || item.Provider != "my-provider" || item.Window.ID != "week" || item.Window.Kind != "weekly" || item.Unit != "tokens" || item.Limit != 1000 || item.Used != 250 || item.Remaining != 750 || item.PercentUsed != 25 || !item.Visible || item.Window.ResetAt == nil || *item.Window.ResetAt != 1773596236982 {
		t.Fatalf("item=%+v", item)
	}
}

func TestCustomProviderResetFormatsAndHeaderAuth(t *testing.T) {
	t.Setenv("CUSTOM_TOKEN", "raw-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Token"); got != "raw-token" {
			t.Fatalf("X-Token=%q", got)
		}
		fmt.Fprint(w, `{"id":"one","limit":"10","used":"2","resetSeconds":1773596236,"resetRFC":"2026-03-15T12:17:16Z"}`)
	}))
	defer srv.Close()
	base := config.CustomEndpointConfig{ID: "single", URL: srv.URL, Auth: config.CustomAuthConfig{Type: "header", TokenEnv: "CUSTOM_TOKEN", HeaderName: "X-Token"}, Item: config.CustomItemMapping{ID: "id", Label: "literal:One", Window: config.CustomWindowMapping{ID: "literal:session", Label: "literal:S", Kind: "literal:rolling", ResetAt: "resetSeconds", ResetAtFormat: "unixSeconds"}, Unit: "literal:count", Limit: "limit", Used: "used", Visible: true}}
	res, err := NewWithClient(config.CustomProviderConfig{ID: "custom", Endpoints: []config.CustomEndpointConfig{base}}, srv.Client()).Fetch(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := *res.Items[0].Window.ResetAt; got != 1773596236000 {
		t.Fatalf("unixSeconds reset=%d", got)
	}
	base.Item.Window.ResetAt = "resetRFC"
	base.Item.Window.ResetAtFormat = "rfc3339"
	res, err = NewWithClient(config.CustomProviderConfig{ID: "custom", Endpoints: []config.CustomEndpointConfig{base}}, srv.Client()).Fetch(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := *res.Items[0].Window.ResetAt; got != int64(1773577036000) {
		t.Fatalf("rfc3339 reset=%d", got)
	}
}

func TestCustomProviderNon2xxRetryAfterAndMissingToken(t *testing.T) {
	t.Setenv("CUSTOM_TOKEN", "secret")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	cfg := config.CustomProviderConfig{ID: "custom", Endpoints: []config.CustomEndpointConfig{{ID: "quota", URL: srv.URL, Auth: config.CustomAuthConfig{Type: "bearer", TokenEnv: "CUSTOM_TOKEN"}}}}
	res, err := NewWithClient(cfg, srv.Client()).Fetch(context.Background(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "HTTP 429") || res.RetryAfter != 5*time.Second {
		t.Fatalf("err=%v retry=%s", err, res.RetryAfter)
	}
	t.Setenv("CUSTOM_TOKEN", "")
	_, err = NewWithClient(cfg, srv.Client()).Fetch(context.Background(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "CUSTOM_TOKEN") {
		t.Fatalf("err=%v", err)
	}
}
