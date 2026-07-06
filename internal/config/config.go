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

const (
	MinClaudeOAuthRefreshMs = int64((5 * time.Minute) / time.Millisecond)
	MinOpenAIRefreshMs      = int64((10 * time.Minute) / time.Millisecond)
	MinZAIRefreshMs         = int64((5 * time.Minute) / time.Millisecond)
)

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
	ClaudeOAuth ClaudeOAuthConfig      `yaml:"claudeOAuth"`
	OpenAI      OpenAIConfig           `yaml:"openai"`
	ZAI         ZAIConfig              `yaml:"zAi"`
	Custom      []CustomProviderConfig `yaml:"custom"`
}

type ClaudeOAuthConfig struct {
	Enabled         bool   `yaml:"enabled"`
	CredentialsPath string `yaml:"credentialsPath"`
	EndpointURL     string `yaml:"endpointUrl"`
	BetaHeader      string `yaml:"betaHeader"`
	UserAgent       string `yaml:"userAgent"`
	RefreshMs       int64  `yaml:"refreshMs"`
	StaleMs         int64  `yaml:"staleMs"`
}

type OpenAIConfig struct {
	Enabled       bool     `yaml:"enabled"`
	BaseURL       string   `yaml:"baseUrl"`
	CostsEndpoint string   `yaml:"costsEndpoint"`
	APIKeyEnv     string   `yaml:"apiKeyEnv"`
	RefreshMs     int64    `yaml:"refreshMs"`
	StaleMs       int64    `yaml:"staleMs"`
	Budgets       []Budget `yaml:"budgets"`
	ProjectIDs    []string `yaml:"projectIds"`
	APIKeyIDs     []string `yaml:"apiKeyIds"`
	GroupBy       []string `yaml:"groupBy"`
}

type ZAIConfig struct {
	Enabled           bool     `yaml:"enabled"`
	EndpointURL       string   `yaml:"endpointUrl"`
	TokenEnv          string   `yaml:"tokenEnv"`
	TokenEnvFallbacks []string `yaml:"tokenEnvFallbacks"`
	AuthScheme        string   `yaml:"authScheme"`
	AuthHeader        string   `yaml:"authHeader"`
	RefreshMs         int64    `yaml:"refreshMs"`
	StaleMs           int64    `yaml:"staleMs"`
	ExcludeLimitTypes []string `yaml:"excludeLimitTypes"`
	VisibleLimitTypes []string `yaml:"visibleLimitTypes"`
}

type CustomProviderConfig struct {
	ID        string                 `yaml:"id"`
	Label     string                 `yaml:"label"`
	Enabled   bool                   `yaml:"enabled"`
	RefreshMs int64                  `yaml:"refreshMs"`
	StaleMs   int64                  `yaml:"staleMs"`
	Endpoints []CustomEndpointConfig `yaml:"endpoints"`
}

type CustomEndpointConfig struct {
	ID        string            `yaml:"id"`
	URL       string            `yaml:"url"`
	Method    string            `yaml:"method"`
	Headers   map[string]string `yaml:"headers"`
	Auth      CustomAuthConfig  `yaml:"auth"`
	Body      string            `yaml:"body"`
	ItemsPath string            `yaml:"itemsPath"`
	Item      CustomItemMapping `yaml:"item"`
}

type CustomAuthConfig struct {
	Type       string `yaml:"type"`
	TokenEnv   string `yaml:"tokenEnv"`
	HeaderName string `yaml:"headerName"`
}

type CustomItemMapping struct {
	ID          any                 `yaml:"id"`
	Label       any                 `yaml:"label"`
	Window      CustomWindowMapping `yaml:"window"`
	Unit        any                 `yaml:"unit"`
	Limit       any                 `yaml:"limit"`
	Used        any                 `yaml:"used"`
	Remaining   any                 `yaml:"remaining"`
	PercentUsed any                 `yaml:"percentUsed"`
	Visible     any                 `yaml:"visible"`
}

