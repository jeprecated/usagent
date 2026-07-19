package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/analysis"
	"github.com/jeprecated/usagent/internal/app"
	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/model"
	"github.com/jeprecated/usagent/internal/providers"
)

func TestHTTPContract(t *testing.T) {
	cfg := config.Default()
	cfg.Providers.ClaudeOAuth.Enabled = false
	a := app.New(cfg, nil)
	a.Store.UpsertSuccess("claude-code", "Claude", []model.QuotaItem{{ID: "claude-code-oauth-session", Provider: "claude-code", Label: "Claude 5h", Window: model.Window{ID: "session", Label: "S", Kind: "rolling"}, Unit: "percent", Limit: 100, Used: 20, Remaining: 80, PercentUsed: 20, Visible: true}}, time.UnixMilli(1000), 1000, 2000)
	h := New(a, "config.example.yaml")
	for _, path := range []string{"/healthz", "/readyz", "/v1/usage", "/v1/usage/analysis", "/v1/expiring-usage", "/v1/recommendations/provider", "/v1/resets", "/v1/availability", "/v1/providers"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/config/raw", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("debug config endpoint should be absent, got %d", rec.Code)
	}
	var usage struct {
		SchemaVersion int               `json:"schemaVersion"`
		Service       string            `json:"service"`
		Providers     []model.Provider  `json:"providers"`
		QuotaItems    []model.QuotaItem `json:"quotaItems"`
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/usage", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &usage); err != nil {
		t.Fatal(err)
	}
	if usage.SchemaVersion != 2 || usage.Service != "usagent" || len(usage.Providers) != 3 || len(usage.QuotaItems) != 1 {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestExpiringUsageEndpointUsesCachedUsageAndQueryFilters(t *testing.T) {
	cfg := config.Default()
	cfg.Providers.ChatGPT.Metadata = config.ProviderMetadataConfig{Tier: "high", Tags: []string{"chat", "subscription"}, Models: map[string]config.MetadataConfig{"chatgpt-primary": {Tier: "extra-high", Tags: []string{"codex"}}}}
	cfg.Providers.ZAI.Metadata = config.ProviderMetadataConfig{Tier: "medium", Tags: []string{"api"}}
	a := app.New(cfg, nil)
	now := time.Now()
	resetAt := now.Add(time.Hour).UnixMilli()
	a.Store.UpsertSuccess("chatgpt", "ChatGPT Pro", []model.QuotaItem{
		{ID: "chatgpt-primary", Provider: "chatgpt", Label: "ChatGPT 5h", Window: model.Window{ID: "session", Label: "S", Kind: "rolling", ResetAt: &resetAt}, Unit: "percent", Limit: 100, Used: 20, Remaining: 80, PercentUsed: 20, Visible: true, Reset: &model.Reset{ResetAt: resetAt, ResetWindowID: "session", Source: "provider"}},
		{ID: "chatgpt-rate-limit-reset-credits", Provider: "chatgpt", Label: "Reset credits", Window: model.Window{ID: "reset-credits", Label: "RC", Kind: "bank", ResetAt: &resetAt}, Unit: "credits", Limit: 10, Used: 0, Remaining: 10, Visible: true, Reset: &model.Reset{ResetAt: resetAt, ResetWindowID: "reset-credits", Source: "provider"}},
	}, now, int64(time.Hour/time.Millisecond), int64((2*time.Hour)/time.Millisecond))
	a.Store.UpsertSuccess("z-ai", "z.ai", []model.QuotaItem{
		{ID: "z-ai-daily", Provider: "z-ai", Label: "z.ai Daily", Window: model.Window{ID: "daily", Label: "D", Kind: "daily", ResetAt: &resetAt}, Unit: "percent", Limit: 100, Used: 20, Remaining: 80, PercentUsed: 20, Visible: true, Reset: &model.Reset{ResetAt: resetAt, ResetWindowID: "daily", Source: "provider"}},
	}, now, int64(time.Hour/time.Millisecond), int64((2*time.Hour)/time.Millisecond))
	h := New(a, "config.example.yaml")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/expiring-usage?within=2h&minimumRemainingPercent=10&providers=chatgpt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var res analysis.ExpiringUsageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Opportunities) != 1 || res.Opportunities[0].Provider != "chatgpt" || res.Opportunities[0].ItemID != "chatgpt-primary" {
		t.Fatalf("unexpected opportunities: %+v", res.Opportunities)
	}
	if res.Opportunities[0].ProviderTier != "high" || res.Opportunities[0].ModelTier != "extra-high" || res.Opportunities[0].ModelTags[0] != "codex" {
		t.Fatalf("missing metadata in expiring usage response: %+v", res.Opportunities[0])
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/expiring-usage?within=2h&tiers=medium&tags=api", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Opportunities) != 1 || res.Opportunities[0].Provider != "z-ai" || res.Opportunities[0].ProviderTier != "medium" {
		t.Fatalf("unexpected tier/tag filtered opportunities: %+v", res.Opportunities)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/expiring-usage?withinMs=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Opportunities) != 0 {
		t.Fatalf("expected withinMs filter to exclude future reset, got %+v", res.Opportunities)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/expiring-usage?includeLowConfidence=not-bool", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad query status 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestProviderRecommendationsEndpointUsesCachedUsageAndQueryFilters(t *testing.T) {
	cfg := config.Default()
	cfg.Providers.ChatGPT.Metadata = config.ProviderMetadataConfig{Tier: "high", Tags: []string{"chat", "subscription"}, Models: map[string]config.MetadataConfig{"chatgpt-secondary": {Tier: "extra-high", Tags: []string{"codex"}}}}
	cfg.Providers.ClaudeOAuth.Metadata = config.ProviderMetadataConfig{Tier: "high", Tags: []string{"code"}, Models: map[string]config.MetadataConfig{"claude-code-oauth-session": {Tier: "high", Tags: []string{"fable"}}, "claude-code-oauth-weekly-all": {Tier: "high", Tags: []string{"fable"}}}}
	a := app.New(cfg, nil)
	now := time.Now()
	chatReset := now.Add(4 * 24 * time.Hour).UnixMilli()
	claudeSessionReset := now.Add(time.Hour).UnixMilli()
	claudeWeeklyReset := now.Add(24 * time.Hour).UnixMilli()
	a.Store.UpsertSuccess("chatgpt", "ChatGPT Pro", []model.QuotaItem{
		{ID: "chatgpt-secondary", Provider: "chatgpt", Label: "ChatGPT weekly", Window: model.Window{ID: "weekly", Label: "W", Kind: "weekly", ResetAt: &chatReset}, Unit: "percent", Limit: 100, Used: 45, Remaining: 55, PercentUsed: 45, Visible: true, Reset: &model.Reset{ResetAt: chatReset, ResetWindowID: "weekly", Source: "provider"}},
	}, now, int64(time.Hour/time.Millisecond), int64((2*time.Hour)/time.Millisecond))
	a.Store.UpsertSuccess("claude-code", "Claude", []model.QuotaItem{
		{ID: "claude-code-oauth-session", Provider: "claude-code", Label: "Claude 5h", Window: model.Window{ID: "session", Label: "S", Kind: "rolling", ResetAt: &claudeSessionReset}, Unit: "percent", Limit: 100, Used: 10, Remaining: 90, PercentUsed: 10, Visible: true, Reset: &model.Reset{ResetAt: claudeSessionReset, ResetWindowID: "session", Source: "provider"}},
		{ID: "claude-code-oauth-weekly-all", Provider: "claude-code", Label: "Claude weekly", Window: model.Window{ID: "weekly", Label: "W", Kind: "weekly", ResetAt: &claudeWeeklyReset}, Unit: "percent", Limit: 100, Used: 90, Remaining: 10, PercentUsed: 90, Visible: true, Reset: &model.Reset{ResetAt: claudeWeeklyReset, ResetWindowID: "weekly", Source: "provider"}},
	}, now, int64(time.Hour/time.Millisecond), int64((2*time.Hour)/time.Millisecond))
	h := New(a, "config.example.yaml")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/recommendations/provider?task=cheap&providers=chatgpt,claude-code&minimumRemainingPercent=20&unit=percent", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var res analysis.ProviderRecommendationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.TaskProfile != analysis.TaskProfileCheap || res.SelectedProvider != "chatgpt" || len(res.RankedCandidates) != 1 {
		t.Fatalf("unexpected recommendation response: %+v", res)
	}
	if res.RankedCandidates[0].Score <= 0 || res.RankedCandidates[0].ConstrainingItem == nil || res.RankedCandidates[0].ConstrainingItem.Unit != "percent" {
		t.Fatalf("missing candidate scores/signals: %+v", res.RankedCandidates[0])
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/recommendations/provider?tiers=extra-high&tags=codex", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("tier/tag status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.RankedCandidates) != 1 || res.SelectedProvider != "chatgpt" {
		t.Fatalf("unexpected tier/tag recommendation response: %+v", res)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/recommendations/provider?tier=high&tag=code&unit=percent", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("singular tier/tag status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.RankedCandidates) != 1 || res.SelectedProvider != "claude-code" {
		t.Fatalf("unexpected singular tier/tag recommendation response: %+v", res)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/recommendations/provider?task=bogus", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad task status 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestProviderRecommendationsEndpointUsesCachedUsageOnly(t *testing.T) {
	cfg := config.Default()
	cfg.Server.StatePath = ""
	cfg.UsageView.Providers = []string{"counted"}
	a := app.New(cfg, nil)
	provider := &countingProvider{id: "counted"}
	a.Providers = []providers.Provider{provider}
	now := time.Now()
	resetAt := now.Add(time.Hour).UnixMilli()
	// Keep the cached item fresh enough for analysis while making its refresh time
	// overdue. If the recommendation path refreshes providers instead of reading
	// the cached snapshot, the counting provider will observe the live call.
	a.Store.UpsertSuccess("counted", "Counted", []model.QuotaItem{
		{ID: "counted-session", Provider: "counted", Label: "Counted 5h", Window: model.Window{ID: "session", Label: "S", Kind: "rolling", ResetAt: &resetAt}, Unit: "percent", Limit: 100, Used: 20, Remaining: 80, PercentUsed: 20, State: "fresh", Severity: "ok", Visible: true, Reset: &model.Reset{ResetAt: resetAt, ResetWindowID: "session", Source: "provider"}},
	}, now.Add(-time.Hour), 1, int64((24*time.Hour)/time.Millisecond))
	h := New(a, "config.example.yaml")

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/recommendations/provider?providers=counted&minimumRemainingPercent=10&unit=percent", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var res analysis.ProviderRecommendationResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if res.SelectedProvider != "counted" || len(res.RankedCandidates) != 1 || res.RankedCandidates[0].ConstrainingItem == nil {
			t.Fatalf("recommendation response=%+v", res)
		}
	}
	if got := atomic.LoadInt32(&provider.count); got != 0 {
		t.Fatalf("provider recommendations triggered provider fetch count=%d", got)
	}
}

