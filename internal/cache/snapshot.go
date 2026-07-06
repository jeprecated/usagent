package cache

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jmalloc/usagent/internal/model"
	"github.com/jmalloc/usagent/internal/providers"
)

type ProviderSnapshot struct {
	ID            string            `json:"id"`
	Label         string            `json:"label"`
	Source        string            `json:"source"`
	Items         []model.QuotaItem `json:"items"`
	NextRefreshAt int64             `json:"nextRefreshAt"`
	StaleAt       int64             `json:"staleAt"`
	LastUpdatedAt int64             `json:"lastUpdatedAt"`
	LastError     *model.ItemError  `json:"lastError,omitempty"`
}

type Snapshot struct {
	SchemaVersion int                         `json:"schemaVersion"`
	UpdatedAt     int64                       `json:"updatedAt"`
	Providers     map[string]ProviderSnapshot `json:"providers"`
}

type Store struct {
	mu        sync.RWMutex
	snap      Snapshot
	statePath string
}

func NewStore(statePath string) *Store {
	return &Store{statePath: statePath, snap: Snapshot{SchemaVersion: 2, Providers: map[string]ProviderSnapshot{}}}
}

func (s *Store) Load() error {
	if s.statePath == "" {
		return nil
	}
	b, err := os.ReadFile(s.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var snap Snapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return err
	}
	if snap.Providers == nil {
		snap.Providers = map[string]ProviderSnapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap = snap
	return nil
}

func (s *Store) Save() error {
	if s.statePath == "" {
		return nil
	}
	s.mu.RLock()
	b, err := json.MarshalIndent(s.snap, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.statePath), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.statePath), ".snapshot-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.statePath)
}

