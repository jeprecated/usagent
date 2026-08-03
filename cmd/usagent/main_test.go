package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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
	if !strings.Contains(stdout.String(), "--include-low-confidence") || !strings.Contains(stdout.String(), "--within-ms") || !strings.Contains(stdout.String(), "--tiers") || !strings.Contains(stdout.String(), "--tags") {
		t.Fatalf("help=%s", stdout.String())
	}
}

func TestRootStatusAliasAndClientDestinationHelp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/usage" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		fmt.Fprint(w, `{"schemaVersion":2,"service":"usagent","generatedAt":1,"startedAt":1,"stale":false,"providers":[],"quotaItems":[]}`)
	}))
	defer srv.Close()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("server: {host: '127.0.0.1', port: 1}\nclient: {mode: prefer-daemon}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := runWithIO([]string{"status", "--config", configPath, "--daemon-url", srv.URL, "--json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"service": "usagent"`) || stderr.Len() != 0 {
		t.Fatalf("stdout=%s stderr=%s", stdout.String(), stderr.String())
	}

	for _, args := range [][]string{{"usage", "--help"}, {"expiring", "--help"}, {"--help"}} {
		stdout.Reset()
		stderr.Reset()
		if err := runWithIO(args, &stdout, &stderr); err != nil {
			t.Fatalf("args=%v: %v", args, err)
		}
		for _, flag := range []string{"--daemon-url", "--host", "--port"} {
			if !strings.Contains(stdout.String(), flag) {
				t.Fatalf("args=%v missing %s in help=%q", args, flag, stdout.String())
			}
		}
	}
}

func TestRootHelpUsesColorWhenForced(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	var stdout, stderr bytes.Buffer
	if err := runWithIO([]string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "\x1b[") || !strings.Contains(stdout.String(), "Commands:") {
		t.Fatalf("help=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestServeIsIndependentOfRequireDaemonClientPolicy(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configText := fmt.Sprintf(`server:
  host: "127.0.0.1"
  port: %d
  statePath: %q
client:
  url: "http://central.invalid:8788"
  mode: "require-daemon"
providers: {}
`, port, filepath.Join(dir, "snapshot.json"))
	if err := os.WriteFile(configPath, []byte(configText), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runWithContext(ctx, []string{"serve", "--config", configPath}, io.Discard, io.Discard)
	}()

	client := &http.Client{Timeout: 100 * time.Millisecond}
	healthURL := "http://127.0.0.1:" + strconv.Itoa(port) + "/healthz"
	deadline := time.Now().Add(3 * time.Second)
	started := false
	for time.Now().Before(deadline) {
		resp, requestErr := client.Get(healthURL)
		if requestErr == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				started = true
				break
			}
		}
		select {
		case runErr := <-done:
			t.Fatalf("serve exited before becoming healthy: %v", runErr)
		case <-time.After(20 * time.Millisecond):
		}
	}
	if !started {
		cancel()
		t.Fatal("serve did not become healthy")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve did not stop after context cancellation")
	}
}

func TestMCPHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := runWithIO([]string{"mcp", "--help"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "usagent mcp") || !strings.Contains(stdout.String(), "--daemon-url") || !strings.Contains(stdout.String(), "--offline") {
		t.Fatalf("help=%s stderr=%s", stdout.String(), stderr.String())
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
