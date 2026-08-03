package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequireDaemonUsageFailuresAreFailClosed(t *testing.T) {
	tests := []struct {
		name    string
		server  func(*testing.T) (string, func())
		timeout string
	}{
		{
			name: "connection",
			server: func(*testing.T) (string, func()) {
				return "http://127.0.0.1:1", func() {}
			},
			timeout: "200ms",
		},
		{
			name: "timeout",
			server: func(t *testing.T) (string, func()) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					time.Sleep(100 * time.Millisecond)
					writeValidUsage(w)
				}))
				return srv.URL, srv.Close
			},
			timeout: "10ms",
		},
		{
			name: "non-2xx",
			server: func(t *testing.T) (string, func()) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Error(w, "no", http.StatusServiceUnavailable)
				}))
				return srv.URL, srv.Close
			},
			timeout: "1s",
		},
		{
			name: "response validation",
			server: func(t *testing.T) (string, func()) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					fmt.Fprint(w, `{"schemaVersion":1,"service":"other","providers":[],"quotaItems":[]}`)
				}))
				return srv.URL, srv.Close
			},
			timeout: "1s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origin, closeServer := tt.server(t)
			defer closeServer()

			var providerCalls, alternateCalls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerCalls.Add(1)
				writeProviderUsage(w)
			}))
			defer provider.Close()
			alternate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				alternateCalls.Add(1)
				writeValidUsage(w)
			}))
			defer alternate.Close()

			dir := t.TempDir()
			xdg := filepath.Join(dir, "xdg")
			t.Setenv("XDG_CONFIG_HOME", xdg)
			writeUserDaemonConfig(t, xdg, alternate.URL, "prefer-daemon")
			statePath := filepath.Join(dir, "state", "snapshot.json")
			configPath := writePolicyConfig(t, dir, origin, "require-daemon", statePath, provider.URL)

			var stdout, stderr bytes.Buffer
			err := RunUsage(context.Background(), []string{"--config", configPath, "--timeout", tt.timeout, "--json"}, &stdout, &stderr)
			if err == nil {
				t.Fatal("require-daemon failure unexpectedly succeeded")
			}
			for _, want := range []string{origin, "local fallback is forbidden", "require-daemon"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error=%q missing %q", err, want)
				}
			}
			if providerCalls.Load() != 0 || alternateCalls.Load() != 0 {
				t.Fatalf("provider calls=%d alternate calls=%d", providerCalls.Load(), alternateCalls.Load())
			}
			assertNoLocalState(t, statePath)
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRequireDaemonExpiringUsageFailureIsFailClosed(t *testing.T) {
	var providerCalls, alternateCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		writeProviderUsage(w)
	}))
	defer provider.Close()
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{}`)
	}))
	defer primary.Close()
	alternate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		alternateCalls.Add(1)
		fmt.Fprint(w, `{"generatedAt":1,"opportunities":[]}`)
	}))
	defer alternate.Close()

	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	writeUserDaemonConfig(t, xdg, alternate.URL, "prefer-daemon")
	statePath := filepath.Join(dir, "state", "snapshot.json")
	configPath := writePolicyConfig(t, dir, primary.URL, "require-daemon", statePath, provider.URL)

	var stdout, stderr bytes.Buffer
	err := RunExpiringUsage(context.Background(), []string{"--config", configPath, "--timeout", "1s", "--json"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), primary.URL) || !strings.Contains(err.Error(), "generatedAt must be positive") || !strings.Contains(err.Error(), "local fallback is forbidden") {
		t.Fatalf("error=%v", err)
	}
	if providerCalls.Load() != 0 || alternateCalls.Load() != 0 {
		t.Fatalf("provider calls=%d alternate calls=%d", providerCalls.Load(), alternateCalls.Load())
	}
	assertNoLocalState(t, statePath)
}

func TestPolicyConflictsMakeNoNetworkRequests(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeValidUsage(w)
	}))
	defer srv.Close()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "snapshot.json")
	preferPath := writePolicyConfig(t, dir, srv.URL, "prefer-daemon", statePath, srv.URL)
	requirePath := writePolicyConfig(t, dir, srv.URL, "require-daemon", statePath, srv.URL)
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "usage daemon URL and offline", run: func() error {
			return RunUsage(context.Background(), []string{"--config", preferPath, "--daemon-url", srv.URL, "--offline"}, &bytes.Buffer{}, &bytes.Buffer{})
		}},
		{name: "expiring daemon URL and host", run: func() error {
			return RunExpiringUsage(context.Background(), []string{"--config", preferPath, "--daemon-url", srv.URL, "--host", "127.0.0.1"}, &bytes.Buffer{}, &bytes.Buffer{})
		}},
		{name: "usage require daemon offline", run: func() error {
			return RunUsage(context.Background(), []string{"--config", requirePath, "--offline"}, &bytes.Buffer{}, &bytes.Buffer{})
		}},
		{name: "expiring require daemon offline", run: func() error {
			return RunExpiringUsage(context.Background(), []string{"--config", requirePath, "--offline"}, &bytes.Buffer{}, &bytes.Buffer{})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := calls.Load()
			if err := tt.run(); err == nil {
				t.Fatal("policy conflict unexpectedly succeeded")
			}
			if calls.Load() != before {
				t.Fatalf("network calls changed from %d to %d", before, calls.Load())
			}
		})
	}
	assertNoLocalState(t, statePath)
}

func TestExplicitDaemonURLSuppressesAlternateDaemonButAllowsLocalFallback(t *testing.T) {
	var explicitCalls, alternateCalls, providerCalls atomic.Int32
	explicit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		explicitCalls.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer explicit.Close()
	alternate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		alternateCalls.Add(1)
		writeValidUsage(w)
	}))
	defer alternate.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		writeProviderUsage(w)
	}))
	defer provider.Close()

	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	writeUserDaemonConfig(t, xdg, alternate.URL, "require-daemon")
	statePath := filepath.Join(dir, "state", "snapshot.json")
	configPath := writePolicyConfig(t, dir, "http://127.0.0.1:1", "prefer-daemon", statePath, provider.URL)

	var stdout, stderr bytes.Buffer
	if err := RunUsage(context.Background(), []string{"--config", configPath, "--daemon-url", explicit.URL, "--timeout", "1s"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if explicitCalls.Load() != 1 || alternateCalls.Load() != 0 || providerCalls.Load() != 1 {
		t.Fatalf("explicit=%d alternate=%d provider=%d", explicitCalls.Load(), alternateCalls.Load(), providerCalls.Load())
	}
	if !strings.Contains(stdout.String(), "Mine") || !strings.Contains(stdout.String(), "75 tokens") || !strings.Contains(stderr.String(), "daemon unavailable") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestLocalOnlyUsageSkipsDaemonAndRefreshesLocally(t *testing.T) {
	var daemonCalls, providerCalls atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		daemonCalls.Add(1)
		writeValidUsage(w)
	}))
	defer daemon.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		writeProviderUsage(w)
	}))
	defer provider.Close()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state", "snapshot.json")
	configPath := writePolicyConfig(t, dir, daemon.URL, "local-only", statePath, provider.URL)
	var stdout, stderr bytes.Buffer
	if err := RunUsage(context.Background(), []string{"--config", configPath, "--host", "unused.invalid", "--port", "9999"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if daemonCalls.Load() != 0 || providerCalls.Load() != 1 {
		t.Fatalf("daemon=%d provider=%d", daemonCalls.Load(), providerCalls.Load())
	}
	if stderr.Len() != 0 || !strings.Contains(stdout.String(), "Mine") || !strings.Contains(stdout.String(), "75 tokens") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestPreferDaemonOfflineSkipsDaemonAndRefreshesLocally(t *testing.T) {
	var daemonCalls, providerCalls atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		daemonCalls.Add(1)
		writeValidUsage(w)
	}))
	defer daemon.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		writeProviderUsage(w)
	}))
	defer provider.Close()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state", "snapshot.json")
	configPath := writePolicyConfig(t, dir, daemon.URL, "prefer-daemon", statePath, provider.URL)
	var stdout, stderr bytes.Buffer
	if err := RunUsage(context.Background(), []string{"--config", configPath, "--offline"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if daemonCalls.Load() != 0 || providerCalls.Load() != 1 {
		t.Fatalf("daemon=%d provider=%d", daemonCalls.Load(), providerCalls.Load())
	}
	if stderr.Len() != 0 || !strings.Contains(stdout.String(), "Mine") || !strings.Contains(stdout.String(), "75 tokens") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func writePolicyConfig(t *testing.T, dir, clientURL, mode, statePath, providerURL string) string {
	t.Helper()
	path := filepath.Join(dir, "config-"+mode+".yaml")
	text := fmt.Sprintf(`server:
  host: "127.0.0.1"
  port: 1
  statePath: %q
