package app

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmalloc/usagent/internal/config"
	"github.com/jmalloc/usagent/internal/model"
	"github.com/jmalloc/usagent/internal/providers"
)

type slowProvider struct {
	id    string
	count int32
	block chan struct{}
}

func (p *slowProvider) ID() string    { return p.id }
func (p *slowProvider) Label() string { return p.id }
func (p *slowProvider) Fetch(context.Context, time.Time) (providers.Result, error) {
	atomic.AddInt32(&p.count, 1)
	<-p.block
	return providers.Result{Items: []model.QuotaItem{{ID: p.id + "-item", Provider: p.id, Label: "Item", Window: model.Window{ID: "w", Label: "W", Kind: "test"}, Unit: "count", Limit: 1, Visible: true}}}, nil
}

func TestRefreshDueDoesNotStartConcurrentFetchesForSameProvider(t *testing.T) {
	cfg := config.Default()
	cfg.Server.StatePath = ""
	cfg.UsageView.Providers = []string{"fake"}
	a := New(cfg, nil)
	p := &slowProvider{id: "fake", block: make(chan struct{})}
	a.Providers = []providers.Provider{p}
	a.Timings["fake"] = ProviderTiming{RefreshMs: 1000, StaleMs: 2000}
	now := time.UnixMilli(1000)
	a.RefreshDue(context.Background(), now)
	a.RefreshDue(context.Background(), now)
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&p.count); got != 1 {
		t.Fatalf("fetch count while in-flight=%d", got)
	}
	close(p.block)
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&p.count); got != 1 {
		t.Fatalf("fetch count after completion=%d", got)
	}
}

func TestRefreshOneRecordsLocalUsageHistory(t *testing.T) {
	cfg := config.Default()
	cfg.Server.StatePath = filepath.Join(t.TempDir(), "snapshot.json")
	cfg.UsageView.Providers = []string{"fake"}
	a := New(cfg, nil)
	p := &slowProvider{id: "fake", block: make(chan struct{})}
	close(p.block)
	a.Providers = []providers.Provider{p}
	a.Timings["fake"] = ProviderTiming{RefreshMs: 1000, StaleMs: 2000}
	a.RefreshOne(context.Background(), p, time.UnixMilli(1000))
	if got := len(a.History.Samples()); got != 1 {
		t.Fatalf("history samples=%d", got)
	}
	loaded := New(cfg, nil)
	if err := loaded.LoadState(); err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got := len(loaded.History.Samples()); got != 1 {
		t.Fatalf("loaded history samples=%d", got)
	}
}

func TestUsageDoesNotFetchProvidersForHTTPCallers(t *testing.T) {
	cfg := config.Default()
	cfg.Server.StatePath = ""
	cfg.UsageView.Providers = []string{"fake"}
	a := New(cfg, nil)
	p := &slowProvider{id: "fake", block: make(chan struct{})}
	a.Providers = []providers.Provider{p}
	for i := 0; i < 10; i++ {
		_ = a.Usage(time.Now())
	}
	if got := atomic.LoadInt32(&p.count); got != 0 {
		t.Fatalf("usage triggered provider fetch count=%d", got)
	}
}

