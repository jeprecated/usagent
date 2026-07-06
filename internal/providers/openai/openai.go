package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jmalloc/usagent/internal/config"
	"github.com/jmalloc/usagent/internal/model"
	"github.com/jmalloc/usagent/internal/providers"
	"github.com/jmalloc/usagent/internal/ratelimit"
)

const (
	defaultBaseURL       = "https://api.openai.com"
	defaultCostsEndpoint = "https://api.openai.com/v1/organization/costs"
	maxPages             = 20
)

type Provider struct {
	cfg    config.OpenAIConfig
	client *http.Client
}

func New(cfg config.OpenAIConfig) *Provider { return NewWithClient(cfg, http.DefaultClient) }
func NewWithClient(cfg config.OpenAIConfig, c *http.Client) *Provider {
	if c == nil {
		c = http.DefaultClient
	}
	return &Provider{cfg: cfg, client: c}
}
func (p *Provider) ID() string    { return "openai" }
func (p *Provider) Label() string { return "OpenAI" }

type costsPayload struct {
	Data     []costBucket `json:"data"`
	NextPage string       `json:"next_page"`
}

type costBucket struct {
	StartTime int64        `json:"start_time"`
	EndTime   int64        `json:"end_time"`
	Results   []costResult `json:"results"`
}

type costResult struct {
	Amount struct {
		Value    float64 `json:"value"`
		Currency string  `json:"currency"`
	} `json:"amount"`
}

func (p *Provider) Fetch(ctx context.Context, now time.Time) (providers.Result, error) {
	keyEnv := p.cfg.APIKeyEnv
	if keyEnv == "" {
		keyEnv = "OPENAI_ADMIN_KEY"
	}
	apiKey := os.Getenv(keyEnv)
	if apiKey == "" {
		return providers.Result{}, fmt.Errorf("openai admin API key missing: set %s", keyEnv)
	}
	if len(p.cfg.Budgets) == 0 {
		return providers.Result{Items: []model.QuotaItem{}}, nil
	}
	start, end := budgetRange(now, p.cfg.Budgets)
	buckets, retry, err := p.fetchCosts(ctx, now, apiKey, start, end)
	if err != nil {
		return providers.Result{RetryAfter: retry}, err
	}
	items := make([]model.QuotaItem, 0, len(p.cfg.Budgets))
	for _, b := range p.cfg.Budgets {
		budgetStart, budgetEnd := windowBounds(now, b)
		items = append(items, NormalizeBudget(b, sumCosts(buckets, b.Unit, budgetStart, budgetEnd)))
	}
	return providers.Result{Items: items}, nil
}

func (p *Provider) fetchCosts(ctx context.Context, now time.Time, apiKey string, start, end time.Time) ([]costBucket, time.Duration, error) {
	endpoint := p.costsEndpoint()
	buckets := []costBucket{}
	page := ""
	seen := map[string]bool{}
	for pages := 0; pages < maxPages; pages++ {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, 0, err
		}
		q := u.Query()
		q.Set("start_time", fmt.Sprintf("%d", start.Unix()))
		q.Set("end_time", fmt.Sprintf("%d", end.Unix()))
		q.Set("bucket_width", "1d")
		q.Set("limit", fmt.Sprintf("%d", costsLimit(start, end)))
		for _, v := range p.cfg.ProjectIDs {
			q.Add("project_ids[]", v)
		}
		for _, v := range p.cfg.APIKeyIDs {
			q.Add("api_key_ids[]", v)
		}
		for _, v := range p.cfg.GroupBy {
			q.Add("group_by[]", v)
		}
		if page != "" {
			q.Set("page", page)
		}
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("authorization", "Bearer "+apiKey)
		req.Header.Set("accept", "application/json")
		resp, err := p.client.Do(req)
		if err != nil {
			return nil, 0, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			retry := ratelimit.RetryAfter(resp.Header, now)
			err := providers.HTTPStatusError("openai costs", resp.StatusCode, retry, resp.Body)
			_ = resp.Body.Close()
			return nil, retry, err
		}
		var payload costsPayload
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			_ = resp.Body.Close()
			return nil, 0, err
		}
		if err := resp.Body.Close(); err != nil {
			return nil, 0, err
		}
		buckets = append(buckets, payload.Data...)
		if payload.NextPage == "" {
			return buckets, 0, nil
		}
		if seen[payload.NextPage] {
			return buckets, 0, errors.New("openai costs pagination loop detected")
		}
		seen[payload.NextPage] = true
		page = payload.NextPage
	}
	return buckets, 0, fmt.Errorf("openai costs exceeded pagination limit %d", maxPages)
}

