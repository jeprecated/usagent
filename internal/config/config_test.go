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
	if cfg.Client.Mode != ClientModePreferDaemon || cfg.Client.URL != "" {
		t.Fatalf("client=%+v", cfg.Client)
	}
	if cfg.Providers.ClaudeOAuth.EndpointURL == "" || cfg.Providers.ClaudeOAuth.UserAgent == "" {
		t.Fatal("missing claude endpoint/user-agent")
	}
	if got := cfg.UsageView.Providers; len(got) != 3 || got[0] != "claude-code" || got[1] != "chatgpt" {
		t.Fatalf("providers=%v", got)
	}
	if cfg.Providers.ChatGPT.AuthPath == "" || cfg.Providers.ChatGPT.EndpointURL == "" || cfg.Providers.ChatGPT.TokenEnv != "CHATGPT_ACCESS_TOKEN" {
		t.Fatalf("chatgpt=%+v", cfg.Providers.ChatGPT)
	}
	if cfg.Providers.OpenAI.APIKeyEnv != "OPENAI_ADMIN_KEY" || len(cfg.Providers.OpenAI.Budgets) != 2 {
		t.Fatalf("openai=%+v", cfg.Providers.OpenAI)
	}
	if cfg.Providers.ZAI.EndpointURL == "" || cfg.Providers.ZAI.TokenEnv != "ZAI_API_KEY" {
		t.Fatalf("zai=%+v", cfg.Providers.ZAI)
	}
}

func TestNormalizeClientModesAndURL(t *testing.T) {
	for _, mode := range []ClientMode{ClientModePreferDaemon, ClientModeRequireDaemon, ClientModeLocalOnly} {
		t.Run(string(mode), func(t *testing.T) {
			cfg := Default()
			cfg.Client = ClientConfig{Mode: mode, URL: "https://lattice.example:8788/"}
			got, err := Normalize(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got.Client.Mode != mode || got.Client.URL != "https://lattice.example:8788" {
				t.Fatalf("client=%+v", got.Client)
			}
		})
	}

	cfg := Default()
	cfg.Client = ClientConfig{}
	got, err := Normalize(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Client.Mode != ClientModePreferDaemon || got.Client.URL != "" {
		t.Fatalf("default client=%+v", got.Client)
	}

	cfg.Client.Mode = "hybrid"
	if _, err := Normalize(cfg); err == nil || !strings.Contains(err.Error(), "client.mode") {
		t.Fatalf("invalid mode error=%v", err)
	}
}

func TestNormalizeClientURLValidation(t *testing.T) {
	valid := map[string]string{
		"http://127.0.0.1:8787": "http://127.0.0.1:8787",
		"https://lattice:8788/": "https://lattice:8788",
		"http://[::1]:8788/":    "http://[::1]:8788",
	}
	for input, want := range valid {
		t.Run("valid_"+strings.ReplaceAll(input, "/", "_"), func(t *testing.T) {
			got, err := NormalizeClientURL(input)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("NormalizeClientURL(%q)=%q want %q", input, got, want)
			}
		})
	}

	invalid := []string{
		"lattice:8788",
		"ftp://lattice:8788",
		"http://",
		"http:///missing-host",
		"http://user:secret@lattice:8788",
		"http://lattice:8788?view=usage",
		"http://lattice:8788?",
		"http://lattice:8788#usage",
		"http://lattice:8788#",
		"http://lattice:8788/v1",
	}
	for _, input := range invalid {
		t.Run("invalid_"+strings.ReplaceAll(input, "/", "_"), func(t *testing.T) {
			if got, err := NormalizeClientURL(input); err == nil {
				t.Fatalf("NormalizeClientURL(%q)=%q, want error", input, got)
			}
		})
	}
}

func TestNormalizeClientURLIsIdempotent(t *testing.T) {
	cfg := Default()
	cfg.Client.URL = "https://[::1]:8788/"
	first, err := Normalize(cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Normalize(first)
	if err != nil {
		t.Fatal(err)
	}
	if first.Client != second.Client {
		t.Fatalf("first=%+v second=%+v", first.Client, second.Client)
	}
}

func TestLoadParsesProviderAndModelMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `server: {host: "127.0.0.1", port: 8787, readAuth: {mode: "none"}}
providers:
  chatgpt:
    metadata:
      tier: "high"
      tags: ["chat", "subscription", "chat"]
      models:
        chatgpt-primary:
          tier: "extra-high"
          tags: ["codex", "fast"]
  custom:
    - id: "mine"
      label: "Mine"
      enabled: true
      metadata:
        tier: "medium"
        tags: ["local", "custom"]
usageView: {providers: [chatgpt, mine, unknown]}
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers.ChatGPT.Metadata.Tier != "high" || len(cfg.Providers.ChatGPT.Metadata.Tags) != 2 {
		t.Fatalf("chatgpt metadata=%+v", cfg.Providers.ChatGPT.Metadata)
	}
	modelMetadata := cfg.Providers.ChatGPT.Metadata.Models["chatgpt-primary"]
	if modelMetadata.Tier != "extra-high" || len(modelMetadata.Tags) != 2 || modelMetadata.Tags[0] != "codex" {
		t.Fatalf("model metadata=%+v", modelMetadata)
	}
	if cfg.Providers.Custom[0].Metadata.Tier != "medium" || cfg.Providers.Custom[0].Metadata.Tags[1] != "custom" {
		t.Fatalf("custom metadata=%+v", cfg.Providers.Custom[0].Metadata)
	}
	if cfg.Providers.ClaudeOAuth.Metadata.Tier != "" || len(cfg.Providers.ClaudeOAuth.Metadata.Tags) != 0 {
		t.Fatalf("defaults should not hard-code metadata: %+v", cfg.Providers.ClaudeOAuth.Metadata)
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
