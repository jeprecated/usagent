package cliproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/model"
	"github.com/jeprecated/usagent/internal/providers/chatgpt"
	"github.com/jeprecated/usagent/internal/providers/claude"
)

// Upstream requests mirror the CLIProxyAPI Management Center quota page.

func (p *Provider) fetchClaude(ctx context.Context, key string, a account, now time.Time) ([]model.QuotaItem, error) {
	body, err := p.upstream(ctx, key, a, upstreamRequest{
		label: "claude oauth usage", method: http.MethodGet,
		url: claude.UsageURL("https://api.anthropic.com/api/oauth/usage"),
		header: map[string]string{
			"Authorization":     "Bearer $TOKEN$",
			"anthropic-beta":    "oauth-2025-04-20",
			"anthropic-version": "2023-06-01",
			"Accept":            "application/json",
			"User-Agent":        config.DefaultClaudeOAuthUserAgent,
		},
	})
	if err != nil {
		return nil, err
	}
	items, err := claude.NormalizeJSON(body, now, p.cfg.RefreshMs, p.cfg.StaleMs)
	if err != nil {
		return nil, errors.New("claude oauth usage returned invalid JSON")
	}
	return adopt(a, items, "claude-code-", "Claude"), nil
}

func (p *Provider) fetchCodex(ctx context.Context, key string, a account, now time.Time) ([]model.QuotaItem, error) {
	header := map[string]string{"Authorization": "Bearer $TOKEN$", "Accept": "application/json", "User-Agent": "usagent/0.1"}
	if id := strings.TrimSpace(a.IDToken.ChatGPTAccountID); id != "" {
		header["chatgpt-account-id"] = id
	}
	body, err := p.upstream(ctx, key, a, upstreamRequest{label: "codex usage", method: http.MethodGet, url: "https://chatgpt.com/backend-api/wham/usage", header: header})
	if err != nil {
		return nil, err
	}
	items, err := chatgpt.NormalizeJSON(body, now, p.cfg.RefreshMs, p.cfg.StaleMs)
	if err != nil {
		return nil, errors.New("codex usage returned invalid JSON")
	}
	return adopt(a, items, "chatgpt-", "ChatGPT"), nil
}

var antigravityQuotaURLs = []string{
	"https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
	"https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:retrieveUserQuotaSummary",
	"https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
}

func (p *Provider) fetchAntigravity(ctx context.Context, key string, a account, now time.Time) ([]model.QuotaItem, error) {
	project := strings.TrimSpace(a.ProjectID)
	if project == "" {
		return nil, errors.New("antigravity credential has no project_id")
	}
	data, _ := json.Marshal(map[string]string{"project": project})
	header := map[string]string{
		"Authorization": "Bearer $TOKEN$",
		"Content-Type":  "application/json",
		"User-Agent":    "antigravity/cli/1.0.13 (aidev_client; os_type=darwin; arch=arm64)",
	}
	var lastErr error
	for _, endpoint := range antigravityQuotaURLs {
		payload, err := p.upstreamJSON(ctx, key, a, upstreamRequest{label: "antigravity quota", method: http.MethodPost, url: endpoint, header: header, data: string(data)})
		if err != nil {
			lastErr = err
			continue
		}
		if items := antigravityItems(a, payload, now); len(items) > 0 {
			return items, nil
		}
		lastErr = errors.New("antigravity quota returned no buckets")
	}
	return nil, lastErr
}

func antigravityItems(a account, payload map[string]any, now time.Time) []model.QuotaItem {
	if _, ok := payload["models"]; !ok {
		if inner := obj(payload["body"]); inner != nil {
			payload = inner
		}
	}
	items := []model.QuotaItem{}
	for gi, g := range list(payload["groups"]) {
		group := obj(g)
		groupLabel := firstNonEmpty(text(field(group, "displayName", "display_name")), fmt.Sprintf("Group %d", gi+1))
		for bi, b := range list(group["buckets"]) {
			bucket := obj(b)
			remaining, ok := fraction(field(bucket, "remainingFraction", "remaining_fraction"))
			if !ok {
				continue
			}
			window := strings.ToLower(text(bucket["window"]))
			id := firstNonEmpty(text(field(bucket, "bucketId", "bucket_id")), fmt.Sprintf("%s-%s-%d", groupLabel, window, bi+1))
			label := firstNonEmpty(text(field(bucket, "displayName", "display_name")), text(bucket["description"]), window, id)
			if !strings.EqualFold(label, groupLabel) {
				label = groupLabel + " " + label
			}
			w := windowOther
			switch window {
			case "5h", "five-hour", "five_hour":
				w = windowSession
			case "weekly", "week":
				w = windowWeekly
			}
			items = append(items, percentItem(a, id, label, w, (1-remaining)*100, timeMs(field(bucket, "resetTime", "reset_time")), now))
		}
	}
	return items
}

