// Package cliproxy reads subscription quota for every account enrolled in a
// CLIProxyAPI instance. It lists credentials through the management API and
// fetches each account's own usage endpoint through management api-call, so
// CLIProxyAPI attaches (and refreshes) the token; usagent never sees it.
package cliproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/model"
	"github.com/jeprecated/usagent/internal/providers"
	"github.com/jeprecated/usagent/internal/ratelimit"
)

const (
	ProviderID       = "cliproxy"
	managementPrefix = "/v8/management"
	// Bounded so a large account pool does not burst upstream usage endpoints.
	maxConcurrentAccounts = 4
)

type Provider struct {
	cfg    config.CLIProxyConfig
	client *http.Client
}

func New(cfg config.CLIProxyConfig) *Provider { return NewWithClient(cfg, http.DefaultClient) }
func NewWithClient(cfg config.CLIProxyConfig, client *http.Client) *Provider {
	if client == nil {
		client = http.DefaultClient
	}
	return &Provider{cfg: cfg, client: client}
}
func (p *Provider) ID() string    { return ProviderID }
func (p *Provider) Label() string { return "CLIProxy" }

// credential is one entry of GET /v8/management/credentials. Never read the
// "account" field: for API-key credentials it holds the key itself.
type credential struct {
	AuthIndex   string `json:"auth_index"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Provider    string `json:"provider"`
	Label       string `json:"label"`
	Email       string `json:"email"`
	Note        string `json:"note"`
	AccountType string `json:"account_type"`
	ProjectID   string `json:"project_id"`
	Disabled    bool   `json:"disabled"`
	RuntimeOnly bool   `json:"runtime_only"`
	IDToken     struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
	} `json:"id_token"`
}

// account is a supported, enabled credential with its display identity.
type account struct {
	credential
	kind  string // normalized provider: claude, codex, antigravity, devin, kimi, meta, xai
	title string // e.g. "Claude"
	name  string // non-identifying display name
}

func (a account) itemID(suffix string) string {
	return "cliproxy-" + a.kind + "-" + stableID(a.AuthIndex) + "-" + stableID(suffix)
}

func (a account) itemLabel(window string) string {
	return strings.TrimSpace(a.title + " " + a.name + " " + window)
}

var kinds = map[string]string{
	"claude": "Claude", "codex": "Codex", "antigravity": "Antigravity", "devin": "Devin",
	"kimi": "Kimi", "meta": "Meta", "xai": "Grok",
}

func normalizeKind(raw string) string {
	kind := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), "_", "-")
	switch kind {
	case "x-ai", "grok":
		return "xai"
	case "kimi-ai":
		return "kimi"
	}
	return kind
}

func (p *Provider) Fetch(ctx context.Context, now time.Time) (providers.Result, error) {
	key, err := p.managementKey()
	if err != nil {
		return providers.Result{}, err
	}
	var listing struct {
		Files []credential `json:"files"`
	}
	if result, err := p.management(ctx, key, http.MethodGet, "/credentials", nil, &listing, now); err != nil {
		return result, err
	}
	accounts := selectAccounts(listing.Files)
	if len(accounts) == 0 {
		return providers.Result{}, errors.New("cliproxy has no enabled subscription accounts with readable quota")
	}

	results := make([][]model.QuotaItem, len(accounts))
	sem := make(chan struct{}, maxConcurrentAccounts)
	var wg sync.WaitGroup
	for i, acct := range accounts {
		wg.Add(1)
		go func(i int, acct account) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			items, err := p.fetchAccount(ctx, key, acct, now)
			if err == nil && len(items) == 0 {
				err = errors.New("no quota windows reported")
			}
			if err != nil {
				items = []model.QuotaItem{errorItem(acct, err, now)}
			}
			results[i] = items
		}(i, acct)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return providers.Result{}, err
	}

	items := []model.QuotaItem{}
	failed := 0
	for _, accountItems := range results {
		if len(accountItems) == 1 && accountItems[0].Error != nil {
			failed++
		}
		items = append(items, accountItems...)
	}
	// Keep per-account errors visible, but a fully failed pool is a provider
	// failure so cached values go stale instead of being replaced.
	if failed == len(accounts) {
		return providers.Result{}, fmt.Errorf("all %d cliproxy accounts failed: %s", failed, items[0].Error.Message)
	}
	return providers.Result{Items: items}, nil
}

func (p *Provider) fetchAccount(ctx context.Context, key string, a account, now time.Time) ([]model.QuotaItem, error) {
	switch a.kind {
	case "claude":
		return p.fetchClaude(ctx, key, a, now)
	case "codex":
		return p.fetchCodex(ctx, key, a, now)
	case "antigravity":
		return p.fetchAntigravity(ctx, key, a, now)
	case "devin":
		return p.fetchDevin(ctx, key, a, now)
	case "kimi":
		return p.fetchKimi(ctx, key, a, now)
	case "meta":
		return p.fetchMeta(ctx, key, a, now)
	case "xai":
		return p.fetchXAI(ctx, key, a, now)
	}
	return nil, fmt.Errorf("unsupported account type %q", a.kind)
}

func selectAccounts(files []credential) []account {
	accounts := []account{}
	for _, c := range files {
		kind := normalizeKind(firstNonEmpty(c.Provider, c.Type))
		if c.Disabled || c.AccountType == "api_key" || strings.TrimSpace(c.AuthIndex) == "" || kinds[kind] == "" {
			continue
		}
		name := strings.TrimSpace(c.Note)
		if name == "" {
			name = maskLabel(firstNonEmpty(c.Email, c.Label, strings.TrimSuffix(c.Name, ".json")))
		}
		accounts = append(accounts, account{credential: c, kind: kind, title: kinds[kind], name: name})
	}
	sort.SliceStable(accounts, func(i, j int) bool {
		if accounts[i].kind != accounts[j].kind {
			return accounts[i].kind < accounts[j].kind
		}
		return accounts[i].name < accounts[j].name
	})
	// Masked labels can collide; a short stable suffix keeps accounts apart.
	seen := map[string]int{}
	for _, a := range accounts {
		seen[a.kind+"\x00"+a.name]++
	}
	for i, a := range accounts {
		if seen[a.kind+"\x00"+a.name] > 1 {
			accounts[i].name = strings.TrimSpace(a.name + " #" + shortID(a.AuthIndex))
		}
	}
	return accounts
}

// maskLabel mirrors CLIProxyAPI's quota-stats masking: the daemon has no read
// auth, so it must not publish full account emails. Set a credential note in
// CLIProxyAPI to show a chosen name instead.
func maskLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	first := string([]rune(label)[0])
	if at := strings.LastIndex(label, "@"); at > 0 && at < len(label)-1 {
		return first + "***@" + label[at+1:]
	}
	return first + "***"
}

func shortID(v string) string {
	id := stableID(v)
	if len(id) > 4 {
		return id[:4]
	}
	return id
}

func (p *Provider) managementKey() (string, error) {
	if p.cfg.ManagementKeyFile != "" {
		b, err := os.ReadFile(p.cfg.ManagementKeyFile)
		if err != nil {
			return "", fmt.Errorf("read cliproxy management key file: %w", err)
		}
		if key := strings.TrimSpace(string(b)); key != "" {
			return key, nil
		}
		return "", errors.New("cliproxy management key file is empty")
	}
	if key := strings.TrimSpace(os.Getenv(p.cfg.ManagementKeyEnv)); key != "" {
		return key, nil
	}
	return "", fmt.Errorf("cliproxy management key missing; set %s or providers.cliproxy.managementKeyFile", p.cfg.ManagementKeyEnv)
}

// management calls a management route. A rejected key is reported without
// retrying quickly: CLIProxyAPI bans clients after repeated failed logins.
func (p *Provider) management(ctx context.Context, key, method, path string, body, out any, now time.Time) (providers.Result, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return providers.Result{}, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.cfg.BaseURL+managementPrefix+path, reader)
	if err != nil {
		return providers.Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Never forward the management key to another origin.
	client := *p.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return providers.Result{}, fmt.Errorf("cliproxy management: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return providers.Result{RetryAfter: time.Hour}, fmt.Errorf("cliproxy management key rejected (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retry := ratelimit.RetryAfter(resp.Header, now)
		return providers.Result{RetryAfter: retry}, providers.HTTPStatusError("cliproxy management "+path, resp.StatusCode, retry, resp.Body)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out); err != nil {
		return providers.Result{}, fmt.Errorf("decode cliproxy management %s: %w", path, err)
	}
	return providers.Result{}, nil
}

type upstreamRequest struct {
	label  string
	method string
	url    string
	header map[string]string
	data   string
}

// upstream proxies one request through management api-call. "$TOKEN$" in a
// header or the body is replaced by CLIProxyAPI with the account's token.
// Errors carry only the status: some upstream bodies echo credentials.
func (p *Provider) upstream(ctx context.Context, key string, a account, r upstreamRequest) ([]byte, error) {
	payload := map[string]any{"auth_index": a.AuthIndex, "method": r.method, "url": r.url, "header": r.header}
	if r.data != "" {
		payload["data"] = r.data
	}
	var resp struct {
		StatusCode int    `json:"status_code"`
		Body       string `json:"body"`
	}
	if _, err := p.management(ctx, key, http.MethodPost, "/requests/api-call", payload, &resp, time.Now()); err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s returned HTTP %d", r.label, resp.StatusCode)
	}
	return []byte(resp.Body), nil
}

func (p *Provider) upstreamJSON(ctx context.Context, key string, a account, r upstreamRequest) (map[string]any, error) {
	body, err := p.upstream(ctx, key, a, r)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%s returned invalid JSON", r.label)
	}
	return out, nil
}

// Window shapes shared with the built-in providers so usage views and the
// widget classify CLIProxy items the same way.
var (
	windowSession = model.Window{ID: "session", Label: "S", Kind: "rolling"}
	windowDaily   = model.Window{ID: "daily", Label: "D", Kind: "daily"}
	windowWeekly  = model.Window{ID: "weekly", Label: "W", Kind: "weekly"}
	windowMonthly = model.Window{ID: "monthly", Label: "M", Kind: "monthly"}
	windowOther   = model.Window{ID: "window", Label: "Q", Kind: "rolling"}
)

// windowForHours classifies a quota period by its length.
func windowForHours(hours float64) model.Window {
	switch {
	case hours <= 0:
		return windowOther
	case hours <= 6:
		return windowSession
	case hours <= 36:
		return windowDaily
	case hours <= 10*24:
		return windowWeekly
	case hours >= 27*24:
		return windowMonthly
	}
	return windowOther
}

func percentItem(a account, suffix, windowText string, w model.Window, usedPercent float64, resetAt *int64, now time.Time) model.QuotaItem {
	used := clampPercent(usedPercent)
	w.ResetAt = resetAt
	item := model.QuotaItem{
		ID: a.itemID(suffix), Provider: ProviderID, Label: a.itemLabel(windowText), Window: w,
		Unit: "percent", Limit: 100, Used: used, Remaining: math.Max(0, 100-used), PercentUsed: used,
		State: "fresh", Severity: severityForPercent(used), Visible: true,
	}
	if resetAt != nil {
		item.Reset = &model.Reset{ResetAt: *resetAt, ResetWindowID: w.ID, Source: "provider"}
	}
	return item
}

// errorItem keeps a failing account visible without a quota value. Error items
// are excluded from replenishment baselines, so recovery cannot raise an alert.
func errorItem(a account, err error, now time.Time) model.QuotaItem {
	return model.QuotaItem{
		ID: a.itemID("status"), Provider: ProviderID, Label: a.itemLabel(""),
		Window: model.Window{ID: "status", Label: "-", Kind: "status"},
		State:  "error", Severity: "error", Visible: true,
		Error: &model.ItemError{Provider: ProviderID, Code: "account_refresh_failed", Message: err.Error(), LastOccurredAt: now.UnixMilli(), Recoverable: true},
	}
}

// adopt rebrands items parsed by a built-in provider's normalizer as this
// account's items.
func adopt(a account, items []model.QuotaItem, idPrefix, labelPrefix string) []model.QuotaItem {
	for i := range items {
		items[i].Provider = ProviderID
		items[i].ID = a.itemID(strings.TrimPrefix(items[i].ID, idPrefix))
		label := strings.TrimSpace(items[i].Label)
		if strings.HasPrefix(strings.ToLower(label), strings.ToLower(labelPrefix)+" ") {
			label = strings.TrimSpace(label[len(labelPrefix):])
		}
		items[i].Label = a.itemLabel(label)
	}
	return items
}

func clampPercent(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return math.Round(v*100) / 100
}

func severityForPercent(v float64) string {
	if v >= 100 {
		return "critical"
	}
	if v >= 80 {
		return "warning"
	}
	return "ok"
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func stableID(v string) string {
	return strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(v), "-"), "-")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func queryEscape(v string) string { return url.QueryEscape(v) }