func TestResetsAndAvailabilityEndpointsUseCachedUsage(t *testing.T) {
	cfg := config.Default()
	a := app.New(cfg, nil)
	provider := &countingProvider{id: "counted"}
	a.Providers = []providers.Provider{provider}
	a.Cfg.UsageView.Providers = []string{"counted"}
	now := time.Now()
	soon := now.Add(time.Hour).UnixMilli()
	later := now.Add(2 * time.Hour).UnixMilli()
	a.Store.UpsertSuccess("counted", "Counted", []model.QuotaItem{
		{ID: "weekly", Provider: "counted", Label: "Weekly", Window: model.Window{ID: "weekly", Label: "W", Kind: "weekly", ResetAt: &later}, Unit: "percent", Limit: 100, Used: 95, Remaining: 5, PercentUsed: 95, State: "fresh", Severity: "ok", Visible: true, Reset: &model.Reset{ResetAt: later, ResetWindowID: "weekly", Source: "provider"}},
		{ID: "session", Provider: "counted", Label: "Session", Window: model.Window{ID: "session", Label: "S", Kind: "rolling", ResetAt: &soon}, Unit: "percent", Limit: 100, Used: 20, Remaining: 80, PercentUsed: 20, State: "fresh", Severity: "ok", Visible: true, Reset: &model.Reset{ResetAt: soon, ResetWindowID: "session", Source: "provider"}},
		{ID: "no-reset", Provider: "counted", Label: "No reset", Unit: "percent", Limit: 100, Used: 1, Remaining: 99, PercentUsed: 1, State: "fresh", Severity: "ok", Visible: true},
	}, now, int64(time.Hour/time.Millisecond), int64((2*time.Hour)/time.Millisecond))
	h := New(a, "config.example.yaml")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/resets", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("resets status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resets analysis.ResetCalendarResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resets); err != nil {
		t.Fatal(err)
	}
	if len(resets.Resets) != 2 || resets.Resets[0].ItemID != "session" || resets.Resets[1].ItemID != "weekly" {
		t.Fatalf("resets=%+v", resets.Resets)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/availability", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("availability status=%d body=%s", rec.Code, rec.Body.String())
	}
	var availability analysis.AvailabilityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &availability); err != nil {
		t.Fatal(err)
	}
	if len(availability.Providers) != 1 || !availability.Providers[0].Available || len(availability.Providers[0].ConstrainedBy) != 1 || availability.Providers[0].ConstrainedBy[0].ItemID != "weekly" {
		t.Fatalf("availability=%+v", availability.Providers)
	}
	if got := atomic.LoadInt32(&provider.count); got != 0 {
		t.Fatalf("provider fetch should not be called, got %d", got)
	}
}

