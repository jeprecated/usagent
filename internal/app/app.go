package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jmalloc/usagent/internal/cache"
	"github.com/jmalloc/usagent/internal/config"
	"github.com/jmalloc/usagent/internal/model"
	"github.com/jmalloc/usagent/internal/providers"
	"github.com/jmalloc/usagent/internal/providers/claude"
	"github.com/jmalloc/usagent/internal/providers/noop"
)

type ProviderTiming struct{ RefreshMs, StaleMs int64 }

type App struct {
	Cfg       config.Config
	Store     *cache.Store
	Providers []providers.Provider
	Timings   map[string]ProviderTiming
	StartedAt int64
	Logger    *slog.Logger
	mu        sync.Mutex
	inFlight  map[string]bool
}

func New(cfg config.Config, logger *slog.Logger) *App {
	if logger == nil {
		logger = slog.Default()
	}
	ps := []providers.Provider{}
	timings := map[string]ProviderTiming{}
	if cfg.Providers.ClaudeOAuth.Enabled {
		p := claude.New(cfg.Providers.ClaudeOAuth)
		ps = append(ps, p)
		timings[p.ID()] = ProviderTiming{RefreshMs: cfg.Providers.ClaudeOAuth.RefreshMs, StaleMs: cfg.Providers.ClaudeOAuth.StaleMs}
	}
	for _, id := range cfg.UsageView.Providers {
		if id == "claude-code" && cfg.Providers.ClaudeOAuth.Enabled {
			continue
		}
		label := LabelFor(id)
		ps = append(ps, noop.New(id, label))
		timings[id] = ProviderTiming{RefreshMs: cfg.Quota.RefreshMs, StaleMs: max(cfg.Quota.RefreshMs*3, int64((15*time.Minute)/time.Millisecond))}
	}
	return &App{Cfg: cfg, Store: cache.NewStore(cfg.Server.StatePath), Providers: ps, Timings: timings, StartedAt: time.Now().UnixMilli(), Logger: logger, inFlight: map[string]bool{}}
}

func LabelFor(id string) string {
	switch id {
	case "claude-code":
		return "Claude"
	case "openai":
		return "OpenAI"
	case "z-ai":
		return "z.ai"
	default:
		return id
	}
}

func (a *App) LoadState() error { return a.Store.Load() }

func (a *App) ProviderModels() []model.Provider {
	out := make([]model.Provider, 0, len(a.Cfg.UsageView.Providers))
	for _, id := range a.Cfg.UsageView.Providers {
		out = append(out, model.Provider{ID: id, Label: LabelFor(id), Source: "pull"})
	}
	return out
}

func (a *App) Usage(now time.Time) model.Usage {
	u := a.Store.Overview(now, a.ProviderModels())
	u.StartedAt = a.StartedAt
	return u
}

func (a *App) RefreshDue(ctx context.Context, now time.Time) {
	for _, p := range a.Providers {
		if a.Store.NeedsRefresh(p.ID(), now) {
			go a.RefreshOne(ctx, p, now)
		}
	}
}

func (a *App) RefreshOne(ctx context.Context, p providers.Provider, now time.Time) {
	if !a.enter(p.ID()) {
		return
	}
	defer a.leave(p.ID())
	timing := a.Timings[p.ID()]
	if timing.RefreshMs <= 0 {
		timing.RefreshMs = a.Cfg.Quota.RefreshMs
	}
	if timing.StaleMs <= 0 {
		timing.StaleMs = max(timing.RefreshMs*3, int64((15*time.Minute)/time.Millisecond))
	}
	result, err := p.Fetch(ctx, now)
	_ = cache.ApplyFetch(a.Store, p, result, err, now, timing.RefreshMs, timing.StaleMs)
	if err != nil {
		a.Logger.Warn("provider refresh failed", "provider", p.ID(), "error", err)
	} else {
		a.Logger.Debug("provider refreshed", "provider", p.ID(), "items", len(result.Items))
	}
	if err := a.Store.Save(); err != nil {
		a.Logger.Warn("state save failed", "error", err)
	}
}

func (a *App) RunRefreshLoop(ctx context.Context) {
	a.RefreshDue(ctx, time.Now())
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			a.RefreshDue(ctx, now)
		}
	}
}

func (a *App) enter(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inFlight[id] {
		return false
	}
	a.inFlight[id] = true
	return true
}
func (a *App) leave(id string) { a.mu.Lock(); defer a.mu.Unlock(); delete(a.inFlight, id) }
