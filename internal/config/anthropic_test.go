package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAnthropicConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`providers:
  anthropic:
    enabled: true
    apiKeyFile: "~/.config/usagent/anthropic-admin-key"
    refreshMs: 1
    checkpoint:
      balanceUSD: 75.25
      reportStart: "2026-05-01T00:00:00Z"
      reportedCostUSD: 1.23456
    metadata:
      tags: [api, api]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Providers.Anthropic
	if !a.Enabled || a.APIKeyEnv != "ANTHROPIC_ADMIN_KEY" || a.CostsEndpoint != DefaultAnthropicCostsEndpoint || a.RefreshMs != MinAnthropicRefreshMs || a.StaleMs != a.RefreshMs*3 {
		t.Fatalf("config=%+v", a)
	}
	home, _ := os.UserHomeDir()
	if a.APIKeyFile != filepath.Join(home, ".config/usagent/anthropic-admin-key") || *a.Checkpoint.BalanceUSD != 75.25 || *a.Checkpoint.ReportedCostUSD != 1.23456 || len(a.Metadata.Tags) != 1 {
		t.Fatalf("config=%+v checkpoint=%+v", a, a.Checkpoint)
	}
}

func TestAnthropicDisabledHasNoCheckpointDefault(t *testing.T) {
	cfg, err := Normalize(Default())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers.Anthropic.Enabled || cfg.Providers.Anthropic.Checkpoint != nil {
		t.Fatal("must not enable or invent a balance checkpoint")
	}
	cfg.Providers.Anthropic.Enabled = true
	if _, err := Normalize(cfg); err == nil {
		t.Fatal("enabled without checkpoint")
	}
}

func TestValidateAnthropicCheckpoint(t *testing.T) {
	ptr := func(v float64) *float64 { return &v }
	valid := func() AnthropicConfig {
		return AnthropicConfig{Checkpoint: &AnthropicCheckpoint{BalanceUSD: ptr(0), ReportedCostUSD: ptr(0), ReportStart: "2026-05-01T00:00:00Z"}}
	}
	if err := valid().Validate(); err != nil {
		t.Fatalf("explicit zero must be valid: %v", err)
	}
	for _, field := range []string{"balanceUSD", "reportedCostUSD"} {
		for _, value := range []*float64{nil, ptr(-1), ptr(math.NaN()), ptr(math.Inf(1)), ptr(math.MaxFloat64)} {
			cfg := valid()
			if field == "balanceUSD" {
				cfg.Checkpoint.BalanceUSD = value
			} else {
				cfg.Checkpoint.ReportedCostUSD = value
			}
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("field=%s value=%v err=%v", field, value, err)
			}
		}
	}
	for _, date := range []string{"", "2026-05-01", "2026-05-01T12:00:00Z", "2026-05-01T00:00:00+02:00", "2026-05-01T00:00:00.1Z"} {
		cfg := valid()
		cfg.Checkpoint.ReportStart = date
		if err := cfg.Validate(); err == nil {
			t.Fatalf("accepted date %q", date)
		}
	}
	for _, endpoint := range []string{"http://remote.example/costs", "file:///costs", "https://user:pass@example.com", "https://example.com?workspace=one", "https://example.com#fragment", "relative"} {
		cfg := valid()
		cfg.CostsEndpoint = endpoint
		if err := cfg.Validate(); err == nil {
			t.Fatalf("accepted endpoint %q", endpoint)
		}
	}
	for _, endpoint := range []string{DefaultAnthropicCostsEndpoint, "http://127.0.0.1:1234/costs", "http://[::1]:1234/costs"} {
		cfg := valid()
		cfg.CostsEndpoint = endpoint
		if err := cfg.Validate(); err != nil {
			t.Fatalf("endpoint=%s: %v", endpoint, err)
		}
	}
}
