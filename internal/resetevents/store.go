// Package resetevents records observed quota replenishments, not predicted resets.
package resetevents

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jeprecated/usagent/internal/atomicfile"
	"github.com/jeprecated/usagent/internal/model"
)

const MinimumIncrease = 10.0 // percentage points; ignore small API corrections
const PageSize = 100

type Event struct {
	ID            uint64  `json:"id"`
	Provider      string  `json:"provider"`
	ItemID        string  `json:"itemId"`
	Label         string  `json:"label"`
	Window        string  `json:"window"`
	ObservedAt    int64   `json:"observedAt"`
	BeforePercent float64 `json:"beforePercent"`
	AfterPercent  float64 `json:"afterPercent"`
}

type Page struct {
	StreamID string  `json:"streamId"`
	Events   []Event `json:"events"`
}

type baseline struct {
	Items      []model.QuotaItem `json:"items"`
	ObservedAt int64             `json:"observedAt"`
}

type diskState struct {
	Version   int                 `json:"version"`
	StreamID  string              `json:"streamId"`
	Baselines map[string]baseline `json:"baselines"`
	Events    []Event             `json:"events"`
}

type Store struct {
	mu     sync.Mutex
	path   string
	state  diskState
	loaded bool
}

func New(snapshotPath string) *Store {
	path := ""
	if snapshotPath != "" {
		path = filepath.Join(filepath.Dir(snapshotPath), "reset-events.json")
	}
	return &Store{path: path}
}

// Load must succeed before recording or serving events. Never overwrite corrupt
// state: that would silently discard alerts and recycle event IDs.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loaded = false
	var state diskState
	var b []byte
	var err error
	if s.path != "" {
		b, err = os.ReadFile(s.path)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &state); err != nil {
			return err
		}
		if state.Version != 1 || state.StreamID == "" || state.Baselines == nil {
			return errors.New("invalid reset event state")
		}
		for i, event := range state.Events {
			if event.ID != uint64(i+1) {
				return errors.New("invalid reset event sequence")
			}
		}
	} else {
		if err == nil && s.path != "" {
			return errors.New("empty reset event state")
		}
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return err
		}
		state = diskState{Version: 1, StreamID: hex.EncodeToString(id[:]), Baselines: map[string]baseline{}, Events: []Event{}}
		if s.path != "" {
			if err := atomicfile.WriteJSON(s.path, state); err != nil {
				return err
			}
		}
	}
	s.state, s.loaded = state, true
	return nil
}

// Record commits the comparison baseline and events together. Failed writes
// leave both unchanged so a subsequent refresh can retry without losing alerts.
// Only the daemon calls this; CLI local refreshes must not compete for the file.
func (s *Store) Record(provider string, items []model.QuotaItem, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		return errors.New("reset event store is not loaded")
	}
	old := s.state.Baselines[provider]
	if now.UnixMilli() <= old.ObservedAt {
		return nil
	}
	previous := map[string]model.QuotaItem{}
	for _, item := range old.Items {
		previous[item.ID] = item
	}
	next := s.state
	next.Baselines = make(map[string]baseline, len(s.state.Baselines)+1)
	for k, v := range s.state.Baselines {
		next.Baselines[k] = v
	}
	next.Events = append([]Event{}, s.state.Events...)
	valid := []model.QuotaItem{}
	for _, item := range items {
		if !eligible(item) {
			continue
		}
		valid = append(valid, item)
		prev, ok := previous[item.ID]
		if !ok || prev.Unit != item.Unit || prev.Limit != item.Limit || prev.Window.ID != item.Window.ID || prev.Window.Kind != item.Window.Kind {
			continue
		}
		before, after := percentRemaining(prev), percentRemaining(item)
		if after-before+1e-9 < MinimumIncrease {
			continue
		}
		next.Events = append(next.Events, Event{ID: uint64(len(next.Events) + 1), Provider: provider, ItemID: item.ID, Label: item.Label, Window: item.Window.Label, ObservedAt: now.UnixMilli(), BeforePercent: before, AfterPercent: after})
	}
	next.Baselines[provider] = baseline{Items: valid, ObservedAt: now.UnixMilli()}
	if s.path != "" {
		if err := atomicfile.WriteJSON(s.path, next); err != nil {
			return fmt.Errorf("save reset events: %w", err)
		}
	}
	s.state = next
	return nil
}

// Page is cached-only. A different stream (e.g. a replaced daemon state file)
// starts at zero rather than letting an old desktop cursor hide new events.
func (s *Store) Page(stream string, after uint64) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		return Page{}, errors.New("reset event store is not loaded")
	}
	if stream != s.state.StreamID {
		after = 0
	}
	out := Page{StreamID: s.state.StreamID, Events: []Event{}}
	for _, event := range s.state.Events {
		if event.ID > after {
			out.Events = append(out.Events, event)
			if len(out.Events) == PageSize {
				break
			}
		}
	}
	return out, nil
}

func eligible(item model.QuotaItem) bool {
	return item.ID != "" && item.Visible && item.Error == nil &&
		(item.State == "" || item.State == "fresh") && item.Window.Kind != "credit" &&
		finite(item.Limit) && item.Limit > 0 && finite(item.Remaining) && item.Remaining >= 0 && item.Remaining <= item.Limit
}
func finite(n float64) bool                         { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func percentRemaining(item model.QuotaItem) float64 { return item.Remaining / item.Limit * 100 }