func (p *Provider) fetchDevin(ctx context.Context, key string, a account, now time.Time) ([]model.QuotaItem, error) {
	data := `{"metadata":{"ideName":"chisel","ideVersion":"3000.10.21","apiKey":"$TOKEN$","locale":"en","os":"darwin","extensionVersion":"3000.10.21","clientName":"chisel"}}`
	payload, err := p.upstreamJSON(ctx, key, a, upstreamRequest{
		label: "devin user status", method: http.MethodPost,
		url:    "https://server.codeium.com/exa.seat_management_pb.SeatManagementService/GetUserStatus",
		header: map[string]string{"Content-Type": "application/json", "Connect-Protocol-Version": "1"},
		data:   data,
	})
	if err != nil {
		return nil, err
	}
	plan := obj(obj(payload["userStatus"])["planStatus"])
	items := []model.QuotaItem{}
	for _, period := range []struct {
		id string
		w  model.Window
	}{{"daily", windowDaily}, {"weekly", windowWeekly}} {
		remaining, ok := number(plan[period.id+"QuotaRemainingPercent"])
		if !ok {
			continue
		}
		items = append(items, percentItem(a, period.id, period.id, period.w, 100-remaining, timeMs(plan[period.id+"QuotaResetAtUnix"]), now))
	}
	return items, nil
}

var kimiAIName = regexp.MustCompile(`(?i)kimi-ai|kimi\.ai`)

func (p *Provider) fetchKimi(ctx context.Context, key string, a account, now time.Time) ([]model.QuotaItem, error) {
	host := "api.kimi.com"
	raw := strings.ToLower(firstNonEmpty(a.Provider, a.Type))
	if raw == "kimi-ai" || raw == "kimi_ai" || kimiAIName.MatchString(a.Name) {
		host = "api.kimi.ai"
	}
	payload, err := p.upstreamJSON(ctx, key, a, upstreamRequest{label: "kimi usage", method: http.MethodGet, url: "https://" + host + "/coding/v1/usages", header: map[string]string{"Authorization": "Bearer $TOKEN$"}})
	if err != nil {
		return nil, err
	}
	return kimiItems(a, payload, now), nil
}

func kimiItems(a account, payload map[string]any, now time.Time) []model.QuotaItem {
	items := []model.QuotaItem{}
	add := func(id, fallbackLabel string, data map[string]any, hours float64) {
		limit, hasLimit := number(data["limit"])
		used, hasUsed := number(data["used"])
		if !hasUsed {
			if remaining, ok := number(data["remaining"]); ok && hasLimit {
				used, hasUsed = limit-remaining, true
			}
		}
		if !hasUsed || !hasLimit || limit <= 0 {
			return
		}
		label := firstNonEmpty(text(data["name"]), text(data["title"]), fallbackLabel)
		if hours <= 0 {
			hours = periodHoursFromText(label)
		}
		reset := timeMs(field(data, "reset_at", "resetAt", "reset_time", "resetTime"))
		if reset == nil {
			if secs, ok := number(field(data, "reset_in", "resetIn", "ttl")); ok && secs > 0 {
				ms := now.Add(time.Duration(secs) * time.Second).UnixMilli()
				reset = &ms
			}
		}
		items = append(items, percentItem(a, id, label, windowForHours(hours), used/limit*100, reset, now))
	}
	for i, raw := range list(payload["limits"]) {
		item := obj(raw)
		detail := obj(item["detail"])
		if detail == nil {
			detail = item
		}
		window := obj(item["window"])
		duration, _ := number(firstPresent(field(window, "duration"), field(item, "duration"), field(detail, "duration")))
		unit := text(firstPresent(field(window, "timeUnit"), field(item, "timeUnit"), field(detail, "timeUnit")))
		label := firstNonEmpty(text(field(item, "name", "title", "scope")), text(field(detail, "name", "title", "scope")))
		hours := kimiHours(duration, unit)
		if label == "" && hours > 0 {
			label = formatHours(hours)
		}
		add(fmt.Sprintf("limit-%d", i), firstNonEmpty(label, fmt.Sprintf("limit %d", i+1)), detail, hours)
	}
	if usage := obj(payload["usage"]); usage != nil {
		add("summary", "weekly", usage, 0)
	}
	if monthly := obj(obj(payload["usages"])["limit_month_total"]); monthly != nil {
		if ratio, ok := number(monthly["used_ratio"]); ok {
			add("monthly", "monthly", map[string]any{"used": ratio * 100, "limit": 100.0, "reset_time": monthly["reset_time"]}, 0)
		}
	}
	return items
}

