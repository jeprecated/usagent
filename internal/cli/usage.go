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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jeprecated/usagent/internal/analysis"
	"github.com/jeprecated/usagent/internal/app"
	"github.com/jeprecated/usagent/internal/clientpolicy"
	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/model"
)

type UsageOptions struct {
	ConfigPath   string
	DaemonURL    string
	DaemonURLSet bool
	Host         string
	HostSet      bool
	Port         int
	PortSet      bool
	JSON         bool
	Offline      bool
	Timeout      time.Duration
}

func (opts UsageOptions) policyFlags() clientpolicy.FlagOptions {
	return clientpolicy.FlagOptions{
		DaemonURL: opts.DaemonURL, DaemonURLSet: opts.DaemonURLSet,
		Host: opts.Host, HostSet: opts.HostSet,
		Port: opts.Port, PortSet: opts.PortSet,
		Offline: opts.Offline,
	}
}

func RunUsage(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	opts, err := parseUsageFlags(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return err
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}

	var usage model.Usage
	access, err := accessDaemon(ctx, cfg, opts.ConfigPath, opts.policyFlags(), opts.Timeout, func(client daemonClient) error {
		var requestErr error
		usage, requestErr = fetchUsageFromDaemon(ctx, client)
		return requestErr
	})
	if err != nil {
		return err
	}
	if !access.useLocal {
		return writeUsage(stdout, usage, opts.JSON)
	}
	if access.diagnostic != nil && stderr != nil {
		_, _ = fmt.Fprintf(stderr, "usagent daemon unavailable, refreshing locally: %v\n", access.diagnostic)
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
	fs.StringVar(&opts.DaemonURL, "daemon-url", opts.DaemonURL, "daemon HTTP(S) origin override")
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
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "daemon-url":
			opts.DaemonURLSet = true
		case "host":
			opts.HostSet = true
		case "port":
			opts.PortSet = true
		}
	})
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
	resolution, err := clientpolicy.Resolve(cfg, clientpolicy.FlagOptions{})
	if err != nil {
		return model.Usage{}, err
	}
	return fetchUsageFromDaemon(ctx, daemonClient{origin: resolution.Origin, timeout: timeout})
}

