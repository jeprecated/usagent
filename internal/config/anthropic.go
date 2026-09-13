package config

import (
	"fmt"
	"math"
	"net"
	"net/url"
	"time"
)

const (
	DefaultAnthropicCostsEndpoint = "https://api.anthropic.com/v1/organizations/cost_report"
	MinAnthropicRefreshMs         = int64((5 * time.Minute) / time.Millisecond)
)

// AnthropicConfig tracks Console API credit estimates, not Claude subscriptions.
type AnthropicConfig struct {
	Enabled       bool                   `yaml:"enabled"`
	CostsEndpoint string                 `yaml:"costsEndpoint"`
	APIKeyEnv     string                 `yaml:"apiKeyEnv"`
	APIKeyFile    string                 `yaml:"apiKeyFile"`
	RefreshMs     int64                  `yaml:"refreshMs"`
	StaleMs       int64                  `yaml:"staleMs"`
	Checkpoint    *AnthropicCheckpoint   `yaml:"checkpoint"`
	Metadata      ProviderMetadataConfig `yaml:"metadata"`
}

// ReportedCostUSD is the Cost API total from ReportStart at the moment the
// balance was recorded. Keeping it explicit avoids counting that day's earlier
// spending twice, and makes the checkpoint independent of local cache state.
type AnthropicCheckpoint struct {
	BalanceUSD      *float64 `yaml:"balanceUSD"`
	ReportStart     string   `yaml:"reportStart"`
	ReportedCostUSD *float64 `yaml:"reportedCostUSD"`
}

func normalizeAnthropic(cfg AnthropicConfig, refreshMs int64) AnthropicConfig {
	if cfg.CostsEndpoint == "" {
		cfg.CostsEndpoint = DefaultAnthropicCostsEndpoint
	}
	if cfg.APIKeyEnv == "" {
		cfg.APIKeyEnv = "ANTHROPIC_ADMIN_KEY"
	}
	if cfg.RefreshMs <= 0 {
		cfg.RefreshMs = refreshMs
	}
	cfg.RefreshMs = max(cfg.RefreshMs, MinAnthropicRefreshMs)
	if cfg.StaleMs <= 0 {
		cfg.StaleMs = cfg.RefreshMs * 3
	}
	return cfg
}

func (cfg AnthropicConfig) Validate() error {
	c := cfg.Checkpoint
	if c == nil {
		return fmt.Errorf("providers.anthropic.checkpoint is required when enabled")
	}
	for _, field := range []struct {
		name  string
		value *float64
	}{
		{"balanceUSD", c.BalanceUSD}, {"reportedCostUSD", c.ReportedCostUSD},
	} {
		if field.value == nil || math.IsNaN(*field.value) || math.IsInf(*field.value, 0) || *field.value < 0 || *field.value > math.MaxFloat64/100 {
			return fmt.Errorf("providers.anthropic.checkpoint.%s must be an explicit finite non-negative USD amount", field.name)
		}
	}
	start, err := time.Parse(time.RFC3339, c.ReportStart)
	if err != nil || !start.Equal(start.UTC().Truncate(24*time.Hour)) {
		return fmt.Errorf("providers.anthropic.checkpoint.reportStart must be an RFC3339 timestamp at UTC midnight")
	}
	if cfg.CostsEndpoint != "" {
		u, err := url.Parse(cfg.CostsEndpoint)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return fmt.Errorf("providers.anthropic.costsEndpoint must be an absolute URL without credentials, query, or fragment")
		}
		loopback := u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback()
		if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
			return fmt.Errorf("providers.anthropic.costsEndpoint requires HTTPS (HTTP allowed only on loopback for tests/proxies)")
		}
	}
	return nil
}