// kimiHours treats a missing or unknown unit as minutes, as Kimi sends
// protobuf-style units such as TIME_UNIT_MINUTE.
func kimiHours(duration float64, unit string) float64 {
	if duration <= 0 {
		return 0
	}
	switch strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(unit)), "TIME_UNIT_") {
	case "SECOND", "SECONDS":
		return duration / 3600
	case "HOUR", "HOURS":
		return duration
	case "DAY", "DAYS":
		return duration * 24
	case "WEEK", "WEEKS":
		return duration * 24 * 7
	}
	return duration / 60
}

func periodHoursFromText(label string) float64 {
	text := strings.ToLower(label)
	switch {
	case strings.Contains(text, "day"):
		return 24
	case strings.Contains(text, "week"):
		return 24 * 7
	case strings.Contains(text, "month"):
		return 24 * 30
	case strings.Contains(text, "5h"), strings.Contains(text, "hour"):
		return 5
	}
	return 0
}

func formatHours(hours float64) string {
	if hours < 1 {
		return strconv.FormatFloat(hours*60, 'f', -1, 64) + "m"
	}
	return strconv.FormatFloat(hours, 'f', -1, 64) + "h"
}

var metaToken = regexp.MustCompile(`^dca:\S+$`)

// Meta authenticates with the credential's dca_token rather than the token
// CLIProxyAPI substitutes, so it is read from the downloaded credential and
// kept only for this request.
func (p *Provider) fetchMeta(ctx context.Context, key string, a account, now time.Time) ([]model.QuotaItem, error) {
	if a.RuntimeOnly || strings.TrimSpace(a.Name) == "" {
		return nil, errors.New("meta credential is not file-backed")
	}
	var file map[string]any
	if _, err := p.management(ctx, key, http.MethodGet, "/credentials/download?name="+queryEscape(a.Name), nil, &file, now); err != nil {
		return nil, err
	}
	token := strings.TrimSpace(text(file["dca_token"]))
	if !metaToken.MatchString(token) {
		return nil, errors.New("meta credential has no dca_token")
	}
	payload, err := p.upstreamJSON(ctx, key, a, upstreamRequest{
		label: "meta usage", method: http.MethodPost, url: "https://api.meta.ai/muse-code/key", data: "{}",
		header: map[string]string{"Accept": "application/json", "Content-Type": "application/json", "Authorization": "Bearer " + token, "x-api-version": "1.0.0"},
	})
	if err != nil {
		return nil, err
	}
	usage := obj(payload["subs_usage"])
	items := []model.QuotaItem{}
	if window := obj(usage["window"]); window != nil {
		if used, ok := number(window["used_percent"]); ok {
			minutes, _ := number(window["window_duration_mins"])
			w := windowSession
			if minutes > 0 {
				w = windowForHours(minutes / 60)
			}
			items = append(items, percentItem(a, "window", formatHours(minutes/60), w, used, timeMs(window["resets_at"]), now))
		}
	}
	if weekly := obj(usage["weekly"]); weekly != nil {
		if used, ok := number(weekly["used_percent"]); ok {
			items = append(items, percentItem(a, "weekly", "weekly", windowWeekly, used, timeMs(weekly["resets_at"]), now))
		}
	}
	return items, nil
}

