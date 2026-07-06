package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultStateBase = "~/.local/state"

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Providers ProvidersConfig `yaml:"providers"`
	UsageView UsageViewConfig `yaml:"usageView"`
	Quota     QuotaConfig     `yaml:"quota"`
}

type ServerConfig struct {
	Host      string   `yaml:"host"`
	Port      int      `yaml:"port"`
	ReadAuth  ReadAuth `yaml:"readAuth"`
	StatePath string   `yaml:"statePath"`
}

type ReadAuth struct {
	Mode string `yaml:"mode"`
}

type ProvidersConfig struct {
	ClaudeOAuth ClaudeOAuthConfig `yaml:"claudeOAuth"`
}

type ClaudeOAuthConfig struct {
	Enabled         bool   `yaml:"enabled"`
	CredentialsPath string `yaml:"credentialsPath"`
	EndpointURL     string `yaml:"endpointUrl"`
	BetaHeader      string `yaml:"betaHeader"`
	RefreshMs       int64  `yaml:"refreshMs"`
	StaleMs         int64  `yaml:"staleMs"`
}

type UsageViewConfig struct {
	Providers        []string `yaml:"providers"`
	Windows          []string `yaml:"windows"`
	PercentMode      string   `yaml:"percentMode"`
	LabelStyle       string   `yaml:"labelStyle"`
	ShowResetMinutes bool     `yaml:"showResetMinutes"`
	Show24hUsage     bool     `yaml:"show24hUsage"`
	HideUnavailable  bool     `yaml:"hideUnavailable"`
	ExcludeWindows   []string `yaml:"excludeWindows"`
}

type QuotaConfig struct {
	RefreshMs int64           `yaml:"refreshMs"`
	Providers []QuotaProvider `yaml:"providers"`
}

type QuotaProvider struct {
	ID                     string   `yaml:"id"`
	Label                  string   `yaml:"label"`
	Source                 string   `yaml:"source"`
	StaleMs                int64    `yaml:"staleMs"`
	RefreshMs              int64    `yaml:"refreshMs"`
	HighlightedBucket      string   `yaml:"highlightedBucket"`
	HighlightedBucketLabel string   `yaml:"highlightedBucketLabel"`
	EndpointURL            string   `yaml:"endpointUrl"`
	APIKeyEnv              string   `yaml:"apiKeyEnv"`
	TokenEnv               string   `yaml:"tokenEnv"`
	Budgets                []Budget `yaml:"budgets"`
}

type Budget struct {
	ID     string         `yaml:"id"`
	Unit   string         `yaml:"unit"`
	Limit  float64        `yaml:"limit"`
	Window map[string]any `yaml:"window"`
}

type CLIOptions struct {
	ConfigPath string
	Host       string
	Port       int
}

func Default() Config {
	return Config{
		Server:    ServerConfig{Host: "127.0.0.1", Port: 8787, ReadAuth: ReadAuth{Mode: "none"}, StatePath: "%STATE%/usagent/snapshot.json"},
		Providers: ProvidersConfig{ClaudeOAuth: ClaudeOAuthConfig{Enabled: false, CredentialsPath: "~/.claude/.credentials.json", EndpointURL: "https://api.anthropic.com/api/oauth/usage", BetaHeader: "oauth-2025-04-20"}},
		UsageView: UsageViewConfig{Providers: []string{"claude-code", "openai", "z-ai"}},
		Quota:     QuotaConfig{RefreshMs: int64((5 * time.Minute) / time.Millisecond)},
	}
}

func ParseFlags(args []string) (CLIOptions, error) {
	fs := flag.NewFlagSet("usagent", flag.ContinueOnError)
	opts := CLIOptions{ConfigPath: getenv("USAGENT_CONFIG", "config.example.yaml")}
	fs.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "path to YAML config file")
	fs.StringVar(&opts.Host, "host", os.Getenv("USAGENT_HOST"), "listen host override")
	fs.IntVar(&opts.Port, "port", envInt("USAGENT_PORT", 0), "listen port override")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	return opts, nil
}

func Load(path string) (Config, error) {
	cfg := Default()
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	return Normalize(cfg)
}

func LoadWithOverrides(path string, opts CLIOptions) (Config, error) {
	cfg, err := Load(path)
	if err != nil {
		return cfg, err
	}
	if opts.Host != "" {
		cfg.Server.Host = opts.Host
	}
	if opts.Port != 0 {
		cfg.Server.Port = opts.Port
	}
	return Normalize(cfg)
}

func Normalize(cfg Config) (Config, error) {
	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}
	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		return cfg, fmt.Errorf("server.port must be 1..65535")
	}
	if cfg.Server.ReadAuth.Mode == "" {
		cfg.Server.ReadAuth.Mode = "none"
	}
	if cfg.Server.ReadAuth.Mode != "none" {
		return cfg, errors.New("only readAuth.mode=none is supported")
	}
	if len(cfg.UsageView.Providers) == 0 {
		cfg.UsageView.Providers = []string{"claude-code", "openai", "z-ai"}
	}
	if cfg.Quota.RefreshMs <= 0 {
		cfg.Quota.RefreshMs = int64((5 * time.Minute) / time.Millisecond)
	}
	if cfg.Providers.ClaudeOAuth.CredentialsPath == "" {
		cfg.Providers.ClaudeOAuth.CredentialsPath = "~/.claude/.credentials.json"
	}
	if cfg.Providers.ClaudeOAuth.EndpointURL == "" {
		cfg.Providers.ClaudeOAuth.EndpointURL = "https://api.anthropic.com/api/oauth/usage"
	}
	if cfg.Providers.ClaudeOAuth.BetaHeader == "" {
		cfg.Providers.ClaudeOAuth.BetaHeader = "oauth-2025-04-20"
	}
	if cfg.Providers.ClaudeOAuth.RefreshMs <= 0 {
		cfg.Providers.ClaudeOAuth.RefreshMs = cfg.Quota.RefreshMs
	}
	if cfg.Providers.ClaudeOAuth.StaleMs <= 0 {
		cfg.Providers.ClaudeOAuth.StaleMs = max(cfg.Providers.ClaudeOAuth.RefreshMs*3, int64((15*time.Minute)/time.Millisecond))
	}
	if cfg.Server.StatePath == "" {
		cfg.Server.StatePath = "%STATE%/usagent/snapshot.json"
	}
	var err error
	cfg.Server.StatePath, err = ExpandRuntimePath(cfg.Server.StatePath)
	if err != nil {
		return cfg, err
	}
	cfg.Providers.ClaudeOAuth.CredentialsPath, err = ExpandRuntimePath(cfg.Providers.ClaudeOAuth.CredentialsPath)
	if err != nil {
		return cfg, err
	}
	return cfg, nil
}

func ExpandRuntimePath(p string) (string, error) {
	if p == "" {
		return p, nil
	}
	stateBase := os.Getenv("XDG_STATE_HOME")
	if stateBase == "" {
		stateBase = filepath.Join(homeDir(), ".local", "state")
	}
	p = strings.ReplaceAll(p, "%STATE%", stateBase)
	if p == "~" {
		return homeDir(), nil
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(homeDir(), p[2:]), nil
	}
	return p, nil
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func envInt(k string, d int) int {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return d
	}
	return n
}
func homeDir() string {
	h, err := os.UserHomeDir()
	if err == nil && h != "" {
		return h
	}
	return "/"
}
