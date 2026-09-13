// Package anthropic estimates prepaid Console credits from organization costs.
// It does not read Claude subscription usage or claim to fetch a billing balance.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/model"
	"github.com/jeprecated/usagent/internal/providers"
	"github.com/jeprecated/usagent/internal/ratelimit"
)

const maxPages = 100

type Provider struct {
	cfg    config.AnthropicConfig
	client *http.Client
}

func New(cfg config.AnthropicConfig) *Provider { return NewWithClient(cfg, http.DefaultClient) }
func NewWithClient(cfg config.AnthropicConfig, client *http.Client) *Provider {
	if client == nil {
		client = http.DefaultClient
	}
	// Never forward an organization admin key through a redirect, including one
	// to the same hostname. Copy the caller's client instead of mutating it.
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("anthropic costs redirect refused")
	}
	if c.Timeout == 0 {
		c.Timeout = 30 * time.Second
	}
	if cfg.CostsEndpoint == "" {
		cfg.CostsEndpoint = config.DefaultAnthropicCostsEndpoint
	}
	if cfg.APIKeyEnv == "" {
		cfg.APIKeyEnv = "ANTHROPIC_ADMIN_KEY"
	}
	return &Provider{cfg: cfg, client: &c}
}
func (p *Provider) ID() string    { return "anthropic" }
func (p *Provider) Label() string { return "Anthropic API" }

type costReport struct {
	Data     *[]costBucket `json:"data"`
	HasMore  *bool         `json:"has_more"`
	NextPage string        `json:"next_page"`
}
type costBucket struct {
	StartingAt time.Time   `json:"starting_at"`
	EndingAt   time.Time   `json:"ending_at"`
	Results    *[]costItem `json:"results"`
}
type costItem struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

func (p *Provider) Fetch(ctx context.Context, now time.Time) (providers.Result, error) {
	if err := p.cfg.Validate(); err != nil {
		return providers.Result{}, err
	}
	checkpoint := p.cfg.Checkpoint
	start, _ := time.Parse(time.RFC3339, checkpoint.ReportStart)
	if start.After(now) {
		return providers.Result{}, errors.New("anthropic checkpoint reportStart is in the future")
	}
	key, err := p.apiKey()
	if err != nil {
		return providers.Result{}, err
	}
	// Include today's partial bucket and keep the same range across pages.
	end := now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	total, retry, err := p.fetchCosts(ctx, now, key, start.UTC(), end)
	if err != nil {
		return providers.Result{RetryAfter: retry}, err
	}
	used := total - *checkpoint.ReportedCostUSD
	if used < -1e-9 {
		return providers.Result{}, errors.New("anthropic reported cost is below checkpoint.reportedCostUSD; check organization/reportStart or reconcile the checkpoint")
	}
	used = math.Max(0, used)
	balance := *checkpoint.BalanceUSD
	remaining := math.Max(0, balance-used)
	percent := 100.0
	if balance > 0 {
		percent = math.Min(100, used/balance*100)
	}
	severity := "ok"
	if percent >= 100 {
		severity = "critical"
	} else if percent >= 80 {
		severity = "warning"
	}
	item := model.QuotaItem{
		ID: "anthropic-api-credits", Provider: p.ID(), Label: "Anthropic API credits (estimated)",
		Window: model.Window{ID: "credits", Label: "Credits", Kind: "custom"},
		Unit:   "usd", Limit: round2(balance), Used: round2(used), Remaining: round2(remaining),
		PercentUsed: math.Round(percent), State: "fresh", Severity: severity, Visible: true, Estimated: true,
	}
	return providers.Result{Items: []model.QuotaItem{item}}, nil
}

func (p *Provider) apiKey() (string, error) {
	key := os.Getenv(p.cfg.APIKeyEnv)
	if p.cfg.APIKeyFile != "" {
		// An explicit file wins, and read failures never fall back to a different
		// credential/organization from the environment. Re-read on every refresh.
		b, err := os.ReadFile(p.cfg.APIKeyFile)
		if err != nil {
			return "", fmt.Errorf("read anthropic admin API key file: %w", err)
		}
		key = string(b)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("anthropic admin API key missing: set %s or providers.anthropic.apiKeyFile", p.cfg.APIKeyEnv)
	}
	return key, nil
}

func (p *Provider) fetchCosts(ctx context.Context, now time.Time, key string, start, end time.Time) (float64, time.Duration, error) {
	var total float64
	page := ""
	seenPages := map[string]bool{}
	seenBuckets := map[time.Time]bool{}
	for n := 0; n < maxPages; n++ {
		u, err := url.Parse(p.cfg.CostsEndpoint)
		if err != nil {
			return 0, 0, err
		}
		q := u.Query()
		q.Set("starting_at", start.Format(time.RFC3339))
		q.Set("ending_at", end.Format(time.RFC3339))
		q.Set("bucket_width", "1d")
		q.Set("limit", "31")
		if page != "" {
			q.Set("page", page)
		}
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return 0, 0, err
		}
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("accept", "application/json")
		req.Header.Set("user-agent", "usagent/0.1")
		resp, err := p.client.Do(req)
		if err != nil {
			return 0, 0, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			retry := ratelimit.AnthropicRetryAfter(resp.Header, now)
			// Do not persist untrusted response bodies that could echo the key.
			err := providers.HTTPStatusError("anthropic costs", resp.StatusCode, retry, nil)
			_ = resp.Body.Close()
			return 0, retry, err
		}
		var report costReport
		err = json.NewDecoder(resp.Body).Decode(&report)
		_ = resp.Body.Close()
		if err != nil {
			return 0, 0, errors.New("decode anthropic cost report: invalid response")
		}
		if report.Data == nil || report.HasMore == nil {
			return 0, 0, errors.New("anthropic cost report missing data or has_more")
		}
		for _, bucket := range *report.Data {
			bStart := bucket.StartingAt.UTC()
			if bucket.Results == nil || bStart.Before(start) || !bStart.Before(end) ||
				!bStart.Equal(bStart.Truncate(24*time.Hour)) || !bucket.EndingAt.Equal(bStart.Add(24*time.Hour)) || seenBuckets[bStart] {
				return 0, 0, errors.New("anthropic cost report contains invalid, duplicate, or out-of-range daily bucket")
			}
			seenBuckets[bStart] = true
			for _, item := range *bucket.Results {
				if !strings.EqualFold(item.Currency, "usd") {
					return 0, 0, errors.New("anthropic cost report contains a non-USD currency")
				}
				cents, err := strconv.ParseFloat(item.Amount, 64)
				if err != nil || math.IsNaN(cents) || math.IsInf(cents, 0) || cents < 0 {
					return 0, 0, errors.New("anthropic cost report contains an invalid amount")
				}
				total += cents / 100
				if math.IsInf(total, 0) || total > math.MaxFloat64/100 {
					return 0, 0, errors.New("anthropic cost report total overflows")
				}
			}
		}
		if !*report.HasMore {
			return total, 0, nil
		}
		if report.NextPage == "" || seenPages[report.NextPage] {
			return 0, 0, errors.New("anthropic cost report pagination cursor missing or repeated")
		}
		seenPages[report.NextPage] = true
		page = report.NextPage
	}
	return 0, 0, fmt.Errorf("anthropic costs exceeded pagination limit %d; reconcile with a more recent checkpoint", maxPages)
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