type CustomWindowMapping struct {
	ID            any    `yaml:"id"`
	Label         any    `yaml:"label"`
	Kind          any    `yaml:"kind"`
	ResetAt       any    `yaml:"resetAt"`
	ResetAtFormat string `yaml:"resetAtFormat"`
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
		Server: ServerConfig{Host: "127.0.0.1", Port: 8787, ReadAuth: ReadAuth{Mode: "none"}, StatePath: "%STATE%/usagent/snapshot.json"},
		Providers: ProvidersConfig{
			ClaudeOAuth: ClaudeOAuthConfig{Enabled: false, CredentialsPath: "~/.claude/.credentials.json", EndpointURL: "https://api.anthropic.com/api/oauth/usage", BetaHeader: "oauth-2025-04-20"},
			OpenAI:      OpenAIConfig{Enabled: false, APIKeyEnv: "OPENAI_ADMIN_KEY", BaseURL: "https://api.openai.com"},
			ZAI:         ZAIConfig{Enabled: false, EndpointURL: "https://api.z.ai/api/monitor/usage/quota/limit", TokenEnv: "ZAI_API_KEY", TokenEnvFallbacks: []string{"GLM_API_KEY"}, AuthScheme: "bearer", AuthHeader: "Authorization", ExcludeLimitTypes: []string{"TIME_LIMIT"}},
		},
		UsageView: UsageViewConfig{Providers: []string{"claude-code", "openai", "z-ai"}},
		Quota:     QuotaConfig{RefreshMs: int64((5 * time.Minute) / time.Millisecond)},
	}
}

func DefaultConfigPath() string {
	if v := os.Getenv("USAGENT_CONFIG"); v != "" {
		return v
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(homeDir(), ".config")
	}
	candidate := filepath.Join(xdg, "usagent", "config.yaml")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return "config.example.yaml"
}