type countingProvider struct {
	id    string
	count int32
}

func (p *countingProvider) ID() string    { return p.id }
func (p *countingProvider) Label() string { return p.id }
func (p *countingProvider) Fetch(context.Context, time.Time) (providers.Result, error) {
	atomic.AddInt32(&p.count, 1)
	return providers.Result{}, nil
}

func TestProviderMetadataAppearsInUsageAndProvidersResponses(t *testing.T) {
	cfg := config.Default()
	cfg.Providers.ChatGPT.Metadata = config.ProviderMetadataConfig{Tier: "high", Tags: []string{"chat"}, Models: map[string]config.MetadataConfig{"chatgpt-primary": {Tier: "extra-high", Tags: []string{"codex"}}}}
	a := app.New(cfg, nil)
	now := time.Now()
	resetAt := now.Add(time.Hour).UnixMilli()
	a.Store.UpsertSuccess("chatgpt", "ChatGPT Pro", []model.QuotaItem{{ID: "chatgpt-primary", Provider: "chatgpt", Label: "ChatGPT 5h", Window: model.Window{ID: "session", Label: "S", Kind: "rolling", ResetAt: &resetAt}, Unit: "percent", Limit: 100, Used: 20, Remaining: 80, PercentUsed: 20, Visible: true, Reset: &model.Reset{ResetAt: resetAt, ResetWindowID: "session", Source: "provider"}}}, now, int64(time.Hour/time.Millisecond), int64((2*time.Hour)/time.Millisecond))
	h := New(a, "config.example.yaml")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/providers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("providers status=%d body=%s", rec.Code, rec.Body.String())
	}
	var providers model.ProvidersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &providers); err != nil {
		t.Fatal(err)
	}
	if len(providers.Providers) <= 1 || providers.Providers[1].ID != "chatgpt" || providers.Providers[1].Tier != "high" || providers.Providers[1].Tags[0] != "chat" {
		t.Fatalf("providers response=%+v", providers)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/usage", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("usage status=%d body=%s", rec.Code, rec.Body.String())
	}
	var usage model.Usage
	if err := json.Unmarshal(rec.Body.Bytes(), &usage); err != nil {
		t.Fatal(err)
	}
	if len(usage.QuotaItems) != 1 || usage.QuotaItems[0].ProviderTier != "high" || usage.QuotaItems[0].ModelTier != "extra-high" || usage.QuotaItems[0].ModelTags[0] != "codex" {
		t.Fatalf("usage response=%+v", usage)
	}
}

