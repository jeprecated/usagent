package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jeprecated/usagent/internal/analysis"
	"github.com/jeprecated/usagent/internal/clientpolicy"
	"github.com/jeprecated/usagent/internal/config"
)

type ExpiringUsageOptions struct {
	ConfigPath              string
	DaemonURL               string
	DaemonURLSet            bool
	Host                    string
	HostSet                 bool
	Port                    int
	PortSet                 bool
	JSON                    bool
	Offline                 bool
	Timeout                 time.Duration
	Within                  time.Duration
	WithinMS                int64
	MinimumRemainingPercent float64
	Providers               []string
	Tiers                   []string
	Tags                    []string
	IncludeLowConfidence    bool
}

func (opts ExpiringUsageOptions) policyFlags() clientpolicy.FlagOptions {
	return clientpolicy.FlagOptions{
		DaemonURL: opts.DaemonURL, DaemonURLSet: opts.DaemonURLSet,
		Host: opts.Host, HostSet: opts.HostSet,
		Port: opts.Port, PortSet: opts.PortSet,
		Offline: opts.Offline,
	}
}

func RunExpiringUsage(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	opts, err := parseExpiringUsageFlags(args)
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

	var res analysis.ExpiringUsageResponse
	access, err := accessDaemon(ctx, cfg, opts.ConfigPath, opts.policyFlags(), opts.Timeout, func(client daemonClient) error {
		var requestErr error
		res, requestErr = fetchExpiringUsageFromDaemon(ctx, client, opts)
		return requestErr
	})
	if err != nil {
		return err
	}
	if !access.useLocal {
		return writeExpiringUsage(stdout, res, opts.JSON)
	}
	if access.diagnostic != nil && stderr != nil {
		_, _ = fmt.Fprintf(stderr, "usagent daemon unavailable, deriving expiring usage locally: %v\n", access.diagnostic)
	}

	usage, rates, err := LocalUsageAndBurnRates(ctx, cfg, opts.Timeout)
	if err != nil {
		return err
	}
	now := time.Now()
	analysisOpts := opts.analysisOptions(now)
	analysisOpts.BurnRates = rates
	res = analysis.ExpiringUsage(usage, analysisOpts)
	return writeExpiringUsage(stdout, res, opts.JSON)
}

func parseExpiringUsageFlags(args []string) (ExpiringUsageOptions, error) {
	fs := flag.NewFlagSet("usagent expiring-usage", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := ExpiringUsageOptions{ConfigPath: config.DefaultConfigPath(), Timeout: 10 * time.Second}
	var providers string
	var tiers string
	var tags string
	fs.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "path to YAML config file")
	fs.StringVar(&opts.DaemonURL, "daemon-url", opts.DaemonURL, "daemon HTTP(S) origin override")
	fs.StringVar(&opts.Host, "host", opts.Host, "daemon listen host override")
	fs.IntVar(&opts.Port, "port", opts.Port, "daemon listen port override")
	fs.BoolVar(&opts.JSON, "json", false, "print raw expiring usage JSON")
	fs.BoolVar(&opts.Offline, "offline", false, "skip daemon and refresh/read usage locally")
	fs.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "daemon/local refresh timeout")
	fs.DurationVar(&opts.Within, "within", 0, "only include opportunities resetting within this duration")
	fs.Int64Var(&opts.WithinMS, "within-ms", 0, "only include opportunities resetting within this many milliseconds")
	fs.Float64Var(&opts.MinimumRemainingPercent, "minimum-remaining-percent", 0, "minimum remaining quota percent to include")
	fs.StringVar(&providers, "providers", "", "comma-separated provider IDs to include")
	fs.StringVar(&tiers, "tiers", "", "comma-separated provider/model tiers to include")
	fs.StringVar(&tags, "tags", "", "comma-separated provider/model tags to include")
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
	opts.Tiers = splitProviders(tiers)
	opts.Tags = splitProviders(tags)
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
	resolution, err := clientpolicy.Resolve(cfg, clientpolicy.FlagOptions{})
	if err != nil {
		return analysis.ExpiringUsageResponse{}, err
	}
	return fetchExpiringUsageFromDaemon(ctx, daemonClient{origin: resolution.Origin, timeout: opts.Timeout}, opts)
}

