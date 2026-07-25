package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestUsageToolPrefersConfiguredDaemon(t *testing.T) {
	seenUsage := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/usage" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		seenUsage = true
		fmt.Fprint(w, `{"schemaVersion":2,"service":"usagent","generatedAt":1,"startedAt":1,"stale":false,"providers":[{"id":"chatgpt","label":"ChatGPT Pro","state":"fresh","source":"pull"}],"quotaItems":[{"id":"chatgpt-primary","provider":"chatgpt","label":"ChatGPT","window":{"id":"5h","label":"5h","kind":"rolling"},"unit":"percent","limit":100,"used":25,"remaining":75,"percentUsed":25,"state":"fresh","visible":true}]}`)
	}))
	defer srv.Close()

	configPath := writeDaemonConfig(t, srv.URL)
	input := frame(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}}) +
		frame(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized", "params": map[string]any{}}) +
		frame(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "usage", "arguments": map[string]any{"json": true}}})

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--config", configPath}, strings.NewReader(input), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !seenUsage {
		t.Fatal("daemon usage endpoint was not called")
	}
	responses := readJSONLResponses(t, stdout.String())
	if len(responses) != 2 {
		t.Fatalf("responses=%d stdout=%s stderr=%s", len(responses), stdout.String(), stderr.String())
	}
	body, _ := json.Marshal(responses[1])
	if !strings.Contains(string(body), `"service": "usagent"`) && !strings.Contains(string(body), `\"service\": \"usagent\"`) {
		t.Fatalf("usage tool response missing usage JSON: %s", string(body))
	}
}

func TestJSONLProtocol(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n"
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--config", writeDaemonConfig(t, "http://127.0.0.1:1")}, strings.NewReader(input), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	responses := readJSONLResponses(t, stdout.String())
	if len(responses) != 1 {
		t.Fatalf("responses=%d stdout=%s stderr=%s", len(responses), stdout.String(), stderr.String())
	}
	body, _ := json.Marshal(responses[0])
	if !strings.Contains(string(body), "tokens_to_burn") {
		t.Fatalf("tools/list response missing tokens_to_burn: %s", string(body))
	}
}

func TestTokensToBurnToolQueriesDaemonExpiringUsage(t *testing.T) {
	var rawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/expiring-usage" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		rawQuery = r.URL.RawQuery
		fmt.Fprint(w, `{"generatedAt":1,"opportunities":[{"provider":"chatgpt","itemId":"chatgpt-primary","label":"ChatGPT 5h","unit":"percent","remaining":80,"percentRemaining":80,"resetAt":3600000,"timeRemainingMs":3600000,"estimatedNaturalUseBeforeReset":5,"estimatedWastedAmount":75,"estimatedWastedPercent":75,"opportunityScore":0.6,"urgency":"extreme","confidence":"medium","reasons":[],"caveats":[]}]}`)
	}))
	defer srv.Close()

	configPath := writeDaemonConfig(t, srv.URL)
	input := frame(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "tokens_to_burn", "arguments": map[string]any{"within": "24h", "providers": []any{"chatgpt"}, "includeLowConfidence": true}}})

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--config", configPath}, strings.NewReader(input), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rawQuery, "within=24h0m0s") || !strings.Contains(rawQuery, "providers=chatgpt") || !strings.Contains(rawQuery, "includeLowConfidence=true") {
		t.Fatalf("query=%s", rawQuery)
	}
	if !strings.Contains(stdout.String(), "Expiring usage") || !strings.Contains(stdout.String(), "ChatGPT 5h") {
		t.Fatalf("stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

func TestStartupOfflineRejectsRequireDaemonBeforeServing(t *testing.T) {
	var providerCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		writeMCPProviderUsage(w)
	}))
	defer provider.Close()
	configPath, statePath := writeMCPPolicyConfig(t, "http://127.0.0.1:1", "require-daemon", provider.URL)

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--config", configPath, "--offline"}, strings.NewReader(frame(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--offline cannot be used") || !strings.Contains(err.Error(), "require-daemon") {
		t.Fatalf("error=%v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 || providerCalls.Load() != 0 {
		t.Fatalf("stdout=%q stderr=%q providerCalls=%d", stdout.String(), stderr.String(), providerCalls.Load())
	}
	assertNoMCPLocalState(t, statePath)
}