func TestUsageIncludesConfiguredMetadataAndOmitsMissingMetadata(t *testing.T) {
	cfg := config.Default()
	cfg.Server.StatePath = ""
	cfg.UsageView.Providers = []string{"chatgpt", "mine", "unknown"}
	cfg.Providers.ChatGPT.Metadata = config.ProviderMetadataConfig{Tier: "high", Tags: []string{"chat", "subscription"}, Models: map[string]config.MetadataConfig{"chatgpt-primary": {Tier: "extra-high", Tags: []string{"codex"}}}}
	cfg.Providers.Custom = []config.CustomProviderConfig{{ID: "mine", Label: "Mine", Enabled: true, Metadata: config.ProviderMetadataConfig{Tier: "medium", Tags: []string{"custom"}}}}
	a := New(cfg, nil)
	now := time.UnixMilli(1000)
	resetAt := now.Add(time.Hour).UnixMilli()
	a.Store.UpsertSuccess("chatgpt", "ChatGPT Pro", []model.QuotaItem{{ID: "chatgpt-primary", Provider: "chatgpt", Label: "ChatGPT 5h", Window: model.Window{ID: "session", Label: "S", Kind: "rolling", ResetAt: &resetAt}, Unit: "percent", Limit: 100, Used: 10, Remaining: 90, PercentUsed: 10, Visible: true}}, now, 1000, 2000)
	a.Store.UpsertSuccess("mine", "Mine", []model.QuotaItem{{ID: "mine-default", Provider: "mine", Label: "Mine quota", Window: model.Window{ID: "daily", Label: "D", Kind: "daily", ResetAt: &resetAt}, Unit: "count", Limit: 10, Used: 1, Remaining: 9, Visible: true}}, now, 1000, 2000)
	a.Store.UpsertSuccess("unknown", "unknown", []model.QuotaItem{{ID: "unknown-default", Provider: "unknown", Label: "Unknown quota", Window: model.Window{ID: "daily", Label: "D", Kind: "daily", ResetAt: &resetAt}, Unit: "count", Limit: 10, Used: 1, Remaining: 9, Visible: true}}, now, 1000, 2000)

	u := a.Usage(now)
	providers := map[string]model.Provider{}
	for _, p := range u.Providers {
		providers[p.ID] = p
	}
	if providers["chatgpt"].Tier != "high" || providers["chatgpt"].Tags[0] != "chat" {
		t.Fatalf("chatgpt provider metadata=%+v", providers["chatgpt"])
	}
	if providers["mine"].Tier != "medium" || providers["mine"].Tags[0] != "custom" {
		t.Fatalf("custom provider metadata=%+v", providers["mine"])
	}
	if providers["unknown"].Tier != "" || len(providers["unknown"].Tags) != 0 {
		t.Fatalf("unknown provider should omit metadata: %+v", providers["unknown"])
	}
	items := map[string]model.QuotaItem{}
	for _, item := range u.QuotaItems {
		items[item.ID] = item
	}
	if items["chatgpt-primary"].ProviderTier != "high" || items["chatgpt-primary"].ModelTier != "extra-high" || items["chatgpt-primary"].ModelTags[0] != "codex" {
		t.Fatalf("chatgpt item metadata=%+v", items["chatgpt-primary"])
	}
	if items["mine-default"].ProviderTier != "medium" || items["mine-default"].ProviderTags[0] != "custom" {
		t.Fatalf("custom item metadata=%+v", items["mine-default"])
	}
	if items["unknown-default"].ProviderTier != "" || len(items["unknown-default"].ProviderTags) != 0 || items["unknown-default"].ModelTier != "" {
		t.Fatalf("unknown item should omit metadata: %+v", items["unknown-default"])
	}
}

func TestNewWiresEnabledProvidersAndCustomMetadata(t *testing.T) {
	cfg := config.Default()
	cfg.Server.StatePath = ""
	cfg.Providers.ChatGPT.Enabled = true
	cfg.Providers.OpenAI.Enabled = true
	cfg.Providers.OpenAI.Budgets = []config.Budget{{ID: "weekly-usd", Unit: "usd", Limit: 10}}
	cfg.Providers.ZAI.Enabled = true
	cfg.Providers.Custom = []config.CustomProviderConfig{{ID: "mine", Label: "Mine", Enabled: true}}
	a := New(cfg, nil)
	ids := map[string]bool{}
	for _, p := range a.Providers {
		ids[p.ID()] = true
	}
	for _, want := range []string{"chatgpt", "openai", "z-ai", "mine"} {
		if !ids[want] {
			t.Fatalf("provider %s not wired; ids=%v", want, ids)
		}
	}
	models := a.ProviderModels()
	if models[len(models)-1].ID != "mine" || models[len(models)-1].Label != "Mine" {
		t.Fatalf("models=%+v", models)
	}
}