func TestUsageAnalysisEndpointUsesCachedUsageOnly(t *testing.T) {
	cfg := config.Default()
	cfg.Server.StatePath = ""
	cfg.UsageView.Providers = []string{"fake"}
	a := app.New(cfg, nil)
	provider := &countingProvider{id: "fake"}
	a.Providers = []providers.Provider{provider}
	now := time.Now()
	resetAt := now.Add(time.Hour).UnixMilli()
	a.Store.UpsertSuccess("fake", "Fake", []model.QuotaItem{{ID: "fake-session", Provider: "fake", Label: "Fake 5h", Window: model.Window{ID: "session", Label: "S", Kind: "rolling", ResetAt: &resetAt}, Unit: "percent", Limit: 100, Used: 20, Remaining: 80, PercentUsed: 20, Visible: true, Reset: &model.Reset{ResetAt: resetAt, ResetWindowID: "session", Source: "provider"}}}, now, int64(time.Hour/time.Millisecond), int64((2*time.Hour)/time.Millisecond))
	h := New(a, "config.example.yaml")

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/usage/analysis", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var res analysis.UsageAnalysisResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if len(res.Items) != 1 || res.Items[0].ItemID != "fake-session" || res.Items[0].PercentRemaining != 80 {
			t.Fatalf("analysis response=%+v", res)
		}
	}
	if got := atomic.LoadInt32(&provider.count); got != 0 {
		t.Fatalf("usage analysis triggered provider fetch count=%d", got)
	}
}

