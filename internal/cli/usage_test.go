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

	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/model"
)

func TestFormatUsagePrintsRemainingByConfiguredProvider(t *testing.T) {
	usage := model.Usage{Providers: []model.Provider{{ID: "claude-code", Label: "Claude", State: model.ProviderStateFresh}, {ID: "openai", Label: "OpenAI", State: model.ProviderStateStale}, {ID: "z-ai", Label: "z.ai", State: model.ProviderStateFresh}}, QuotaItems: []model.QuotaItem{
		{ID: "claude-session", Provider: "claude-code", Label: "Claude session", Window: model.Window{ID: "session", Label: "S", Kind: "rolling"}, Unit: "percent", Remaining: 88, State: "fresh", Visible: true},
		{ID: "openai-week", Provider: "openai", Label: "OpenAI week", Window: model.Window{ID: "week", Label: "W", Kind: "weekly"}, Unit: "usd", Remaining: 42.5, State: "stale", Visible: true},
		{ID: "zai-week", Provider: "z-ai", Label: "z.ai week", Window: model.Window{ID: "week", Label: "W", Kind: "weekly"}, Unit: "M tokens", Limit: 6, Used: 3.06, Remaining: 2.94, PercentUsed: 51, State: "fresh", Visible: true},
	}}
	got := FormatUsage(usage)
	want := "Usage\nremaining quota by provider\n\nPROVIDER  QUOTA    WINDOW            REMAINING  STATUS\nClaude    session  S                       88%\nOpenAI    week     W                    $42.50  stale\nz.ai      week     W       2.94 M tokens (49%)\n"
	if got != want {
		t.Fatalf("FormatUsage()=\n%s\nwant=\n%s", got, want)
	}
}

func TestFormatUsageIncludesUnavailableProviderError(t *testing.T) {
	usage := model.Usage{Providers: []model.Provider{{ID: "openai", Label: "OpenAI", State: model.ProviderStateError, Error: &model.ItemError{Message: "openai costs returned HTTP 500"}}}}
	got := FormatUsage(usage)
	want := "Usage\nremaining quota by provider\n\nPROVIDER  QUOTA        WINDOW  REMAINING  STATUS\nOpenAI    unavailable  -               -  error · openai costs returned HTTP 500\n"
	if got != want {
		t.Fatalf("FormatUsage()=%q want %q", got, want)
	}
}

func TestFormatUsageAppendsStalenessAge(t *testing.T) {
	const now int64 = 1_784_972_820_779 // 2026-07-25 11:47:00
	day := int64(24 * 60 * 60 * 1000)
	// Items are sorted by FormatUsage as (windowID, id), so the expected line
	// below is ordered: ancient < resetCredits < weekly(primary, secondary).
	usage := model.Usage{GeneratedAt: now, Providers: []model.Provider{{ID: "chatgpt", Label: "ChatGPT Pro", State: model.ProviderStateStale}}, QuotaItems: []model.QuotaItem{
		{ID: "chatgpt-primary", Provider: "chatgpt", Label: "ChatGPT 5h", Window: model.Window{ID: "weekly", Label: "W", Kind: "weekly"}, Unit: "percent", Limit: 100, Remaining: 27, State: "stale", Visible: true, Refresh: &model.Refresh{LastUpdatedAt: now - 58*60*1000}},
		{ID: "chatgpt-secondary", Provider: "chatgpt", Label: "ChatGPT weekly", Window: model.Window{ID: "weekly", Label: "W", Kind: "weekly"}, Unit: "percent", Limit: 100, Remaining: 100, State: "stale", Visible: true, Refresh: &model.Refresh{LastUpdatedAt: now - 18*60*60*1000}},
		{ID: "chatgpt-never", Provider: "chatgpt", Label: "ChatGPT resets", Window: model.Window{ID: "resetCredits", Label: "Resets", Kind: "credit"}, Unit: "credits", Limit: 2, Remaining: 2, State: "stale", Visible: true},
		{ID: "chatgpt-ancient", Provider: "chatgpt", Label: "ChatGPT ancient", Window: model.Window{ID: "ancient", Label: "A", Kind: "weekly"}, Unit: "percent", Limit: 100, Remaining: 50, State: "stale", Visible: true, Refresh: &model.Refresh{LastUpdatedAt: now - 3*day}},
	}}
	got := FormatUsage(usage)
	want := "Usage\nremaining quota by provider\n\nPROVIDER     QUOTA    WINDOW         REMAINING  STATUS\nChatGPT Pro  ancient  A                    50%  stale 3d\n             resets   Resets  2 credits (100%)  stale\n             5h       W                    27%  stale 58m\n             weekly   W                   100%  stale 18h\n"
	if got != want {
		t.Fatalf("FormatUsage()=\n%s\nwant=\n%s", got, want)
	}
}

