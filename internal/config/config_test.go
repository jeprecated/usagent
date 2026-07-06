package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadParsesExampleYAML(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("Load config.example.yaml: %v", err)
	}
	if cfg.Server.Port != 8787 {
		t.Fatalf("port=%d", cfg.Server.Port)
	}
	if cfg.Providers.ClaudeOAuth.EndpointURL == "" {
		t.Fatal("missing claude endpoint")
	}
	if got := cfg.UsageView.Providers; len(got) != 3 || got[0] != "claude-code" {
		t.Fatalf("providers=%v", got)
	}
	if cfg.Providers.OpenAI.APIKeyEnv != "OPENAI_ADMIN_KEY" || len(cfg.Providers.OpenAI.Budgets) != 2 {
		t.Fatalf("openai=%+v", cfg.Providers.OpenAI)
	}
	if cfg.Providers.ZAI.EndpointURL == "" || cfg.Providers.ZAI.TokenEnv != "ZAI_API_KEY" {
		t.Fatalf("zai=%+v", cfg.Providers.ZAI)
	}
}

func TestLoadParsesCustomProviderMapping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `server: {host: "127.0.0.1", port: 8787, readAuth: {mode: "none"}}
providers:
  custom:
    - id: "mine"
      label: "Mine"
      enabled: true
      endpoints:
        - id: "quota"
          url: "https://example.test/quota"
          auth: {type: "bearer", tokenEnv: "MINE_TOKEN"}
          itemsPath: "items"
          item:
            id: "id"
            label: "literal:Mine quota"
            window: {id: "window.id", label: "window.label", kind: "window.kind", resetAt: "resetAt", resetAtFormat: "unixMs"}
            unit: "unit"
            limit: "limit"
            used: "used"
            visible: true
usageView: {providers: ["mine"]}
quota: {refreshMs: 1234}
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers.Custom) != 1 || cfg.Providers.Custom[0].ID != "mine" || cfg.Providers.Custom[0].RefreshMs != 1234 || cfg.Providers.Custom[0].StaleMs <= 0 {
		t.Fatalf("custom=%+v", cfg.Providers.Custom)
	}
	item := cfg.Providers.Custom[0].Endpoints[0].Item
	if item.Label != "literal:Mine quota" || item.Window.ResetAtFormat != "unixMs" || item.Visible != true {
		t.Fatalf("item mapping=%+v", item)
	}
}

func TestExpandRuntimePath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state")
	got, err := ExpandRuntimePath("%STATE%/usagent/snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/xdg-state/usagent/snapshot.json" {
		t.Fatalf("got %q", got)
	}
	home, _ := os.UserHomeDir()
	got, err = ExpandRuntimePath("~/.claude/.credentials.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, home) || strings.Contains(got, "~") {
		t.Fatalf("home not expanded: %q", got)
	}
}

func TestDefaultConfigPathUsesXDGConfigBeforeExample(t *testing.T) {
	t.Setenv("USAGENT_CONFIG", "")
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "usagent", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("server: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DefaultConfigPath(); got != path {
		t.Fatalf("DefaultConfigPath()=%q want %q", got, path)
	}
}

func TestNormalizeClampsBuiltInProviderRefreshIntervals(t *testing.T) {
	cfg := Default()
	cfg.Providers.ClaudeOAuth.Enabled = true
	cfg.Providers.ClaudeOAuth.RefreshMs = 1000
	cfg.Providers.OpenAI.Enabled = true
	cfg.Providers.OpenAI.RefreshMs = 1000
	cfg.Providers.ZAI.Enabled = true
	cfg.Providers.ZAI.RefreshMs = 1000
	cfg, err := Normalize(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers.ClaudeOAuth.RefreshMs != MinClaudeOAuthRefreshMs || cfg.Providers.OpenAI.RefreshMs != MinOpenAIRefreshMs || cfg.Providers.ZAI.RefreshMs != MinZAIRefreshMs {
		t.Fatalf("refreshes claude=%d openai=%d zai=%d", cfg.Providers.ClaudeOAuth.RefreshMs, cfg.Providers.OpenAI.RefreshMs, cfg.Providers.ZAI.RefreshMs)
	}
}

func TestParseFlagsAndEnvOverrides(t *testing.T) {
	t.Setenv("USAGENT_CONFIG", "env.yaml")
	t.Setenv("USAGENT_HOST", "0.0.0.0")
	t.Setenv("USAGENT_PORT", "9999")
	opts, err := ParseFlags([]string{"--config", "flag.yaml", "--port", "8888"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.ConfigPath != "flag.yaml" || opts.Host != "0.0.0.0" || opts.Port != 8888 {
		t.Fatalf("opts=%+v", opts)
	}
}
