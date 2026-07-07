package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootExpiringUsageDispatchAndHelp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/expiring-usage" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		fmt.Fprint(w, `{"generatedAt":1,"opportunities":[{"provider":"chatgpt","itemId":"chatgpt-primary","label":"ChatGPT 5h","unit":"percent","remaining":80,"percentRemaining":80,"resetAt":3600000,"timeRemainingMs":3600000,"estimatedNaturalUseBeforeReset":5,"estimatedWastedAmount":75,"estimatedWastedPercent":75,"opportunityScore":0.6,"urgency":"extreme","confidence":"medium","reasons":[],"caveats":[]}]}`)
	}))
	defer srv.Close()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("server: {host: %q, port: %s, readAuth: {mode: none}}\nusageView: {providers: [chatgpt]}\n", u.Hostname(), u.Port())), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := runWithIO([]string{"expiring", "--config", configPath, "--json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"opportunities"`) || !strings.Contains(stdout.String(), `"provider": "chatgpt"`) {
		t.Fatalf("stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if err := runWithIO([]string{"expiring-usage", "--help"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "--include-low-confidence") || !strings.Contains(stdout.String(), "--within-ms") {
		t.Fatalf("help=%s", stdout.String())
	}
}

func TestRootUsageFlagsDispatchToUsageCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[{"id":"alpha","label":"Alpha","window":{"id":"week","label":"W","kind":"weekly"},"unit":"tokens","limit":100,"used":25,"remaining":75,"percentUsed":25}]}`)
	}))
	defer srv.Close()
	dir := t.TempDir()
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
            window: { id: window.id, label: window.label, kind: window.kind }
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
`, statePath, srv.URL)
	if err := os.WriteFile(configPath, []byte(configText), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := runWithIO([]string{"--config", configPath, "--offline", "--json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"service": "usagent"`) || !strings.Contains(stdout.String(), `"provider": "mine"`) {
		t.Fatalf("stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}
