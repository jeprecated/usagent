package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jeprecated/usagent/internal/analysis"
	"github.com/jeprecated/usagent/internal/app"
	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/model"
)

type UsageOptions struct {
	ConfigPath string
	Host       string
	Port       int
	JSON       bool
	Offline    bool
	Timeout    time.Duration
}

func RunUsage(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	opts, err := parseUsageFlags(args)
	if err != nil {
		return err
	}
	cfg, err := config.LoadWithOverrides(opts.ConfigPath, config.CLIOptions{ConfigPath: opts.ConfigPath, Host: opts.Host, Port: opts.Port})
	if err != nil {
		return err
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}

	var usage model.Usage
	if !opts.Offline {
		usage, err = FetchUsageFromDaemon(ctx, cfg, opts.Timeout)
		if err == nil {
			return writeUsage(stdout, usage, opts.JSON)
		}
		primaryErr := err
		if fallback, ok := loadUserDaemonConfig(opts.ConfigPath); ok {
			usage, err = FetchUsageFromDaemon(ctx, fallback, opts.Timeout)
			if err == nil {
				return writeUsage(stdout, usage, opts.JSON)
			}
		}
		if stderr != nil {
			_, _ = fmt.Fprintf(stderr, "usagent daemon unavailable, refreshing locally: %v\n", primaryErr)
		}
	}
	usage, err = LocalUsage(ctx, cfg, opts.Timeout)
	if err != nil {
		return err
	}
	return writeUsage(stdout, usage, opts.JSON)
}

func parseUsageFlags(args []string) (UsageOptions, error) {
	fs := flag.NewFlagSet("usagent usage", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := UsageOptions{ConfigPath: config.DefaultConfigPath(), Timeout: 10 * time.Second}
	fs.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "path to YAML config file")
	fs.StringVar(&opts.Host, "host", opts.Host, "daemon listen host override")
	fs.IntVar(&opts.Port, "port", opts.Port, "daemon listen port override")
	fs.BoolVar(&opts.JSON, "json", false, "print raw schema v2 usage JSON")
	fs.BoolVar(&opts.Offline, "offline", false, "skip daemon and refresh/read usage locally")
	fs.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "daemon/local refresh timeout")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() > 0 {
		return opts, fmt.Errorf("unexpected usage arguments: %s", strings.Join(fs.Args(), " "))
	}
	return opts, nil
}

func loadUserDaemonConfig(primaryPath string) (config.Config, bool) {
	path, ok := userConfigPath()
	if !ok || path == primaryPath {
		return config.Config{}, false
	}
	cfg, err := config.Load(path)
	if err != nil {
		return config.Config{}, false
	}
	return cfg, true
}

func userConfigPath() (string, bool) {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return "", false
	}
	path := filepath.Join(dir, "usagent", "config.yaml")
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	return path, true
}