func costsLimit(start, end time.Time) int {
	days := int(end.Sub(start).Hours()/24) + 1
	if days < 1 {
		return 1
	}
	if days > 180 {
		return 180
	}
	return days
}

func (p *Provider) costsEndpoint() string {
	if p.cfg.CostsEndpoint != "" {
		return p.cfg.CostsEndpoint
	}
	if p.cfg.BaseURL != "" {
		return strings.TrimRight(p.cfg.BaseURL, "/") + "/v1/organization/costs"
	}
	return defaultCostsEndpoint
}

func sumCosts(buckets []costBucket, unit string, start, end time.Time) float64 {
	var used float64
	want := strings.ToLower(unit)
	for _, bucket := range buckets {
		if !bucketOverlapsWindow(bucket, start, end) {
			continue
		}
		for _, result := range bucket.Results {
			currency := strings.ToLower(result.Amount.Currency)
			if want == "" || currency == "" || currency == want {
				used += result.Amount.Value
			}
		}
	}
	return used
}

func bucketOverlapsWindow(bucket costBucket, start, end time.Time) bool {
	if bucket.StartTime == 0 && bucket.EndTime == 0 {
		return true
	}
	bucketStart := time.Unix(bucket.StartTime, 0)
	bucketEnd := time.Unix(bucket.EndTime, 0)
	if bucket.EndTime == 0 {
		bucketEnd = bucketStart.Add(24 * time.Hour)
	}
	return bucketEnd.After(start) && bucketStart.Before(end)
}

func NormalizeBudget(b config.Budget, used float64) model.QuotaItem {
	unit := b.Unit
	if unit == "" {
		unit = "usd"
	}
	windowID, windowLabel, windowKind := windowMetadata(b)
	remaining := math.Max(0, b.Limit-used)
	percent := 0.0
	if b.Limit > 0 {
		percent = clampPercent((used / b.Limit) * 100)
	}
	id := b.ID
	if id == "" {
		id = windowID + "-" + unit
	}
	label := "OpenAI " + windowLabel
	return model.QuotaItem{ID: "openai-cost-" + id, Provider: "openai", Label: label, Window: model.Window{ID: windowID, Label: windowLabel, Kind: windowKind}, Unit: unit, Limit: b.Limit, Used: round2(used), Remaining: round2(remaining), PercentUsed: percent, State: "fresh", Severity: "ok", Visible: true}
}

func windowMetadata(b config.Budget) (id, label, kind string) {
	id = stringFromMap(b.Window, "id")
	kind = stringFromMap(b.Window, "kind")
	label = stringFromMap(b.Window, "label")
	if kind == "" {
		kind = id
	}
	if id == "" {
		id = kind
	}
	if id == "" {
		id = b.ID
	}
	if kind == "" {
		kind = "custom"
	}
	if label == "" {
		switch kind {
		case "weekly":
			label = "W"
		case "monthly":
			label = "M"
		default:
			label = id
		}
	}
	return id, label, kind
}

func budgetRange(now time.Time, budgets []config.Budget) (time.Time, time.Time) {
	if len(budgets) == 0 {
		return now, now
	}
	start, end := windowBounds(now, budgets[0])
	for _, b := range budgets[1:] {
		bStart, bEnd := windowBounds(now, b)
		if bStart.Before(start) {
			start = bStart
		}
		if bEnd.After(end) {
			end = bEnd
		}
	}
	return start, end
}

func windowBounds(now time.Time, b config.Budget) (time.Time, time.Time) {
	_, _, kind := windowMetadata(b)
	switch kind {
	case "monthly":
		return now.AddDate(0, -1, 0), now
	case "weekly":
		return now.AddDate(0, 0, -7), now
	default:
		return now.AddDate(0, 0, -1), now
	}
}

func stringFromMap(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[k]; ok {
		return fmt.Sprint(v)
	}
	return ""
}

func clampPercent(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return math.Round(v)
}
func round2(v float64) float64 { return math.Round(v*100) / 100 }