func (s *Store) UpsertSuccess(id, label string, items []model.QuotaItem, now time.Time, refreshMs, staleMs int64) {
	nowMs := now.UnixMilli()
	next := nowMs + refreshMs
	stale := nowMs + staleMs
	copied := cloneItems(items)
	for i := range copied {
		copied[i].State = stateOr(copied[i].State, "fresh")
		copied[i].Severity = severityOr(copied[i].Severity, "ok")
		copied[i].Refresh = &model.Refresh{LastUpdatedAt: nowMs, Source: "provider", NextRefreshAt: next, StaleAt: stale}
		copied[i].Error = nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snap.Providers == nil {
		s.snap.Providers = map[string]ProviderSnapshot{}
	}
	s.snap.SchemaVersion = 2
	s.snap.UpdatedAt = nowMs
	s.snap.Providers[id] = ProviderSnapshot{ID: id, Label: label, Source: "pull", Items: copied, NextRefreshAt: next, StaleAt: stale, LastUpdatedAt: nowMs}
}

func (s *Store) MarkFailure(id, label string, now time.Time, retryAfter time.Duration, err error, refreshMs int64) {
	nowMs := now.UnixMilli()
	next := nowMs + refreshMs
	if retryAfter > 0 {
		retryNext := now.Add(retryAfter).UnixMilli()
		if retryNext > next {
			next = retryNext
		}
	}
	errorInfo := &model.ItemError{Provider: id, Code: "refresh_failed", Message: err.Error(), LastOccurredAt: nowMs, Recoverable: true}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snap.Providers == nil {
		s.snap.Providers = map[string]ProviderSnapshot{}
	}
	ps := s.snap.Providers[id]
	if ps.ID == "" {
		ps.ID = id
		ps.Label = label
		ps.Source = "pull"
	}
	ps.NextRefreshAt = next
	ps.LastError = errorInfo
	if len(ps.Items) > 0 {
		for i := range ps.Items {
			ps.Items[i].State = "stale"
			if ps.Items[i].Severity == "ok" || ps.Items[i].Severity == "" {
				ps.Items[i].Severity = "warning"
			}
			ps.Items[i].Error = errorInfo
			if ps.Items[i].Refresh != nil {
				ps.Items[i].Refresh.NextRefreshAt = next
			}
		}
	}
	s.snap.UpdatedAt = nowMs
	s.snap.Providers[id] = ps
}

func (s *Store) Snapshot(now time.Time, providerOrder []model.Provider) Snapshot {
	nowMs := now.UnixMilli()
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := Snapshot{SchemaVersion: 2, UpdatedAt: s.snap.UpdatedAt, Providers: map[string]ProviderSnapshot{}}
	for _, p := range providerOrder {
		ps := s.snap.Providers[p.ID]
		if ps.ID == "" {
			ps = ProviderSnapshot{ID: p.ID, Label: p.Label, Source: p.Source}
		}
		if ps.Source == "" {
			ps.Source = "pull"
		}
		if ps.Label == "" {
			ps.Label = p.Label
		}
		ps.Items = cloneItems(ps.Items)
		if len(ps.Items) > 0 && ps.StaleAt > 0 && nowMs >= ps.StaleAt {
			for i := range ps.Items {
				ps.Items[i].State = "stale"
				if ps.Items[i].Severity == "ok" {
					ps.Items[i].Severity = "warning"
				}
				if ps.LastError != nil {
					ps.Items[i].Error = ps.LastError
				}
			}
		}
		out.Providers[p.ID] = ps
	}
	return out
}

func (s *Store) NeedsRefresh(id string, now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ps := s.snap.Providers[id]
	return ps.NextRefreshAt == 0 || now.UnixMilli() >= ps.NextRefreshAt
}

func (s *Store) Overview(now time.Time, providerOrder []model.Provider) model.Usage {
	snap := s.Snapshot(now, providerOrder)
	providersOut := make([]model.Provider, 0, len(providerOrder))
	items := []model.QuotaItem{}
	for _, base := range providerOrder {
		ps := snap.Providers[base.ID]
		state := model.ProviderStateStale
		if ps.LastError != nil {
			state = model.ProviderStateError
		} else if len(ps.Items) == 0 {
			state = model.ProviderStateStale
		} else {
			state = model.ProviderStateFresh
			for _, it := range ps.Items {
				if it.State == "stale" {
					state = model.ProviderStateStale
				}
				if it.State == "error" || it.Severity == "error" {
					state = model.ProviderStateError
				}
			}
		}
		var last *int64
		if ps.LastUpdatedAt > 0 {
			v := ps.LastUpdatedAt
			last = &v
		}
		providersOut = append(providersOut, model.Provider{ID: base.ID, Label: base.Label, State: state, Source: "pull", LastUpdatedAt: last, Error: ps.LastError})
		items = append(items, ps.Items...)
	}
	return model.Usage{SchemaVersion: 2, Service: "usagent", GeneratedAt: now.UnixMilli(), Stale: len(items) == 0, Providers: providersOut, QuotaItems: items}
}

func ApplyFetch(store *Store, p providers.Provider, result providers.Result, err error, now time.Time, refreshMs, staleMs int64) error {
	if err != nil {
		store.MarkFailure(p.ID(), p.Label(), now, result.RetryAfter, err, refreshMs)
		return err
	}
	store.UpsertSuccess(p.ID(), p.Label(), result.Items, now, refreshMs, staleMs)
	return nil
}

func cloneItems(in []model.QuotaItem) []model.QuotaItem {
	out := make([]model.QuotaItem, len(in))
	copy(out, in)
	for i := range out {
		if in[i].Refresh != nil {
			r := *in[i].Refresh
			out[i].Refresh = &r
		}
		if in[i].Reset != nil {
			r := *in[i].Reset
			out[i].Reset = &r
		}
		if in[i].Error != nil {
			e := *in[i].Error
			out[i].Error = &e
		}
	}
	return out
}
func stateOr(v, d string) string {
	if v != "" {
		return v
	}
	return d
}
func severityOr(v, d string) string {
	if v != "" {
		return v
	}
	return d
}
