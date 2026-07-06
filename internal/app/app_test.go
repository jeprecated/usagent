package app

import (
	"context"
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

func TestNewWiresEnabledProvidersAndCustomMetadata(t *testing.T) {
	cfg := config.Default()
	cfg.Server.StatePath = ""
	cfg.Providers.OpenAI.Enabled = true
	cfg.Providers.OpenAI.Budgets = []config.Budget{{ID: "weekly-usd", Unit: "usd", Limit: 10}}
	cfg.Providers.ZAI.Enabled = true
	cfg.Providers.Custom = []config.CustomProviderConfig{{ID: "mine", Label: "Mine", Enabled: true}}
	a := New(cfg, nil)
	ids := map[string]bool{}
	for _, p := range a.Providers {
		ids[p.ID()] = true
	}
	for _, want := range []string{"openai", "z-ai", "mine"} {
		if !ids[want] {
			t.Fatalf("provider %s not wired; ids=%v", want, ids)
		}
	}
	models := a.ProviderModels()
	if models[len(models)-1].ID != "mine" || models[len(models)-1].Label != "Mine" {
		t.Fatalf("models=%+v", models)
	}
}
