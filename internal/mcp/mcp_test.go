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
	"testing"
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
