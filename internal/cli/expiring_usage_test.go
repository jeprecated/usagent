package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmalloc/usagent/internal/analysis"
)

func TestFetchExpiringUsageFromDaemonPassesQueryParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/expiring-usage" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("within") != "24h0m0s" || q.Get("withinMs") != "60000" || q.Get("minimumRemainingPercent") != "25" || q.Get("providers") != "chatgpt,z-ai" || q.Get("includeLowConfidence") != "true" {
			t.Fatalf("query=%s", r.URL.RawQuery)
		}
		fmt.Fprint(w, `{"generatedAt":1,"opportunities":[{"provider":"chatgpt","itemId":"chatgpt-primary","label":"ChatGPT 5h","unit":"percent","remaining":80,"percentRemaining":80,"resetAt":3600000,"timeRemainingMs":3600000,"estimatedNaturalUseBeforeReset":5,"estimatedWastedAmount":75,"estimatedWastedPercent":75,"opportunityScore":0.6,"urgency":"extreme","confidence":"medium","reasons":[],"caveats":[]}]}`)
	}))
	defer srv.Close()
	host, port := splitServer(t, srv.URL)
	res, err := FetchExpiringUsageFromDaemon(context.Background(), testConfig(t, host, port), ExpiringUsageOptions{Timeout: time.Second, Within: 24 * time.Hour, WithinMS: 60000, MinimumRemainingPercent: 25, Providers: []string{"chatgpt", "z-ai"}, IncludeLowConfidence: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Opportunities) != 1 || res.Opportunities[0].Provider != "chatgpt" {
		t.Fatalf("res=%+v", res)
	}
}

func TestRunExpiringUsageJSONOutputFromDaemon(t *testing.T) {
	providerCalled := false
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalled = true
		t.Fatalf("provider API should not be called while daemon request succeeds")
	}))
	defer provider.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/expiring-usage" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		fmt.Fprint(w, `{"generatedAt":1,"opportunities":[{"provider":"claude-code","itemId":"claude-code-oauth-session","label":"Claude 5h","unit":"percent","remaining":80,"percentRemaining":80,"resetAt":3600000,"timeRemainingMs":3600000,"estimatedNaturalUseBeforeReset":5,"estimatedWastedAmount":75,"estimatedWastedPercent":75,"opportunityScore":0.6,"urgency":"extreme","confidence":"medium","reasons":[],"caveats":[]}]}`)
	}))
	defer srv.Close()
	host, port := splitServer(t, srv.URL)
	extra := fmt.Sprintf(`providers:
  custom:
    - id: should-not-call
      label: Should Not Call
      enabled: true
      endpoints:
        - id: quota
          url: %q
`, provider.URL)
	configPath := writeExpiringTestConfig(t, host, port, extra)
	var stdout, stderr bytes.Buffer
	if err := RunExpiringUsage(context.Background(), []string{"--config", configPath, "--json", "--within", "24h"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q", stderr.String())
	}
	if providerCalled {
		t.Fatal("provider API was called during daemon-backed expiring usage request")
	}
	var res analysis.ExpiringUsageResponse
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("stdout is not JSON: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "\n  \"opportunities\": [") || len(res.Opportunities) != 1 || res.Opportunities[0].ItemID != "claude-code-oauth-session" {
		t.Fatalf("stdout=%s", stdout.String())
	}
}

func TestRunExpiringUsageFallsBackToLocalDerivation(t *testing.T) {
	now := time.Now()
	resetAt := now.Add(time.Hour).UnixMilli()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"items":[{"id":"alpha","label":"Alpha","window":{"id":"session","label":"S","kind":"rolling"},"unit":"percent","limit":100,"used":20,"remaining":80,"percentUsed":20,"resetAt":%d}]}`, resetAt)
	}))
	defer provider.Close()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	configPath := filepath.Join(dir, "config.yaml")
	statePath := filepath.Join(dir, "snapshot.json")
	configText := fmt.Sprintf(`server:
  host: "127.0.0.1"
  port: 1
  statePath: %q
providers:
  custom:
    - id: mine
      label: Mine
      enabled: true
      endpoints:
        - id: quota
          url: %q
          itemsPath: items
          item:
            id: id
            label: label
            window: { id: window.id, label: window.label, kind: window.kind, resetAt: resetAt, resetAtFormat: unixMs }
            unit: unit
            limit: limit
            used: used
            remaining: remaining
            percentUsed: percentUsed
            visible: true
usageView:
  providers: [mine]
quota:
  refreshMs: 300000
`, statePath, provider.URL)
	if err := os.WriteFile(configPath, []byte(configText), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := RunExpiringUsage(context.Background(), []string{"--config", configPath, "--timeout", "2s", "--within", "24h"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "daemon unavailable") {
		t.Fatalf("stderr=%q", stderr.String())
	}
	got := stdout.String()
	for _, want := range []string{"mine/Alpha:", "80% remaining", "waste", "resets in", "urgency", "confidence medium"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout=%q missing %q", got, want)
		}
	}
}

func writeExpiringTestConfig(t *testing.T, host string, port int, extra string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	statePath := filepath.Join(dir, "snapshot.json")
	text := fmt.Sprintf(`server:
  host: %q
  port: %d
  statePath: %q
  readAuth: {mode: none}
usageView:
  providers: [claude-code, chatgpt, z-ai]
%s
`, host, port, statePath, extra)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
