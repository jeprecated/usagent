package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/model"
	"github.com/jeprecated/usagent/internal/providers"
	"github.com/jeprecated/usagent/internal/ratelimit"
)

type Provider struct {
	cfg    config.CustomProviderConfig
	client *http.Client
}

func New(cfg config.CustomProviderConfig) *Provider { return NewWithClient(cfg, http.DefaultClient) }
func NewWithClient(cfg config.CustomProviderConfig, c *http.Client) *Provider {
	if c == nil {
		c = http.DefaultClient
	}
	return &Provider{cfg: cfg, client: c}
}
func (p *Provider) ID() string { return p.cfg.ID }
func (p *Provider) Label() string {
	if p.cfg.Label != "" {
		return p.cfg.Label
	}
	return p.cfg.ID
}

func (p *Provider) Fetch(ctx context.Context, now time.Time) (providers.Result, error) {
	items := []model.QuotaItem{}
	for _, ep := range p.cfg.Endpoints {
		epItems, retry, err := p.fetchEndpoint(ctx, now, ep)
		if err != nil {
			return providers.Result{RetryAfter: retry}, err
		}
		items = append(items, epItems...)
	}
	return providers.Result{Items: items}, nil
}

func (p *Provider) fetchEndpoint(ctx context.Context, now time.Time, ep config.CustomEndpointConfig) ([]model.QuotaItem, time.Duration, error) {
	method := ep.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if ep.Body != "" {
		body = bytes.NewBufferString(ep.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, ep.URL, body)
	if err != nil {
		return nil, 0, err
	}
	for k, v := range ep.Headers {
		req.Header.Set(k, v)
	}
	if err := applyAuth(req, ep.Auth); err != nil {
		return nil, 0, err
	}
	if req.Header.Get("accept") == "" {
		req.Header.Set("accept", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retry := ratelimit.RetryAfter(resp.Header, now)
		return nil, retry, providers.HTTPStatusError(fmt.Sprintf("custom provider %s endpoint %s", p.ID(), ep.ID), resp.StatusCode, retry, resp.Body)
	}
	var root any
	if err := json.NewDecoder(resp.Body).Decode(&root); err != nil {
		return nil, 0, err
	}
	nodes := []any{root}
	if ep.ItemsPath != "" {
		v, ok := lookup(root, ep.ItemsPath)
		if !ok {
			return []model.QuotaItem{}, 0, nil
		}
		arr, ok := v.([]any)
		if !ok {
			return nil, 0, fmt.Errorf("custom provider %s endpoint %s itemsPath %q is not an array", p.ID(), ep.ID, ep.ItemsPath)
		}
		nodes = arr
	}
	items := make([]model.QuotaItem, 0, len(nodes))
	for _, node := range nodes {
		item, err := mapItem(p.cfg.ID, node, ep.Item)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, 0, nil
}

func applyAuth(req *http.Request, auth config.CustomAuthConfig) error {
	typ := strings.ToLower(auth.Type)
	if typ == "" || typ == "none" {
		return nil
	}
	if auth.TokenEnv == "" {
		return fmt.Errorf("custom auth %s requires tokenEnv", typ)
	}
	token := os.Getenv(auth.TokenEnv)
	if token == "" {
		return fmt.Errorf("custom auth token missing: set %s", auth.TokenEnv)
	}
	header := auth.HeaderName
	if header == "" {
		header = "Authorization"
	}
	if typ == "bearer" {
		req.Header.Set(header, "Bearer "+token)
		return nil
	}
	if typ == "header" {
		req.Header.Set(header, token)
		return nil
	}
	return fmt.Errorf("unsupported custom auth type %q", auth.Type)
}

func mapItem(providerID string, node any, m config.CustomItemMapping) (model.QuotaItem, error) {
	id := str(resolve(node, m.ID, ""))
	if id == "" {
		return model.QuotaItem{}, fmt.Errorf("custom provider %s mapped item missing id", providerID)
	}
	visible := true
	if m.Visible != nil {
		visible = boolVal(resolve(node, m.Visible, true))
	}
	resetAt := parseReset(resolve(node, m.Window.ResetAt, nil), m.Window.ResetAtFormat)
	window := model.Window{ID: str(resolve(node, m.Window.ID, "default")), Label: str(resolve(node, m.Window.Label, "")), Kind: str(resolve(node, m.Window.Kind, "custom")), ResetAt: resetAt}
	if window.Label == "" {
		window.Label = window.ID
	}
	limit := num(resolve(node, m.Limit, 0))
	used := num(resolve(node, m.Used, 0))
	remaining := num(resolve(node, m.Remaining, math.Max(0, limit-used)))
	percent := num(resolve(node, m.PercentUsed, 0))
	if m.PercentUsed == nil && limit > 0 {
		percent = used / limit * 100
	}
	item := model.QuotaItem{ID: providerID + "-" + id, Provider: providerID, Label: str(resolve(node, m.Label, id)), Window: window, Unit: str(resolve(node, m.Unit, "count")), Limit: round2(limit), Used: round2(used), Remaining: round2(math.Max(0, remaining)), PercentUsed: clampPercent(percent), State: "fresh", Severity: "ok", Visible: visible}
	if resetAt != nil {
		item.Reset = &model.Reset{ResetAt: *resetAt, ResetWindowID: window.ID, Source: "provider"}
	}
	return item, nil
}

// resolve treats strings as dot-paths by default. Prefix with "literal:" to force a string literal.
// Non-string bool/number YAML values are used as literals.
func resolve(root any, spec any, def any) any {
	if spec == nil {
		return def
	}
	s, ok := spec.(string)
	if !ok {
		return spec
	}
	if strings.HasPrefix(s, "literal:") {
		return strings.TrimPrefix(s, "literal:")
	}
	if v, ok := lookup(root, s); ok {
		return v
	}
	return def
}

func lookup(root any, path string) (any, bool) {
	if path == "" {
		return root, true
	}
	cur := root
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			continue
		}
		switch v := cur.(type) {
		case map[string]any:
			var ok bool
			cur, ok = v[part]
			if !ok {
				return nil, false
			}
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(v) {
				return nil, false
			}
			cur = v[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}
func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	default:
		return 0
	}
}
func boolVal(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		b, _ := strconv.ParseBool(x)
		return b
	default:
		return false
	}
}

func parseReset(v any, format string) *int64 {
	if v == nil {
		return nil
	}
	switch format {
	case "unixSeconds":
		ms := int64(num(v) * 1000)
		return &ms
	case "rfc3339":
		t, err := time.Parse(time.RFC3339, str(v))
		if err != nil {
			return nil
		}
		ms := t.UnixMilli()
		return &ms
	default:
		ms := int64(num(v))
		if ms <= 0 {
			return nil
		}
		return &ms
	}
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
