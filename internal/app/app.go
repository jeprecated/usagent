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
	"github.com/jmalloc/usagent/internal/providers/chatgpt"
	"github.com/jmalloc/usagent/internal/providers/claude"
	"github.com/jmalloc/usagent/internal/providers/custom"
	"github.com/jmalloc/usagent/internal/providers/noop"
	"github.com/jmalloc/usagent/internal/providers/openai"
	"github.com/jmalloc/usagent/internal/providers/zai"
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
	labels := labelsFor(cfg)
	active := map[string]bool{}
	if cfg.Providers.ClaudeOAuth.Enabled {
		p := claude.New(cfg.Providers.ClaudeOAuth)
		ps = append(ps, p)
		timings[p.ID()] = ProviderTiming{RefreshMs: cfg.Providers.ClaudeOAuth.RefreshMs, StaleMs: cfg.Providers.ClaudeOAuth.StaleMs}
		active[p.ID()] = true
	}
	if cfg.Providers.ChatGPT.Enabled {
		p := chatgpt.New(cfg.Providers.ChatGPT)
		ps = append(ps, p)
		timings[p.ID()] = ProviderTiming{RefreshMs: cfg.Providers.ChatGPT.RefreshMs, StaleMs: cfg.Providers.ChatGPT.StaleMs}
		active[p.ID()] = true
	}
	if cfg.Providers.OpenAI.Enabled {
		p := openai.New(cfg.Providers.OpenAI)
		ps = append(ps, p)
		timings[p.ID()] = ProviderTiming{RefreshMs: cfg.Providers.OpenAI.RefreshMs, StaleMs: cfg.Providers.OpenAI.StaleMs}
		active[p.ID()] = true
	}
	if cfg.Providers.ZAI.Enabled {
		p := zai.New(cfg.Providers.ZAI)
		ps = append(ps, p)
		timings[p.ID()] = ProviderTiming{RefreshMs: cfg.Providers.ZAI.RefreshMs, StaleMs: cfg.Providers.ZAI.StaleMs}
		active[p.ID()] = true
	}
	for _, cp := range cfg.Providers.Custom {
		if !cp.Enabled {
			continue
		}
		p := custom.New(cp)
		ps = append(ps, p)
		timings[p.ID()] = ProviderTiming{RefreshMs: cp.RefreshMs, StaleMs: cp.StaleMs}
		active[p.ID()] = true
		labels[p.ID()] = p.Label()
		if !contains(cfg.UsageView.Providers, p.ID()) {
			cfg.UsageView.Providers = append(cfg.UsageView.Providers, p.ID())
		}
	}
	for _, id := range cfg.UsageView.Providers {
		if active[id] {
			continue
		}
		label := labels[id]
		if label == "" {
			label = LabelFor(id)
		}
		ps = append(ps, noop.New(id, label))
		timings[id] = ProviderTiming{RefreshMs: cfg.Quota.RefreshMs, StaleMs: max(cfg.Quota.RefreshMs*3, int64((15*time.Minute)/time.Millisecond))}
	}
	return &App{Cfg: cfg, Store: cache.NewStore(cfg.Server.StatePath), Providers: ps, Timings: timings, StartedAt: time.Now().UnixMilli(), Logger: logger, inFlight: map[string]bool{}}
}

func LabelFor(id string) string {
	switch id {
	case "claude-code":
		return "Claude"
	case "chatgpt":
		return "ChatGPT Pro"
	case "openai":
		return "OpenAI API"
	case "z-ai":
		return "z.ai"
	default:
		return id
	}
}

func labelsFor(cfg config.Config) map[string]string {
	labels := map[string]string{"claude-code": "Claude", "chatgpt": "ChatGPT Pro", "openai": "OpenAI API", "z-ai": "z.ai"}
	for _, cp := range cfg.Providers.Custom {
		if cp.Label != "" {
			labels[cp.ID] = cp.Label
		}
	}
	return labels
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func (a *App) LoadState() error { return a.Store.Load() }

func (a *App) ProviderModels() []model.Provider {
	labels := labelsFor(a.Cfg)
	out := make([]model.Provider, 0, len(a.Cfg.UsageView.Providers))
	for _, id := range a.Cfg.UsageView.Providers {
		label := labels[id]
		if label == "" {
			label = LabelFor(id)
		}
		out = append(out, model.Provider{ID: id, Label: label, Source: "pull"})
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