func TestChatGPTResetCreditsEndpoints(t *testing.T) {
	t.Setenv("CHATGPT_ACCESS_TOKEN", "token")
	t.Setenv("CHATGPT_ACCOUNT_ID", "acct")
	var consumed bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/credits":
			fmt.Fprint(w, `{"available_count":1,"credits":[{"id":"credit-1","status":"available","expires_at":"2026-07-12T01:33:14Z"}]}`)
		case "/consume":
			consumed = true
			fmt.Fprint(w, `{"windows_reset":1,"code":"reset","redeemed_at":"2026-06-13T13:12:31Z"}`)
		case "/usage":
			fmt.Fprint(w, `{"rate_limit":{"primary_window":{"used_percent":0,"limit_window_seconds":18000},"secondary_window":{"used_percent":0,"limit_window_seconds":604800}},"rate_limit_reset_credits":{"available_count":0}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	cfg := config.Default()
	cfg.Server.StatePath = filepath.Join(t.TempDir(), "snapshot.json")
	cfg.Providers.ChatGPT.Enabled = true
	cfg.Providers.ChatGPT.TokenEnv = "CHATGPT_ACCESS_TOKEN"
	cfg.Providers.ChatGPT.AccountIDEnv = "CHATGPT_ACCOUNT_ID"
	cfg.Providers.ChatGPT.EndpointURL = provider.URL + "/usage"
	cfg.Providers.ChatGPT.ResetCreditsEndpointURL = provider.URL + "/credits"
	cfg.Providers.ChatGPT.ResetConsumeEndpointURL = provider.URL + "/consume"
	a := app.New(cfg, nil)
	h := New(a, "config.example.yaml")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/chatgpt/reset-credits", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var listed model.ChatGPTResetCreditsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil || listed.AvailableCount != 1 || len(listed.Credits) != 1 {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}

	body := []byte(`{"creditId":"credit-1","redeemRequestId":"req-1","confirm":"consume-chatgpt-reset-credit"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chatgpt/reset-credits/consume", bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-usagent-action", "consume-chatgpt-reset-credit")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || consumed {
		t.Fatalf("consume without opt-in status=%d consumed=%v body=%s", rec.Code, consumed, rec.Body.String())
	}

	cfg.Providers.ChatGPT.AllowResetConsume = true
	a = app.New(cfg, nil)
	h = New(a, "config.example.yaml")
	req = httptest.NewRequest(http.MethodPost, "/v1/chatgpt/reset-credits/consume", bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-usagent-action", "consume-chatgpt-reset-credit")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !consumed {
		t.Fatalf("consume status=%d consumed=%v body=%s", rec.Code, consumed, rec.Body.String())
	}
}