func (p *Provider) fetchXAI(ctx context.Context, key string, a account, now time.Time) ([]model.QuotaItem, error) {
	header := map[string]string{
		"Authorization":         "Bearer $TOKEN$",
		"x-xai-token-auth":      "xai-grok-cli",
		"x-grok-client-version": "1.0.46",
		"accept":                "*/*",
		"user-agent":            "grok-pager/1.0.46 grok-shell/1.0.46 (macos; aarch64)",
	}
	items := []model.QuotaItem{}
	var firstErr error
	for _, endpoint := range []struct{ suffix, url string }{
		{"credits", "https://cli-chat-proxy.grok.com/v1/billing?format=credits"},
		{"billing", "https://cli-chat-proxy.grok.com/v1/billing"},
	} {
		payload, err := p.upstreamJSON(ctx, key, a, upstreamRequest{label: "grok billing", method: http.MethodGet, url: endpoint.url, header: header})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if item, ok := xaiItem(a, endpoint.suffix, obj(payload["config"]), now); ok {
			items = append(items, item)
		}
	}
	if len(items) == 0 && firstErr != nil {
		return nil, firstErr
	}
	// Paid API-tier accounts report no subscription billing; the Management
	// Center only probes them with a chat completion, which would spend tokens.
	if len(items) == 0 {
		return nil, errors.New("grok billing reported no subscription quota")
	}
	return items, nil
}

// xaiItem reads either the credit-based weekly view or the monthly cents view.
func xaiItem(a account, suffix string, cfg map[string]any, now time.Time) (model.QuotaItem, bool) {
	if cfg == nil {
		return model.QuotaItem{}, false
	}
	period := obj(field(cfg, "currentPeriod", "current_period"))
	start := firstPresent(field(period, "start"), field(cfg, "billingPeriodStart", "billing_period_start"))
	end := firstPresent(field(period, "end"), field(cfg, "billingPeriodEnd", "billing_period_end"))
	reset := timeMs(end)
	hours := 0.0
	if startMs := timeMs(start); startMs != nil && reset != nil && *reset > *startMs {
		hours = float64(*reset-*startMs) / float64(time.Hour/time.Millisecond)
	}
	periodType := strings.ToLower(text(field(period, "type")))
	w := windowForHours(hours)
	if strings.Contains(periodType, "weekly") {
		w = windowWeekly
	} else if strings.Contains(periodType, "monthly") {
		w = windowMonthly
	}
	if used, ok := number(field(cfg, "creditUsagePercent", "credit_usage_percent")); ok {
		if w == windowOther {
			w = windowWeekly
		}
		return percentItem(a, suffix, "credits", w, used, reset, now), true
	}
	limit, hasLimit := cents(field(cfg, "monthlyLimit", "monthly_limit"))
	used, hasUsed := cents(cfg["used"])
	if !hasLimit || !hasUsed || limit <= 0 {
		return model.QuotaItem{}, false
	}
	if w == windowOther {
		w = windowMonthly
	}
	return percentItem(a, suffix, "monthly", w, min(used, limit)/limit*100, reset, now), true
}

func cents(v any) (float64, bool) {
	if m := obj(v); m != nil {
		return number(m["val"])
	}
	return number(v)
}

// Loose JSON helpers: these upstreams mix camelCase/snake_case and send
// numbers as strings.

func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func field(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

func firstPresent(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func text(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	}
	return 0, false
}

// fraction accepts 0..1 numbers, numeric strings, or "NN%".
func fraction(v any) (float64, bool) {
	if f, ok := number(v); ok {
		return f, true
	}
	if s := text(v); strings.HasSuffix(s, "%") {
		f, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
		return f / 100, err == nil
	}
	return 0, false
}

// timeMs parses RFC3339 strings or Unix seconds/milliseconds.
func timeMs(v any) *int64 {
	if s := text(v); s != "" {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			ms := t.UnixMilli()
			return &ms
		}
	}
	n, ok := number(v)
	if !ok || n <= 0 {
		return nil
	}
	if n < 1e11 {
		n *= 1000
	}
	ms := int64(n)
	return &ms
}