func fetchExpiringUsageFromDaemon(ctx context.Context, client daemonClient, opts ExpiringUsageOptions) (analysis.ExpiringUsageResponse, error) {
	q := make(url.Values)
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
	if len(opts.Tiers) > 0 {
		q.Set("tiers", strings.Join(opts.Tiers, ","))
	}
	if len(opts.Tags) > 0 {
		q.Set("tags", strings.Join(opts.Tags, ","))
	}
	if opts.IncludeLowConfidence {
		q.Set("includeLowConfidence", "true")
	}
	var res analysis.ExpiringUsageResponse
	if err := client.get(ctx, "/v1/expiring-usage", q, &res); err != nil {
		return analysis.ExpiringUsageResponse{}, err
	}
	if res.GeneratedAt <= 0 {
		return analysis.ExpiringUsageResponse{}, errors.New("daemon returned invalid expiring usage response: generatedAt must be positive")
	}
	for i, opportunity := range res.Opportunities {
		if strings.TrimSpace(opportunity.Provider) == "" {
			return analysis.ExpiringUsageResponse{}, fmt.Errorf("daemon returned invalid expiring usage response: opportunity %d provider must not be empty", i)
		}
		if strings.TrimSpace(opportunity.ItemID) == "" {
			return analysis.ExpiringUsageResponse{}, fmt.Errorf("daemon returned invalid expiring usage response: opportunity %d itemId must not be empty", i)
		}
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
	tiers := lowerSet(opts.Tiers)
	tags := lowerSet(opts.Tags)
	return analysis.ExpiringUsageOptions{Now: now, Within: within, MinimumRemainingPercent: opts.MinimumRemainingPercent, Providers: providers, Tiers: tiers, Tags: tags, IncludeLowConfidence: opts.IncludeLowConfidence}
}

func lowerSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			out[value] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func writeExpiringUsage(w io.Writer, res analysis.ExpiringUsageResponse, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	_, err := io.WriteString(w, FormatExpiringUsageWithColor(res, shouldColor(w)))
	return err
}

func FormatExpiringUsage(res analysis.ExpiringUsageResponse) string {
	return FormatExpiringUsageWithColor(res, false)
}

func FormatExpiringUsageWithColor(res analysis.ExpiringUsageResponse, color bool) string {
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
	rows := make([]expiringUsageRow, 0, len(ops))
	for _, op := range ops {
		item := op.Label
		if item == "" {
			item = op.ItemID
		}
		waste, raw := formatExpiringWasteCells(op)
		rows = append(rows, expiringUsageRow{
			urgency:   strings.ToUpper(defaultString(op.Urgency, "normal")),
			reset:     formatExpiringDuration(time.Duration(op.TimeRemainingMs) * time.Millisecond),
			subject:   op.Provider + " / " + item,
			remaining: formatExpiringAmount(op.Remaining, op.Unit),
			waste:     waste,
			raw:       raw,
			conf:      defaultString(op.Confidence, "unknown"),
		})
	}
	widths := expiringUsageColumnWidths(rows)
	lines := []string{colorize("Expiring usage", ansiBold, color), colorize("sorted by opportunity score", ansiDim, color), ""}
	lines = append(lines, expiringUsageHeader(widths, color))
	for _, row := range rows {
		lines = append(lines, expiringUsageLine(row, widths, color))
	}
	return strings.Join(lines, "\n") + "\n"
}

type expiringUsageRow struct {
	urgency   string
	reset     string
	subject   string
	remaining string
	waste     string
	raw       string
	conf      string
}

func formatExpiringWasteCells(op analysis.ExpiringUsageOpportunity) (string, string) {
	actionable := op.ActionableWasteAmount
	actionablePercent := op.ActionableWastePercent
	if actionable == 0 && op.EstimatedWastedAmount > 0 {
		actionable = op.EstimatedWastedAmount
		actionablePercent = op.EstimatedWastedPercent
	}
	actionableText := formatExpiringAmount(actionable, op.Unit)
	if actionablePercent > 0 {
		actionableText += fmt.Sprintf(" (%s%%)", formatNumber(actionablePercent))
	}
	raw := op.RawEstimatedWastedAmount
	if raw == 0 {
		raw = op.EstimatedWastedAmount
	}
	if op.OverlapContext != nil {
		if raw > 0 && raw != actionable {
			return "actionable " + actionableText, formatExpiringAmount(raw, op.Unit)
		}
		return "actionable " + actionableText, "—"
	}
	return "estimated unused " + actionableText, "—"
}

type expiringUsageWidths struct {
	urgency   int
	reset     int
	subject   int
	remaining int
	waste     int
	raw       int
	conf      int
}

func expiringUsageColumnWidths(rows []expiringUsageRow) expiringUsageWidths {
	widths := expiringUsageWidths{urgency: len("URGENCY"), reset: len("RESET"), subject: len("PROVIDER / ITEM"), remaining: len("REMAINING"), waste: len("WASTE"), raw: len("RAW UNUSED"), conf: len("CONF")}
	for _, row := range rows {
		widths.urgency = max(widths.urgency, len(row.urgency))
		widths.reset = max(widths.reset, len(row.reset))
		widths.subject = max(widths.subject, len(row.subject))
		widths.remaining = max(widths.remaining, len(row.remaining))
		widths.waste = max(widths.waste, len(row.waste))
		widths.raw = max(widths.raw, len(row.raw))
		widths.conf = max(widths.conf, len(row.conf))
	}
	return widths
}

func expiringUsageHeader(widths expiringUsageWidths, color bool) string {
	parts := []string{
		padRight("URGENCY", widths.urgency),
		padLeft("RESET", widths.reset),
		padRight("PROVIDER / ITEM", widths.subject),
		padLeft("REMAINING", widths.remaining),
		padRight("WASTE", widths.waste),
		padLeft("RAW UNUSED", widths.raw),
		padRight("CONF", widths.conf),
	}
	return colorize(strings.Join(parts, "  "), ansiDim, color)
}

func expiringUsageLine(row expiringUsageRow, widths expiringUsageWidths, color bool) string {
	urgency := colorize(padRight(row.urgency, widths.urgency), urgencyANSI(row.urgency), color)
	reset := colorize(padLeft(row.reset, widths.reset), resetANSI(row.urgency), color)
	parts := []string{
		urgency,
		reset,
		colorize(padRight(row.subject, widths.subject), ansiBold, color),
		colorize(padLeft(row.remaining, widths.remaining), ansiGreen, color),
		colorize(padRight(row.waste, widths.waste), ansiYellow, color),
		colorize(padLeft(row.raw, widths.raw), ansiDim, color),
		colorize(padRight(row.conf, widths.conf), confidenceANSI(row.conf), color),
	}
	return strings.Join(parts, "  ")
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func padLeft(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
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

func shouldColor(w io.Writer) bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	if strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	if force := os.Getenv("CLICOLOR_FORCE"); force != "" && force != "0" {
		return true
	}
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiRed     = "\x1b[31m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiMagenta = "\x1b[35m"
	ansiCyan    = "\x1b[36m"
)

func colorize(s, code string, enabled bool) string {
	if !enabled || code == "" {
		return s
	}
	return code + s + ansiReset
}

func urgencyANSI(urgency string) string {
	switch strings.ToLower(urgency) {
	case "extreme", "critical":
		return ansiBold + ansiRed
	case "high":
		return ansiMagenta
	case "normal", "medium":
		return ansiYellow
	case "low":
		return ansiDim
	default:
		return ""
	}
}

func resetANSI(urgency string) string {
	switch strings.ToLower(urgency) {
	case "extreme", "critical", "high":
		return urgencyANSI(urgency)
	default:
		return ""
	}
}

func confidenceANSI(confidence string) string {
	switch strings.ToLower(confidence) {
	case "high":
		return ansiGreen
	case "medium":
		return ansiCyan
	case "low":
		return ansiYellow
	default:
		return ansiDim
	}
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