client:
  url: %q
  mode: %q
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
usageView: {providers: [mine]}
quota: {refreshMs: 300000}
`, statePath, clientURL, mode, providerURL)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeUserDaemonConfig(t *testing.T, xdg, clientURL, mode string) {
	t.Helper()
	path := filepath.Join(xdg, "usagent", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	text := fmt.Sprintf("server: {host: '127.0.0.1', port: 1}\nclient: {url: %q, mode: %q}\n", clientURL, mode)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeValidUsage(w http.ResponseWriter) {
	fmt.Fprint(w, `{"schemaVersion":2,"service":"usagent","generatedAt":1,"startedAt":1,"stale":false,"providers":[],"quotaItems":[]}`)
}

func writeProviderUsage(w http.ResponseWriter) {
	fmt.Fprint(w, `{"items":[{"id":"alpha","label":"Alpha","window":{"id":"week","label":"W","kind":"weekly"},"unit":"tokens","limit":100,"used":25,"remaining":75,"percentUsed":25}]}`)
}

func assertNoLocalState(t *testing.T, statePath string) {
	t.Helper()
	for _, path := range []string{statePath, statePath + ".refresh.lock", filepath.Join(filepath.Dir(statePath), "usage-history.json")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unexpected local state at %s (err=%v)", path, err)
		}
	}
}