func TestStartupStaticPolicyConflictsBeforeServing(t *testing.T) {
	preferPath, _ := writeMCPPolicyConfig(t, "http://127.0.0.1:1", "prefer-daemon", "http://127.0.0.1:2")
	localPath, _ := writeMCPPolicyConfig(t, "http://127.0.0.1:1", "local-only", "http://127.0.0.1:2")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "explicit daemon and offline", args: []string{"--config", preferPath, "--daemon-url", "http://127.0.0.1:3", "--offline"}, want: "--daemon-url cannot be combined with --offline"},
		{name: "explicit daemon and legacy host", args: []string{"--config", preferPath, "--daemon-url", "http://127.0.0.1:3", "--host", "127.0.0.1"}, want: "--daemon-url cannot be combined with --host or --port"},
		{name: "explicit daemon and local only", args: []string{"--config", localPath, "--daemon-url", "http://127.0.0.1:3"}, want: "--daemon-url cannot be used when client.mode=local-only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), tt.args, strings.NewReader(frame(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})), &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v, want %q", err, tt.want)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("server handled input before rejecting conflict: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestStartupConfigLoadFailureRemainsLazy(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	input := frame(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--config", missing}, strings.NewReader(input), &stdout, &stderr); err != nil {
		t.Fatalf("bare MCP startup made config load fatal: %v", err)
	}
	responses := readJSONLResponses(t, stdout.String())
	if len(responses) != 1 || responses[0]["result"] == nil {
		t.Fatalf("responses=%v stderr=%q", responses, stderr.String())
	}
}

func TestPerToolOfflineConflictsAreFailClosed(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		explicitURL bool
		wantError   string
	}{
		{name: "require daemon", mode: "require-daemon", wantError: "--offline cannot be used"},
		{name: "explicit daemon URL", mode: "prefer-daemon", explicitURL: true, wantError: "--daemon-url cannot be combined with --offline"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var daemonCalls, providerCalls atomic.Int32
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				daemonCalls.Add(1)
				writeMCPDaemonResponse(w, r)
			}))
			defer daemon.Close()
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerCalls.Add(1)
				writeMCPProviderUsage(w)
			}))
			defer provider.Close()
			configPath, statePath := writeMCPPolicyConfig(t, daemon.URL, tt.mode, provider.URL)
			args := []string{"--config", configPath}
			if tt.explicitURL {
				args = append(args, "--daemon-url", daemon.URL)
			}
			responses, stderr := runMCPToolCalls(t, args,
				map[string]any{"name": "usage", "arguments": map[string]any{"offline": true}},
				map[string]any{"name": "tokens_to_burn", "arguments": map[string]any{"offline": true}},
			)
			for i, response := range responses {
				assertMCPToolError(t, response, tt.wantError)
				if !strings.Contains(mcpResponseText(response), "Diagnostics:") {
					t.Fatalf("response %d lacks diagnostics: %v", i, response)
				}
			}
			if stderr != "" || daemonCalls.Load() != 0 || providerCalls.Load() != 0 {
				t.Fatalf("stderr=%q daemonCalls=%d providerCalls=%d", stderr, daemonCalls.Load(), providerCalls.Load())
			}
			assertNoMCPLocalState(t, statePath)
		})
	}
}

func TestExplicitDaemonURLOverridesConfiguredDestination(t *testing.T) {
	var explicitCalls, configuredCalls, providerCalls atomic.Int32
	explicit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		explicitCalls.Add(1)
		writeMCPDaemonResponse(w, r)
	}))
	defer explicit.Close()
	configured := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		configuredCalls.Add(1)
		writeMCPDaemonResponse(w, r)
	}))
	defer configured.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		writeMCPProviderUsage(w)
	}))
	defer provider.Close()
	configPath, statePath := writeMCPPolicyConfig(t, configured.URL, "prefer-daemon", provider.URL)

	responses, stderr := runMCPToolCalls(t, []string{"--config", configPath, "--daemon-url", explicit.URL},
		map[string]any{"name": "usage", "arguments": map[string]any{}},
	)
	if len(responses) != 1 || mcpToolIsError(responses[0]) {
		t.Fatalf("responses=%v stderr=%q", responses, stderr)
	}
	if explicitCalls.Load() != 1 || configuredCalls.Load() != 0 || providerCalls.Load() != 0 {
		t.Fatalf("explicit=%d configured=%d provider=%d", explicitCalls.Load(), configuredCalls.Load(), providerCalls.Load())
	}
	assertNoMCPLocalState(t, statePath)
}