func fetchUsageFromDaemon(ctx context.Context, client daemonClient) (model.Usage, error) {
	var usage model.Usage
	if err := client.get(ctx, "/v1/usage", nil, &usage); err != nil {
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

type usageRow struct {
	provider   string
	quota      string
	window     string
	remaining  string
	status     string
	percent    float64
	hasPercent bool
}

type usageWidths struct {
	provider  int
	quota     int
	window    int
	remaining int
}

func FormatUsage(usage model.Usage) string {
	return FormatUsageWithColor(usage, false)
}

func FormatUsageWithColor(usage model.Usage, color bool) string {
	itemsByProvider := map[string][]model.QuotaItem{}
	for _, item := range usage.QuotaItems {
		if item.Visible {
			itemsByProvider[item.Provider] = append(itemsByProvider[item.Provider], item)
		}
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

	rows := []usageRow{}
	for _, provider := range usage.Providers {
		items := itemsByProvider[provider.ID]
		providerState := defaultString(string(provider.State), "stale")
		if len(items) == 0 {
			status := providerState
			if message := usageErrorSummary(provider.Error); message != "" {
				status += " · " + message
			}
			rows = append(rows, usageRow{provider: provider.Label, quota: "unavailable", window: "-", remaining: "-", status: status})
			continue
		}
		seenErrors := map[string]bool{}
		for i, item := range items {
			status := item.State
			if status == "fresh" {
				status = ""
			}
			if status != "" {
				if age := staleAge(item, usage.GeneratedAt); age != "" {
					status += " " + age
				}
			} else if i == 0 && providerState != "fresh" {
				status = providerState
			}
			itemError := item.Error
			if itemError == nil {
				itemError = provider.Error
			}
			if message := usageErrorSummary(itemError); message != "" {
				if status == "" {
					status = "error"
				}
				if !seenErrors[message] {
					status += " · " + message
					seenErrors[message] = true
				}
			}
			percent, hasPercent := itemRemainingPercent(item)
			row := usageRow{quota: usageQuotaLabel(provider.Label, item), window: defaultString(item.Window.Label, "-"), remaining: formatRemaining(item), status: status, percent: percent, hasPercent: hasPercent}
			if i == 0 {
				row.provider = provider.Label
			}
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return "No providers configured.\n"
	}

	widths := usageWidths{len("PROVIDER"), len("QUOTA"), len("WINDOW"), len("REMAINING")}
	for _, row := range rows {
		widths.provider = max(widths.provider, len(row.provider))
		widths.quota = max(widths.quota, len(row.quota))
		widths.window = max(widths.window, len(row.window))
		widths.remaining = max(widths.remaining, len(row.remaining))
	}
	lines := []string{
		colorize("Usage", ansiBold, color),
		colorize("remaining quota by provider", ansiDim, color),
		"",
		colorize(strings.Join([]string{padRight("PROVIDER", widths.provider), padRight("QUOTA", widths.quota), padRight("WINDOW", widths.window), padLeft("REMAINING", widths.remaining), "STATUS"}, "  "), ansiDim, color),
	}
	for _, row := range rows {
		status := row.status
		if status != "" {
			status = colorize(status, statusANSI(status), color)
		}
		line := strings.Join([]string{
			colorize(padRight(row.provider, widths.provider), ansiBold+ansiCyan, color),
			padRight(row.quota, widths.quota),
			colorize(padRight(row.window, widths.window), ansiCyan, color),
			colorize(padLeft(row.remaining, widths.remaining), remainingANSI(row), color),
			status,
		}, "  ")
		lines = append(lines, strings.TrimRight(line, " "))
	}
	return strings.Join(lines, "\n") + "\n"
}

// Keep the table compact, including errors loaded from older cached snapshots.
// The original message remains available in --json output.
func usageErrorSummary(itemError *model.ItemError) string {
	if itemError == nil {
		return ""
	}
	message := strings.Join(strings.Fields(itemError.Message), " ")
	if prefix, response, ok := strings.Cut(message, " returned HTTP "); ok {
		code, _, _ := strings.Cut(response, ":")
		if code == "401" {
			return "authentication required; sign in again"
		}
		message = prefix + " returned HTTP " + code
	}
	const maxLength = 100
	if runes := []rune(message); len(runes) > maxLength {
		message = string(runes[:maxLength-1]) + "…"
	}
	return message
}

func usageQuotaLabel(provider string, item model.QuotaItem) string {
	label := strings.TrimSpace(item.Label)
	name := strings.Fields(provider)
	if len(name) > 0 {
		prefix := name[0] + " "
		if len(label) >= len(prefix) && strings.EqualFold(label[:len(prefix)], prefix) {
			label = strings.TrimSpace(label[len(prefix):])
		}
	}
	return defaultString(label, item.ID)
}

func remainingANSI(row usageRow) string {
	if !row.hasPercent {
		return ansiGreen
	}
	switch {
	case row.percent <= 20:
		return ansiBold + ansiRed
	case row.percent <= 50:
		return ansiYellow
	default:
		return ansiGreen
	}
}

func statusANSI(status string) string {
	switch {
	case strings.Contains(strings.ToLower(status), "error"):
		return ansiRed
	case status != "":
		return ansiYellow
	default:
		return ansiDim
	}
}

func writeUsage(w io.Writer, usage model.Usage, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(usage)
	}
	_, err := io.WriteString(w, FormatUsageWithColor(usage, shouldColor(w)))
	return err
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
		return formatNumber(remaining) + "%"
	case "usd":
		return "$" + formatNumber(remaining) + formatRemainingPercentSuffix(item)
	default:
		unit := defaultString(item.Unit, "units")
		return fmt.Sprintf("%s %s%s", formatNumber(remaining), unit, formatRemainingPercentSuffix(item))
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
