package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jmalloc/usagent/internal/analysis"
	"github.com/jmalloc/usagent/internal/config"
)

type ExpiringUsageOptions struct {
	ConfigPath              string
	Host                    string
	Port                    int
	JSON                    bool
	Offline                 bool
	Timeout                 time.Duration
	Within                  time.Duration
	WithinMS                int64
	MinimumRemainingPercent float64
	Providers               []string
	IncludeLowConfidence    bool
}

func RunExpiringUsage(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	opts, err := parseExpiringUsageFlags(args)
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

	if !opts.Offline {
		res, err := FetchExpiringUsageFromDaemon(ctx, cfg, opts)
		if err == nil {
			return writeExpiringUsage(stdout, res, opts.JSON)
		}
		primaryErr := err
		if fallback, ok := loadUserDaemonConfig(opts.ConfigPath); ok {
			res, err = FetchExpiringUsageFromDaemon(ctx, fallback, opts)
			if err == nil {
				return writeExpiringUsage(stdout, res, opts.JSON)
			}
		}
		if stderr != nil {
			_, _ = fmt.Fprintf(stderr, "usagent daemon unavailable, deriving expiring usage locally: %v\n", primaryErr)
		}
	}

	usage, err := LocalUsage(ctx, cfg, opts.Timeout)
	if err != nil {
		return err
	}
	res := analysis.ExpiringUsage(usage, opts.analysisOptions(time.Now()))
	return writeExpiringUsage(stdout, res, opts.JSON)
}

func parseExpiringUsageFlags(args []string) (ExpiringUsageOptions, error) {
	fs := flag.NewFlagSet("usagent expiring-usage", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := ExpiringUsageOptions{ConfigPath: config.DefaultConfigPath(), Timeout: 10 * time.Second}
	var providers string
	fs.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "path to YAML config file")
	fs.StringVar(&opts.Host, "host", opts.Host, "daemon listen host override")
	fs.IntVar(&opts.Port, "port", opts.Port, "daemon listen port override")
	fs.BoolVar(&opts.JSON, "json", false, "print raw expiring usage JSON")
	fs.BoolVar(&opts.Offline, "offline", false, "skip daemon and refresh/read usage locally")
	fs.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "daemon/local refresh timeout")
	fs.DurationVar(&opts.Within, "within", 0, "only include opportunities resetting within this duration")
	fs.Int64Var(&opts.WithinMS, "within-ms", 0, "only include opportunities resetting within this many milliseconds")
	fs.Float64Var(&opts.MinimumRemainingPercent, "minimum-remaining-percent", 0, "minimum remaining quota percent to include")
	fs.StringVar(&providers, "providers", "", "comma-separated provider IDs to include")
	fs.BoolVar(&opts.IncludeLowConfidence, "include-low-confidence", false, "include low-confidence opportunities")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() > 0 {
		return opts, fmt.Errorf("unexpected expiring-usage arguments: %s", strings.Join(fs.Args(), " "))
	}
	if opts.Within < 0 {
		return opts, errors.New("within must be a non-negative duration")
	}
	if opts.WithinMS < 0 {
		return opts, errors.New("within-ms must be a non-negative integer")
	}
	if opts.MinimumRemainingPercent < 0 || opts.MinimumRemainingPercent > 100 {
		return opts, errors.New("minimum-remaining-percent must be a number from 0 to 100")
	}
	opts.Providers = splitProviders(providers)
	return opts, nil
}

func splitProviders(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	providers := []string{}
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		provider := strings.TrimSpace(part)
		if provider != "" && !seen[provider] {
			providers = append(providers, provider)
			seen[provider] = true
		}
	}
	return providers
}