func ParseFlags(args []string) (CLIOptions, error) {
	fs := flag.NewFlagSet("usagent", flag.ContinueOnError)
	opts := CLIOptions{ConfigPath: DefaultConfigPath()}
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
	cfg = normalizeProviderDefaults(cfg)
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

func normalizeProviderDefaults(cfg Config) Config {
	if cfg.Providers.ClaudeOAuth.CredentialsPath == "" {
		cfg.Providers.ClaudeOAuth.CredentialsPath = "~/.claude/.credentials.json"
	}
	if cfg.Providers.ClaudeOAuth.EndpointURL == "" {
		cfg.Providers.ClaudeOAuth.EndpointURL = "https://api.anthropic.com/api/oauth/usage"
	}
	if cfg.Providers.ClaudeOAuth.BetaHeader == "" {
		cfg.Providers.ClaudeOAuth.BetaHeader = "oauth-2025-04-20"
	}
	if cfg.Providers.ClaudeOAuth.UserAgent == "" {
		cfg.Providers.ClaudeOAuth.UserAgent = "claude-code/2.0"
	}
	if cfg.Providers.ClaudeOAuth.RefreshMs <= 0 {
		cfg.Providers.ClaudeOAuth.RefreshMs = cfg.Quota.RefreshMs
	}
	cfg.Providers.ClaudeOAuth.RefreshMs = max(cfg.Providers.ClaudeOAuth.RefreshMs, MinClaudeOAuthRefreshMs)
	if cfg.Providers.ClaudeOAuth.StaleMs <= 0 {
		cfg.Providers.ClaudeOAuth.StaleMs = max(cfg.Providers.ClaudeOAuth.RefreshMs*3, int64((15*time.Minute)/time.Millisecond))
	}

	// Back-compat: migrate previous quota.providers OpenAI/z.ai settings into typed providers when present.
	for _, qp := range cfg.Quota.Providers {
		switch qp.ID {
		case "openai":
			if cfg.Providers.OpenAI.APIKeyEnv == "" {
				cfg.Providers.OpenAI.APIKeyEnv = qp.APIKeyEnv
			}
			if cfg.Providers.OpenAI.RefreshMs <= 0 {
				cfg.Providers.OpenAI.RefreshMs = qp.RefreshMs
			}
			if cfg.Providers.OpenAI.StaleMs <= 0 {
				cfg.Providers.OpenAI.StaleMs = qp.StaleMs
			}
			if len(cfg.Providers.OpenAI.Budgets) == 0 {
				cfg.Providers.OpenAI.Budgets = qp.Budgets
			}
		case "z-ai":
			if cfg.Providers.ZAI.EndpointURL == "" {
				cfg.Providers.ZAI.EndpointURL = qp.EndpointURL
			}
			if cfg.Providers.ZAI.TokenEnv == "" {
				cfg.Providers.ZAI.TokenEnv = qp.TokenEnv
			}
			if cfg.Providers.ZAI.RefreshMs <= 0 {
				cfg.Providers.ZAI.RefreshMs = qp.RefreshMs
			}
			if cfg.Providers.ZAI.StaleMs <= 0 {
				cfg.Providers.ZAI.StaleMs = qp.StaleMs
			}
		}
	}

	if cfg.Providers.OpenAI.APIKeyEnv == "" {
		cfg.Providers.OpenAI.APIKeyEnv = "OPENAI_ADMIN_KEY"
	}
	if cfg.Providers.OpenAI.BaseURL == "" && cfg.Providers.OpenAI.CostsEndpoint == "" {
		cfg.Providers.OpenAI.BaseURL = "https://api.openai.com"
	}
	if cfg.Providers.OpenAI.RefreshMs <= 0 {
		cfg.Providers.OpenAI.RefreshMs = cfg.Quota.RefreshMs
	}
	cfg.Providers.OpenAI.RefreshMs = max(cfg.Providers.OpenAI.RefreshMs, MinOpenAIRefreshMs)
	if cfg.Providers.OpenAI.StaleMs <= 0 {
		cfg.Providers.OpenAI.StaleMs = max(cfg.Providers.OpenAI.RefreshMs*3, int64((15*time.Minute)/time.Millisecond))
	}

	if cfg.Providers.ZAI.EndpointURL == "" {
		cfg.Providers.ZAI.EndpointURL = "https://api.z.ai/api/monitor/usage/quota/limit"
	}
	if cfg.Providers.ZAI.TokenEnv == "" {
		cfg.Providers.ZAI.TokenEnv = "ZAI_API_KEY"
	}
	if len(cfg.Providers.ZAI.TokenEnvFallbacks) == 0 {
		cfg.Providers.ZAI.TokenEnvFallbacks = []string{"GLM_API_KEY"}
	}
	if cfg.Providers.ZAI.AuthScheme == "" {
		cfg.Providers.ZAI.AuthScheme = "bearer"
	}
	if cfg.Providers.ZAI.AuthHeader == "" {
		cfg.Providers.ZAI.AuthHeader = "Authorization"
	}
	if len(cfg.Providers.ZAI.ExcludeLimitTypes) == 0 {
		cfg.Providers.ZAI.ExcludeLimitTypes = []string{"TIME_LIMIT"}
	}
	if cfg.Providers.ZAI.RefreshMs <= 0 {
		cfg.Providers.ZAI.RefreshMs = cfg.Quota.RefreshMs
	}
	cfg.Providers.ZAI.RefreshMs = max(cfg.Providers.ZAI.RefreshMs, MinZAIRefreshMs)
	if cfg.Providers.ZAI.StaleMs <= 0 {
		cfg.Providers.ZAI.StaleMs = max(cfg.Providers.ZAI.RefreshMs*3, int64((15*time.Minute)/time.Millisecond))
	}

	for i := range cfg.Providers.Custom {
		if cfg.Providers.Custom[i].RefreshMs <= 0 {
			cfg.Providers.Custom[i].RefreshMs = cfg.Quota.RefreshMs
		}
		if cfg.Providers.Custom[i].StaleMs <= 0 {
			cfg.Providers.Custom[i].StaleMs = max(cfg.Providers.Custom[i].RefreshMs*3, int64((15*time.Minute)/time.Millisecond))
		}
		for j := range cfg.Providers.Custom[i].Endpoints {
			if cfg.Providers.Custom[i].Endpoints[j].Method == "" {
				cfg.Providers.Custom[i].Endpoints[j].Method = "GET"
			}
			if cfg.Providers.Custom[i].Endpoints[j].Headers == nil {
				cfg.Providers.Custom[i].Endpoints[j].Headers = map[string]string{}
			}
		}
	}
	return cfg
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
