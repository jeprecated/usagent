package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jmalloc/usagent/internal/config"
	"github.com/jmalloc/usagent/internal/model"
)

func TestFormatUsagePrintsRemainingByConfiguredProvider(t *testing.T) {
	usage := model.Usage{Providers: []model.Provider{{ID: "claude-code", Label: "Claude", State: model.ProviderStateFresh}, {ID: "openai", Label: "OpenAI", State: model.ProviderStateStale}, {ID: "z-ai", Label: "z.ai", State: model.ProviderStateError}}, QuotaItems: []model.QuotaItem{
		{ID: "claude-session", Provider: "claude-code", Label: "Claude session", Window: model.Window{ID: "session", Label: "S", Kind: "rolling"}, Unit: "percent", Remaining: 88, State: "fresh", Visible: true},
		{ID: "openai-week", Provider: "openai", Label: "OpenAI week", Window: model.Window{ID: "week", Label: "W", Kind: "weekly"}, Unit: "usd", Remaining: 42.5, State: "stale", Visible: true},
	}}
	got := FormatUsage(usage)
	want := "Claude: S 88% remaining\nOpenAI: W $42.50 remaining [stale]\nz.ai: unavailable [error]\n"
	if got != want {
		t.Fatalf("FormatUsage()=\n%s\nwant=\n%s", got, want)
	}
}

func TestFormatUsageIncludesUnavailableProviderError(t *testing.T) {
	usage := model.Usage{Providers: []model.Provider{{ID: "openai", Label: "OpenAI", State: model.ProviderStateError, Error: &model.ItemError{Message: "openai costs returned HTTP 500"}}}}
	got := FormatUsage(usage)
	want := "OpenAI: unavailable [error: openai costs returned HTTP 500]\n"
	if got != want {
		t.Fatalf("FormatUsage()=%q want %q", got, want)
	}
}

func TestFetchUsageFromDaemon(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/usage" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		fmt.Fprint(w, `{"schemaVersion":2,"service":"usagent","generatedAt":1,"startedAt":1,"stale":false,"providers":[],"quotaItems":[]}`)
	}))
	defer srv.Close()
	host, port := splitServer(t, srv.URL)
	usage, err := FetchUsageFromDaemon(context.Background(), testConfig(t, host, port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if usage.SchemaVersion != 2 || usage.Service != "usagent" {
		t.Fatalf("usage=%+v", usage)
	}
}

func testConfig(t *testing.T, host string, port int) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Server.Host = host
	cfg.Server.Port = port
	return cfg
}

func splitServer(t *testing.T, rawURL string) (string, int) {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	host := u.Hostname()
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}

func TestRunUsageFallsBackToLocalRefreshWhenDaemonUnavailable(t *testing.T) {
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
	err := RunUsage(context.Background(), []string{"--config", configPath, "--timeout", "2s"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "Mine: W 75 tokens remaining\n" {
		t.Fatalf("stdout=%q stderr=%q", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "daemon unavailable") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}
