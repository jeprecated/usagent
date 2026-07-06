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
