package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	DefaultCLIProxyBaseURL = "http://127.0.0.1:8317"
	MinCLIProxyRefreshMs   = int64((5 * time.Minute) / time.Millisecond)
)

// CLIProxyConfig reads every enrolled CLIProxyAPI account through its management
// API. CLIProxyAPI attaches (and refreshes) each account's own token, so usagent
// never handles those OAuth credentials.
type CLIProxyConfig struct {
	Enabled bool `yaml:"enabled"`
	// BaseURL is the CLIProxyAPI origin; management routes live under /v8/management.
	BaseURL          string `yaml:"baseUrl"`
	ManagementKeyEnv string `yaml:"managementKeyEnv"`
	// ManagementKeyFile is read at refresh time and takes precedence over the env var.
	ManagementKeyFile string                 `yaml:"managementKeyFile"`
	RefreshMs         int64                  `yaml:"refreshMs"`
	StaleMs           int64                  `yaml:"staleMs"`
	Metadata          ProviderMetadataConfig `yaml:"metadata"`
}

func normalizeCLIProxy(cfg CLIProxyConfig, refreshMs int64) (CLIProxyConfig, error) {
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultCLIProxyBaseURL
	}
	if cfg.ManagementKeyEnv == "" {
		cfg.ManagementKeyEnv = "CLIPROXY_MANAGEMENT_KEY"
	}
	if cfg.RefreshMs <= 0 {
		cfg.RefreshMs = refreshMs
	}
	cfg.RefreshMs = max(cfg.RefreshMs, MinCLIProxyRefreshMs)
	if cfg.StaleMs <= 0 {
		cfg.StaleMs = max(cfg.RefreshMs*3, int64((15*time.Minute)/time.Millisecond))
	}
	var err error
	// Home Manager agenix secrets live under ${XDG_RUNTIME_DIR}.
	cfg.ManagementKeyFile, err = ExpandRuntimePath(os.ExpandEnv(cfg.ManagementKeyFile))
	if err != nil {
		return cfg, err
	}
	if !cfg.Enabled {
		return cfg, nil
	}
	// The management key also reads every client key, so never send it in clear
	// beyond loopback.
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return cfg, fmt.Errorf("providers.cliproxy.baseUrl must be an origin such as %q", DefaultCLIProxyBaseURL)
	}
	loopback := u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return cfg, fmt.Errorf("providers.cliproxy.baseUrl requires HTTPS (HTTP allowed only on loopback)")
	}
	return cfg, nil
}