func TestRequireDaemonToolsUseOnlyHealthyCentralDaemon(t *testing.T) {
	var usageCalls, expiringCalls, providerCalls atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/usage":
			usageCalls.Add(1)
		case "/v1/expiring-usage":
			expiringCalls.Add(1)
		default:
			t.Fatalf("unexpected daemon path %s", r.URL.Path)
		}
		writeMCPDaemonResponse(w, r)
	}))
	defer daemon.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		writeMCPProviderUsage(w)
	}))
	defer provider.Close()
	configPath, statePath := writeMCPPolicyConfig(t, daemon.URL, "require-daemon", provider.URL)

	responses, stderr := runMCPToolCalls(t, []string{"--config", configPath},
		map[string]any{"name": "usage", "arguments": map[string]any{"json": true}},
		map[string]any{"name": "tokens_to_burn", "arguments": map[string]any{"json": true}},
		map[string]any{"name": "expiring_usage", "arguments": map[string]any{"json": true}},
	)
	for i, response := range responses {
		if mcpToolIsError(response) {
			t.Fatalf("response %d unexpectedly failed: %v", i, response)
		}
	}
	if stderr != "" || usageCalls.Load() != 1 || expiringCalls.Load() != 2 || providerCalls.Load() != 0 {
		t.Fatalf("stderr=%q usage=%d expiring=%d provider=%d", stderr, usageCalls.Load(), expiringCalls.Load(), providerCalls.Load())
	}
	assertNoMCPLocalState(t, statePath)
}

func TestRequireDaemonUnreachableToolsHaveNoLocalSideEffects(t *testing.T) {
	var providerCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		writeMCPProviderUsage(w)
	}))
	defer provider.Close()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	origin := "http://127.0.0.1:1"
	configPath, statePath := writeMCPPolicyConfig(t, origin, "require-daemon", provider.URL)

	responses, stderr := runMCPToolCalls(t, []string{"--config", configPath, "--timeout", "100ms"},
		map[string]any{"name": "usage", "arguments": map[string]any{}},
		map[string]any{"name": "tokens_to_burn", "arguments": map[string]any{}},
	)
	for _, response := range responses {
		assertMCPToolError(t, response, "local fallback is forbidden")
		if !strings.Contains(mcpResponseText(response), origin) || !strings.Contains(mcpResponseText(response), "require-daemon") {
			t.Fatalf("response does not name selected origin and policy: %v", response)
		}
	}
	if stderr != "" || providerCalls.Load() != 0 {
		t.Fatalf("stderr=%q providerCalls=%d", stderr, providerCalls.Load())
	}
	assertNoMCPLocalState(t, statePath)
}

func TestPermittedPerToolOfflineBehaviorRemainsLocal(t *testing.T) {
	for _, mode := range []string{"prefer-daemon", "local-only"} {
		t.Run(mode, func(t *testing.T) {
			var daemonCalls, providerCalls atomic.Int32
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				daemonCalls.Add(1)
				writeMCPDaemonResponse(w, r)
			}))
			defer daemon.Close()
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerCalls.Add(1)
				writeMCPProviderUsage(w)
			}))
			defer provider.Close()
			configPath, _ := writeMCPPolicyConfig(t, daemon.URL, mode, provider.URL)

			responses, stderr := runMCPToolCalls(t, []string{"--config", configPath},
				map[string]any{"name": "usage", "arguments": map[string]any{"offline": true}},
				map[string]any{"name": "tokens_to_burn", "arguments": map[string]any{"offline": true}},
			)
			if len(responses) != 2 || mcpToolIsError(responses[0]) || mcpToolIsError(responses[1]) {
				t.Fatalf("responses=%v stderr=%q", responses, stderr)
			}
			if daemonCalls.Load() != 0 || providerCalls.Load() != 1 {
				t.Fatalf("daemonCalls=%d providerCalls=%d", daemonCalls.Load(), providerCalls.Load())
			}
		})
	}
}