func FetchExpiringUsageFromDaemon(ctx context.Context, cfg config.Config, opts ExpiringUsageOptions) (analysis.ExpiringUsageResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	u := url.URL{Scheme: "http", Host: net.JoinHostPort(clientHost(cfg.Server.Host), fmt.Sprint(cfg.Server.Port)), Path: "/v1/expiring-usage"}
	q := u.Query()
	if opts.Within > 0 {
		q.Set("within", opts.Within.String())
	}
	if opts.WithinMS > 0 {
		q.Set("withinMs", strconv.FormatInt(opts.WithinMS, 10))
	}
	if opts.MinimumRemainingPercent > 0 {
		q.Set("minimumRemainingPercent", strconv.FormatFloat(opts.MinimumRemainingPercent, 'f', -1, 64))
	}
	if len(opts.Providers) > 0 {
		q.Set("providers", strings.Join(opts.Providers, ","))
	}
	if opts.IncludeLowConfidence {
		q.Set("includeLowConfidence", "true")
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return analysis.ExpiringUsageResponse{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return analysis.ExpiringUsageResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return analysis.ExpiringUsageResponse{}, fmt.Errorf("GET %s returned HTTP %d", u.String(), resp.StatusCode)
	}
	var res analysis.ExpiringUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return analysis.ExpiringUsageResponse{}, err
	}
	if res.Opportunities == nil {
		res.Opportunities = []analysis.ExpiringUsageOpportunity{}
	}
	return res, nil
}

func (opts ExpiringUsageOptions) analysisOptions(now time.Time) analysis.ExpiringUsageOptions {
	within := opts.Within
	if opts.WithinMS > 0 {
		d := time.Duration(opts.WithinMS) * time.Millisecond
		if within == 0 || d < within {
			within = d
		}
	}
	providers := map[string]bool{}
	for _, provider := range opts.Providers {
		providers[provider] = true
	}
	if len(providers) == 0 {
		providers = nil
	}
	return analysis.ExpiringUsageOptions{Now: now, Within: within, MinimumRemainingPercent: opts.MinimumRemainingPercent, Providers: providers, IncludeLowConfidence: opts.IncludeLowConfidence}
}

func writeExpiringUsage(w io.Writer, res analysis.ExpiringUsageResponse, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	_, err := io.WriteString(w, FormatExpiringUsage(res))
	return err
}

func FormatExpiringUsage(res analysis.ExpiringUsageResponse) string {
	if len(res.Opportunities) == 0 {
		return "No expiring usage opportunities found.\n"
	}
	ops := append([]analysis.ExpiringUsageOpportunity(nil), res.Opportunities...)
	sort.SliceStable(ops, func(i, j int) bool {
		left, right := ops[i], ops[j]
		if left.OpportunityScore == right.OpportunityScore {
			if left.ResetAt == right.ResetAt {
				return left.Provider+left.ItemID < right.Provider+right.ItemID
			}
			return left.ResetAt < right.ResetAt
		}
		return left.OpportunityScore > right.OpportunityScore
	})
	lines := make([]string, 0, len(ops))
	for _, op := range ops {
		item := op.Label
		if item == "" {
			item = op.ItemID
		}
		lines = append(lines, fmt.Sprintf("%s/%s: %s remaining, estimated waste ~%s, resets in %s, urgency %s, confidence %s",
			op.Provider,
			item,
			formatExpiringAmount(op.Remaining, op.Unit),
			formatExpiringWaste(op),
			formatExpiringDuration(time.Duration(op.TimeRemainingMs)*time.Millisecond),
			op.Urgency,
			op.Confidence,
		))
	}
	return strings.Join(lines, "\n") + "\n"
}

func formatExpiringWaste(op analysis.ExpiringUsageOpportunity) string {
	amount := formatExpiringAmount(op.EstimatedWastedAmount, op.Unit)
	if op.EstimatedWastedPercent > 0 {
		amount += fmt.Sprintf(" (%s%%)", formatNumber(op.EstimatedWastedPercent))
	}
	return amount
}

func formatExpiringAmount(v float64, unit string) string {
	if unit == "percent" || unit == "%" {
		return formatNumber(v) + "%"
	}
	if unit == "" {
		unit = "units"
	}
	return formatNumber(v) + " " + unit
}

func formatExpiringDuration(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	if d < time.Minute {
		return "<1m"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(math.Round(d.Minutes())))
	}
	if d < 48*time.Hour {
		return fmt.Sprintf("%dh", int(math.Round(d.Hours())))
	}
	return fmt.Sprintf("%dd", int(math.Round(d.Hours()/24)))
}