func FetchUsageFromDaemon(ctx context.Context, cfg config.Config, timeout time.Duration) (model.Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	u := url.URL{Scheme: "http", Host: net.JoinHostPort(clientHost(cfg.Server.Host), fmt.Sprint(cfg.Server.Port)), Path: "/v1/usage"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return model.Usage{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return model.Usage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return model.Usage{}, fmt.Errorf("GET %s returned HTTP %d", u.String(), resp.StatusCode)
	}
	var usage model.Usage
	if err := json.NewDecoder(resp.Body).Decode(&usage); err != nil {
		return model.Usage{}, err
	}
	if usage.SchemaVersion != 2 || usage.Service != "usagent" {
		return model.Usage{}, errors.New("daemon returned unexpected usage schema")
	}
	return usage, nil
}

func LocalUsage(ctx context.Context, cfg config.Config, timeout time.Duration) (model.Usage, error) {
	usage, _, err := LocalUsageAndBurnRates(ctx, cfg, timeout)
	return usage, err
}

func LocalUsageAndBurnRates(ctx context.Context, cfg config.Config, timeout time.Duration) (model.Usage, map[string]analysis.BurnRateEstimate, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(cfg, logger)
	if err := a.LoadState(); err != nil {
		return model.Usage{}, nil, err
	}
	now := time.Now()
	if !hasDueProvider(a, now) {
		usage := a.Usage(now)
		return usage, a.BurnRateEstimates(usage.QuotaItems, now), nil
	}
	unlock, locked, err := acquireLocalRefreshLock(ctx, cfg.Server.StatePath)
	if err != nil {
		return model.Usage{}, nil, err
	}
	if !locked {
		now := time.Now()
		usage := a.Usage(now)
		return usage, a.BurnRateEstimates(usage.QuotaItems, now), nil
	}
	defer unlock()
	// Another local process may have refreshed while this process waited for the
	// lock. Reload before deciding which providers are still due.
	if err := a.LoadState(); err != nil {
		return model.Usage{}, nil, err
	}
	now = time.Now()
	for _, p := range a.Providers {
		if ctx.Err() != nil {
			return model.Usage{}, nil, ctx.Err()
		}
		if a.Store.NeedsRefresh(p.ID(), now) {
			a.RefreshOne(ctx, p, now)
		}
	}
	now = time.Now()
	usage := a.Usage(now)
	return usage, a.BurnRateEstimates(usage.QuotaItems, now), nil
}

func hasDueProvider(a *app.App, now time.Time) bool {
	for _, p := range a.Providers {
		if a.Store.NeedsRefresh(p.ID(), now) {
			return true
		}
	}
	return false
}

func acquireLocalRefreshLock(ctx context.Context, statePath string) (func(), bool, error) {
	if statePath == "" {
		return func() {}, true, nil
	}
	lockPath := statePath + ".refresh.lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, true, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			_ = f.Close()
			return nil, false, err
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return func() {}, false, nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func FormatUsage(usage model.Usage) string {
	itemsByProvider := map[string][]model.QuotaItem{}
	for _, item := range usage.QuotaItems {
		if !item.Visible {
			continue
		}
		itemsByProvider[item.Provider] = append(itemsByProvider[item.Provider], item)
	}
	for provider := range itemsByProvider {
		sort.SliceStable(itemsByProvider[provider], func(i, j int) bool {
			left, right := itemsByProvider[provider][i], itemsByProvider[provider][j]
			if left.Window.ID == right.Window.ID {
				return left.ID < right.ID
			}
			return left.Window.ID < right.Window.ID
		})
	}
	now := usage.GeneratedAt
	lines := []string{}
	for _, provider := range usage.Providers {
		items := itemsByProvider[provider.ID]
		state := string(provider.State)
		if state == "" {
			state = "stale"
		}
		if len(items) == 0 {
			suffix := state
			if provider.Error != nil && provider.Error.Message != "" {
				suffix = state + ": " + provider.Error.Message
			}
			lines = append(lines, fmt.Sprintf("%s: unavailable [%s]", provider.Label, suffix))
			continue
		}
		parts := make([]string, 0, len(items))
		hasItemState := false
		for _, item := range items {
			if item.State != "" && item.State != "fresh" {
				hasItemState = true
			}
			parts = append(parts, formatItem(item, now))
		}
		line := fmt.Sprintf("%s: %s", provider.Label, strings.Join(parts, " · "))
		if state != "fresh" && !hasItemState {
			line += " [" + state + "]"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "No providers configured.\n"
	}
	return strings.Join(lines, "\n") + "\n"
}

func writeUsage(w io.Writer, usage model.Usage, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(usage)
	}
	_, err := io.WriteString(w, FormatUsage(usage))
	return err
}

func formatItem(item model.QuotaItem, now int64) string {
	label := item.Window.Label
	if label == "" {
		label = item.Label
	}
	value := formatRemaining(item)
	if item.State != "" && item.State != "fresh" {
		suffix := item.State
		if age := staleAge(item, now); age != "" {
			suffix += " " + age
		}
		value += " [" + suffix + "]"
	}
	return fmt.Sprintf("%s %s", label, value)
}

// staleAge renders how long ago a stale item's value was last refreshed as a
// compact m/h/d duration. It returns "" when no reliable timestamp is present
// (e.g. the item never refreshed successfully).
func staleAge(item model.QuotaItem, now int64) string {
	if item.Refresh == nil || item.Refresh.LastUpdatedAt <= 0 || now <= 0 {
		return ""
	}
	secs := (now - item.Refresh.LastUpdatedAt) / 1000
	if secs < 0 {
		secs = 0
	}
	switch {
	case secs >= 86400:
		return fmt.Sprintf("%dd", secs/86400)
	case secs >= 3600:
		return fmt.Sprintf("%dh", secs/3600)
	case secs >= 60:
		return fmt.Sprintf("%dm", secs/60)
	default:
		return "<1m"
	}
}

func formatRemaining(item model.QuotaItem) string {
	remaining := item.Remaining
	switch strings.ToLower(item.Unit) {
	case "percent", "%":
		return fmt.Sprintf("%s remaining", formatNumber(remaining)+"%")
	case "usd":
		return fmt.Sprintf("$%s%s remaining", formatNumber(remaining), formatRemainingPercentSuffix(item))
	default:
		unit := item.Unit
		if unit == "" {
			unit = "units"
		}
		return fmt.Sprintf("%s %s%s remaining", formatNumber(remaining), unit, formatRemainingPercentSuffix(item))
	}
}

func formatRemainingPercentSuffix(item model.QuotaItem) string {
	percent, ok := itemRemainingPercent(item)
	if !ok {
		return ""
	}
	return fmt.Sprintf(" (%s%%)", formatNumber(percent))
}

func itemRemainingPercent(item model.QuotaItem) (float64, bool) {
	if item.Limit > 0 {
		return math.Max(0, math.Min(100, item.Remaining/item.Limit*100)), true
	}
	if item.PercentUsed > 0 || item.Used > 0 {
		return math.Max(0, math.Min(100, 100-item.PercentUsed)), true
	}
	return 0, false
}

func formatNumber(v float64) string {
	if math.Abs(v-math.Round(v)) < 0.005 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

func clientHost(host string) string {
	switch host {
	case "", "0.0.0.0":
		return "127.0.0.1"
	case "::", "[::]":
		return "::1"
	default:
		return host
	}
}