func TestMCPReloadsPolicyForEveryToolCall(t *testing.T) {
	var providerCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		writeMCPProviderUsage(w)
	}))
	defer provider.Close()
	configPath, _ := writeMCPPolicyConfig(t, "http://127.0.0.1:1", "prefer-daemon", provider.URL)
	s := server{opts: Options{ConfigPath: configPath, Timeout: time.Second}}

	first, err := s.callUsage(context.Background(), map[string]any{"offline": true})
	if err != nil {
		t.Fatal(err)
	}
	firstResult, ok := first.(map[string]any)
	if !ok || firstResult["isError"] == true || providerCalls.Load() != 1 {
		t.Fatalf("first result=%v providerCalls=%d", first, providerCalls.Load())
	}
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(body), `mode: "prefer-daemon"`, `mode: "require-daemon"`, 1)
	if updated == string(body) {
		t.Fatal("failed to update client mode fixture")
	}
	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}

	second, err := s.callUsage(context.Background(), map[string]any{"offline": true})
	if err != nil {
		t.Fatal(err)
	}
	secondResult, ok := second.(map[string]any)
	if !ok || secondResult["isError"] != true || !strings.Contains(mcpResponseText(map[string]any{"result": secondResult}), "require-daemon") {
		t.Fatalf("second result=%v", second)
	}
	if providerCalls.Load() != 1 {
		t.Fatalf("provider called after policy changed: %d", providerCalls.Load())
	}
}

func TestMCPPolicyFlagsAndToolDescriptions(t *testing.T) {
	opts, err := parseFlags([]string{"--daemon-url", "https://daemon.example", "--host", "", "--port", "0"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.DaemonURLSet || !opts.HostSet || !opts.PortSet {
		t.Fatalf("raw flag presence not retained: %+v", opts)
	}
	server := server{opts: opts}
	joined := strings.Join(server.baseCLIArgs(nil), " ")
	for _, want := range []string{"--daemon-url https://daemon.example", "--host ", "--port 0"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("base args %q missing %q", joined, want)
		}
	}
	body, _ := json.Marshal(tools())
	for _, want := range []string{"require-daemon", "--daemon-url", "never falls back"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("tool descriptions missing %q: %s", want, body)
		}
	}
}

func writeDaemonConfig(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	text := fmt.Sprintf("server: {host: %q, port: %s, readAuth: {mode: none}}\nusageView: {providers: [chatgpt]}\n", u.Hostname(), u.Port())
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeMCPPolicyConfig(t *testing.T, clientURL, mode, providerURL string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state", "snapshot.json")
	path := filepath.Join(dir, "config.yaml")
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
	return path, statePath
}

func runMCPToolCalls(t *testing.T, args []string, calls ...map[string]any) ([]map[string]any, string) {
	t.Helper()
	var input strings.Builder
	for i, call := range calls {
		input.WriteString(frame(map[string]any{"jsonrpc": "2.0", "id": i + 1, "method": "tools/call", "params": call}))
	}
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), args, strings.NewReader(input.String()), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	return readJSONLResponses(t, stdout.String()), stderr.String()
}

func writeMCPDaemonResponse(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/usage":
		fmt.Fprint(w, `{"schemaVersion":2,"service":"usagent","generatedAt":1,"startedAt":1,"stale":false,"providers":[],"quotaItems":[]}`)
	case "/v1/expiring-usage":
		fmt.Fprint(w, `{"generatedAt":1,"opportunities":[]}`)
	default:
		http.NotFound(w, r)
	}
}

func writeMCPProviderUsage(w http.ResponseWriter) {
	fmt.Fprint(w, `{"items":[{"id":"alpha","label":"Alpha","window":{"id":"week","label":"W","kind":"weekly"},"unit":"tokens","limit":100,"used":25,"remaining":75,"percentUsed":25}]}`)
}

func assertMCPToolError(t *testing.T, response map[string]any, want string) {
	t.Helper()
	if !mcpToolIsError(response) || !strings.Contains(mcpResponseText(response), want) {
		t.Fatalf("response=%v, want tool error containing %q", response, want)
	}
}

func mcpToolIsError(response map[string]any) bool {
	result, ok := response["result"].(map[string]any)
	if !ok {
		return false
	}
	isError, _ := result["isError"].(bool)
	return isError
}

func mcpResponseText(response map[string]any) string {
	body, _ := json.Marshal(response)
	return string(body)
}

func assertNoMCPLocalState(t *testing.T, statePath string) {
	t.Helper()
	for _, path := range []string{statePath, statePath + ".refresh.lock", filepath.Join(filepath.Dir(statePath), "usage-history.json")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unexpected local state at %s (err=%v)", path, err)
		}
	}
}

func frame(v any) string {
	msg, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(msg), msg)
}

func readJSONLResponses(t *testing.T, raw string) []map[string]any {
	t.Helper()
	out := []map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("failed to unmarshal line %q: %v", line, err)
		}
		out = append(out, msg)
	}
	return out
}
