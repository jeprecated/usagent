package cursor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"time"

	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/model"
	"github.com/jeprecated/usagent/internal/providers"
	"github.com/jeprecated/usagent/internal/ratelimit"
)

const defaultEndpoint = "https://api2.cursor.sh/aiserver.v1.DashboardService/GetCurrentPeriodUsage"

type Provider struct {
	cfg    config.CursorConfig
	client *http.Client
}

func New(cfg config.CursorConfig) *Provider { return NewWithClient(cfg, http.DefaultClient) }
func NewWithClient(cfg config.CursorConfig, client *http.Client) *Provider {
	if client == nil {
		client = http.DefaultClient
	}
	return &Provider{cfg: cfg, client: client}
}
func (p *Provider) ID() string    { return "cursor" }
func (p *Provider) Label() string { return "Cursor" }

type payload struct {
	BillingCycleEnd int64      `json:"billingCycleEnd,string"`
	PlanUsage       *planUsage `json:"planUsage"`
}

type planUsage struct {
	AutoPercentUsed *float64 `json:"autoPercentUsed"`
	APIPercentUsed  *float64 `json:"apiPercentUsed"`
}

func (p *Provider) Fetch(ctx context.Context, now time.Time) (providers.Result, error) {
	token, err := p.token()
	if err != nil {
		return providers.Result{}, err
	}
	endpoint := p.cfg.EndpointURL
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString("{}"))
	if err != nil {
		return providers.Result{}, err
	}
	req.Header.Set("authorization", "Bearer "+token)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("connect-protocol-version", "1")
	resp, err := p.client.Do(req)
	if err != nil {
		return providers.Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retry := ratelimit.RetryAfter(resp.Header, now)
		return providers.Result{RetryAfter: retry}, providers.HTTPStatusError("cursor usage", resp.StatusCode, retry, resp.Body)
	}
	var pl payload
	if err := json.NewDecoder(resp.Body).Decode(&pl); err != nil {
		return providers.Result{}, err
	}
	if pl.PlanUsage == nil || pl.PlanUsage.AutoPercentUsed == nil || pl.PlanUsage.APIPercentUsed == nil {
		return providers.Result{}, errors.New("cursor usage response missing plan usage percentages")
	}
	return providers.Result{Items: Normalize(pl)}, nil
}

func (p *Provider) token() (string, error) {
	if name := p.cfg.TokenEnv; name != "" {
		if token := os.Getenv(name); token != "" {
			return token, nil
		}
	}
	data, err := os.ReadFile(p.cfg.AuthPath)
	if err != nil {
		return "", fmt.Errorf("read cursor auth: %w", err)
	}
	var auth struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(data, &auth); err != nil {
		return "", fmt.Errorf("parse cursor auth: %w", err)
	}
	if auth.AccessToken == "" {
		return "", errors.New("cursor access token missing: set CURSOR_ACCESS_TOKEN or log in to Cursor")
	}
	return auth.AccessToken, nil
}

func Normalize(pl payload) []model.QuotaItem {
	if pl.PlanUsage == nil {
		return nil
	}
	reset := pl.BillingCycleEnd
	window := model.Window{ID: "monthly", Label: "M", Kind: "monthly"}
	if reset > 0 {
		window.ResetAt = &reset
	}
	return []model.QuotaItem{
		item("cursor-models", "Cursor Models (Grok 4.5)", *pl.PlanUsage.AutoPercentUsed, window),
		item("cursor-other-models", "Cursor Other Models", *pl.PlanUsage.APIPercentUsed, window),
	}
}

func item(id, label string, used float64, window model.Window) model.QuotaItem {
	used = math.Round(math.Max(0, math.Min(100, used))*100) / 100
	item := model.QuotaItem{ID: id, Provider: "cursor", Label: label, Window: window, Unit: "percent", Limit: 100, Used: used, Remaining: 100 - used, PercentUsed: used, State: "fresh", Severity: "ok", Visible: true}
	if window.ResetAt != nil {
		item.Reset = &model.Reset{ResetAt: *window.ResetAt, ResetWindowID: window.ID, Source: "provider"}
	}
	return item
}