func TestFormatUsageWithColor(t *testing.T) {
	usage := model.Usage{Providers: []model.Provider{{ID: "claude", Label: "Claude", State: model.ProviderStateFresh}}, QuotaItems: []model.QuotaItem{
		{ID: "low", Provider: "claude", Label: "Claude low", Window: model.Window{Label: "S"}, Unit: "percent", Limit: 100, Remaining: 10, State: "fresh", Visible: true},
		{ID: "high", Provider: "claude", Label: "Claude high", Window: model.Window{Label: "W"}, Unit: "percent", Limit: 100, Remaining: 80, State: "fresh", Visible: true},
	}}
	got := FormatUsageWithColor(usage, true)
	for _, want := range []string{"\x1b[", ansiRed, ansiGreen, "PROVIDER", "low", "high"} {
		if !strings.Contains(got, want) {
			t.Fatalf("colored usage missing %q: %q", want, got)
		}
	}
}

func TestAcquireLocalRefreshLockReturnsUnlockedWhenBusy(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "snapshot.json")
	unlock, locked, err := acquireLocalRefreshLock(context.Background(), statePath)
	if err != nil || !locked {
		t.Fatalf("first lock locked=%v err=%v", locked, err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, locked, err = acquireLocalRefreshLock(ctx, statePath)
	if err != nil {
		t.Fatalf("second lock err=%v", err)
	}
	if locked {
		t.Fatal("second lock should not be acquired while first is held")
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

func TestRunUsageTriesUserDaemonConfigBeforeLocalFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/usage" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		fmt.Fprint(w, `{"schemaVersion":2,"service":"usagent","generatedAt":1,"startedAt":1,"stale":false,"providers":[{"id":"daemon","label":"Daemon","state":"fresh","source":"pull"}],"quotaItems":[{"id":"daemon-session","provider":"daemon","label":"Daemon session","window":{"id":"session","label":"S","kind":"rolling"},"unit":"percent","limit":100,"used":1,"remaining":99,"percentUsed":1,"state":"fresh","severity":"ok","visible":true}]}`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	userConfigPath := filepath.Join(dir, "usagent", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(userConfigPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userConfigPath, []byte(fmt.Sprintf(`server: {host: "127.0.0.1", port: 1, readAuth: {mode: none}}
client: {url: %q, mode: local-only}
usageView: {providers: [daemon]}
`, srv.URL)), 0o644); err != nil {
		t.Fatal(err)
	}
	primaryConfigPath := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(primaryConfigPath, []byte(`server: {host: "127.0.0.1", port: 1, readAuth: {mode: none}}
usageView: {providers: [bad]}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := RunUsage(context.Background(), []string{"--config", primaryConfigPath, "--timeout", "2s"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); !strings.Contains(got, "Daemon") || !strings.Contains(got, "99%") {
		t.Fatalf("stdout=%q stderr=%q", got, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestRunUsageFallsBackToLocalRefreshWhenDaemonUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[{"id":"alpha","label":"Alpha","window":{"id":"week","label":"W","kind":"weekly"},"unit":"tokens","limit":100,"used":25,"remaining":75,"percentUsed":25}]}`)
	}))
	defer srv.Close()
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
	if got := stdout.String(); !strings.Contains(got, "Mine") || !strings.Contains(got, "75 tokens (75%)") {
		t.Fatalf("stdout=%q stderr=%q", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "daemon unavailable") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}
