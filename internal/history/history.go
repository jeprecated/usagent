package history

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/jmalloc/usagent/internal/model"
)

const (
	SchemaVersion              = 1
	DefaultRetention           = 30 * 24 * time.Hour
	DefaultMaxSamplesPerSeries = 500
)

var RecentHorizons = []time.Duration{15 * time.Minute, time.Hour, 6 * time.Hour}

type Sample struct {
	Provider      string  `json:"provider"`
	ItemID        string  `json:"itemId"`
	Label         string  `json:"label,omitempty"`
	WindowID      string  `json:"windowId,omitempty"`
	WindowLabel   string  `json:"windowLabel,omitempty"`
	WindowKind    string  `json:"windowKind,omitempty"`
	ResetAt       int64   `json:"resetAt,omitempty"`
	ResetWindowID string  `json:"resetWindowId,omitempty"`
	Unit          string  `json:"unit,omitempty"`
	Limit         float64 `json:"limit,omitempty"`
	Used          float64 `json:"used"`
	Remaining     float64 `json:"remaining"`
	PercentUsed   float64 `json:"percentUsed,omitempty"`
	RecordedAt    int64   `json:"recordedAt"`
}

type File struct {
	SchemaVersion int      `json:"schemaVersion"`
	UpdatedAt     int64    `json:"updatedAt"`
	RetentionDays int      `json:"retentionDays"`
	Samples       []Sample `json:"samples"`
}

type RateEstimate struct {
	BurnPerMs    float64 `json:"burnPerMs"`
	Source       string  `json:"source"`
	Confidence   string  `json:"confidence"`
	Since        int64   `json:"since"`
	Until        int64   `json:"until"`
	Samples      int     `json:"samples"`
	WindowToDate bool    `json:"windowToDate,omitempty"`
}

type Store struct {
	mu                  sync.RWMutex
	path                string
	retention           time.Duration
	maxSamplesPerSeries int
	file                File
}

func DefaultPath(snapshotPath string) string {
	if snapshotPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(snapshotPath), "usage-history.json")
}

func NewStore(path string) *Store {
	return NewStoreWithLimits(path, DefaultRetention, DefaultMaxSamplesPerSeries)
}

func NewStoreWithLimits(path string, retention time.Duration, maxSamplesPerSeries int) *Store {
	if retention <= 0 {
		retention = DefaultRetention
	}
	if maxSamplesPerSeries <= 0 {
		maxSamplesPerSeries = DefaultMaxSamplesPerSeries
	}
	return &Store{
		path:                path,
		retention:           retention,
		maxSamplesPerSeries: maxSamplesPerSeries,
		file:                File{SchemaVersion: SchemaVersion, RetentionDays: int(retention.Hours() / 24), Samples: []Sample{}},
	}
}

func (s *Store) Load() error {
	if s == nil || s.path == "" {
		return nil
	}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var file File
	if err := json.Unmarshal(b, &file); err != nil {
		return err
	}
	if file.Samples == nil {
		file.Samples = []Sample{}
	}
	if file.SchemaVersion == 0 {
		file.SchemaVersion = SchemaVersion
	}
	if file.RetentionDays == 0 {
		file.RetentionDays = int(s.retention.Hours() / 24)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.file = file
	return nil
}

func (s *Store) Save() error {
	if s == nil || s.path == "" {
		return nil
	}
	s.mu.RLock()
	b, err := json.MarshalIndent(s.file, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".usage-history-*.json")
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
	return os.Rename(tmpName, s.path)
}

func (s *Store) Record(provider string, items []model.QuotaItem, now time.Time) {
	if s == nil {
		return
	}
	nowMs := now.UnixMilli()
	newSamples := make([]Sample, 0, len(items))
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		sampleProvider := provider
		if sampleProvider == "" {
			sampleProvider = item.Provider
		}
		newSamples = append(newSamples, sampleFromItem(sampleProvider, item, nowMs))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.file.SchemaVersion = SchemaVersion
	s.file.UpdatedAt = nowMs
	s.file.RetentionDays = int(s.retention.Hours() / 24)
	s.file.Samples = append(s.file.Samples, newSamples...)
	s.file.Samples = pruneSamples(s.file.Samples, nowMs-int64(s.retention/time.Millisecond), s.maxSamplesPerSeries)
}

func (s *Store) Rates(items []model.QuotaItem, now time.Time) map[string]RateEstimate {
	out := map[string]RateEstimate{}
	if s == nil {
		return out
	}
	s.mu.RLock()
	samples := append([]Sample(nil), s.file.Samples...)
	s.mu.RUnlock()
	for _, item := range items {
		if item.Provider == "" || item.ID == "" {
			continue
		}
		if rate, ok := rateForItem(samples, item, now); ok {
			out[Key(item.Provider, item.ID)] = rate
		}
	}
	return out
}

func (s *Store) Samples() []Sample {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Sample(nil), s.file.Samples...)
}

func Key(provider, itemID string) string { return provider + "\x00" + itemID }

func sampleFromItem(provider string, item model.QuotaItem, nowMs int64) Sample {
	resetAt, resetWindowID := resetInfo(item)
	return Sample{
		Provider:      provider,
		ItemID:        item.ID,
		Label:         item.Label,
		WindowID:      item.Window.ID,
		WindowLabel:   item.Window.Label,
		WindowKind:    item.Window.Kind,
		ResetAt:       resetAt,
		ResetWindowID: resetWindowID,
		Unit:          item.Unit,
		Limit:         finiteOrZero(item.Limit),
		Used:          finiteOrZero(item.Used),
		Remaining:     finiteOrZero(item.Remaining),
		PercentUsed:   finiteOrZero(item.PercentUsed),
		RecordedAt:    nowMs,
	}
}

func resetInfo(item model.QuotaItem) (int64, string) {
	if item.Reset != nil {
		return item.Reset.ResetAt, item.Reset.ResetWindowID
	}
	if item.Window.ResetAt != nil {
		return *item.Window.ResetAt, item.Window.ID
	}
	return 0, item.Window.ID
}

func finiteOrZero(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func pruneSamples(samples []Sample, cutoffMs int64, maxPerSeries int) []Sample {
	kept := make([]Sample, 0, len(samples))
	for _, sample := range samples {
		if sample.RecordedAt >= cutoffMs {
			kept = append(kept, sample)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].RecordedAt == kept[j].RecordedAt {
			return seriesKey(kept[i]) < seriesKey(kept[j])
		}
		return kept[i].RecordedAt > kept[j].RecordedAt
	})
	counts := map[string]int{}
	bounded := make([]Sample, 0, len(kept))
	for _, sample := range kept {
		key := seriesKey(sample)
		if counts[key] >= maxPerSeries {
			continue
		}
		counts[key]++
		bounded = append(bounded, sample)
	}
	sort.SliceStable(bounded, func(i, j int) bool {
		if bounded[i].RecordedAt == bounded[j].RecordedAt {
			return seriesKey(bounded[i]) < seriesKey(bounded[j])
		}
		return bounded[i].RecordedAt < bounded[j].RecordedAt
	})
	return bounded
}

func seriesKey(sample Sample) string {
	return sample.Provider + "\x00" + sample.ItemID + "\x00" + sample.Unit + "\x00" + sample.WindowID + "\x00" + sample.WindowKind
}

func rateForItem(samples []Sample, item model.QuotaItem, now time.Time) (RateEstimate, bool) {
	compatible := make([]Sample, 0, len(samples))
	for _, sample := range samples {
		if sampleCompatible(sample, item) {
			compatible = append(compatible, sample)
		}
	}
	if len(compatible) < 2 {
		return RateEstimate{}, false
	}
	sort.SliceStable(compatible, func(i, j int) bool { return compatible[i].RecordedAt < compatible[j].RecordedAt })
	nowMs := now.UnixMilli()
	for _, horizon := range RecentHorizons {
		if rate, ok := rateFromSamples(compatible, nowMs-int64(horizon/time.Millisecond), false); ok {
			rate.Source = horizonLabel(horizon) + " local history burn rate"
			return rate, true
		}
	}
	if rate, ok := rateFromSamples(compatible, 0, true); ok {
		rate.Source = "window-to-date local history burn rate"
		rate.WindowToDate = true
		return rate, true
	}
	return RateEstimate{}, false
}

func sampleCompatible(sample Sample, item model.QuotaItem) bool {
	if sample.Provider != item.Provider || sample.ItemID != item.ID {
		return false
	}
	resetAt, resetWindowID := resetInfo(item)
	if resetAt > 0 && sample.ResetAt > 0 && sample.ResetAt != resetAt {
		return false
	}
	if resetWindowID != "" && sample.ResetWindowID != "" && sample.ResetWindowID != resetWindowID {
		return false
	}
	if item.Unit != "" && sample.Unit != "" && sample.Unit != item.Unit {
		return false
	}
	if item.Window.ID != "" && sample.WindowID != "" && sample.WindowID != item.Window.ID {
		return false
	}
	if item.Window.Kind != "" && sample.WindowKind != "" && sample.WindowKind != item.Window.Kind {
		return false
	}
	return true
}

func rateFromSamples(samples []Sample, sinceMs int64, windowToDate bool) (RateEstimate, bool) {
	filtered := make([]Sample, 0, len(samples))
	for _, sample := range samples {
		if sinceMs <= 0 || sample.RecordedAt >= sinceMs {
			filtered = append(filtered, sample)
		}
	}
	if len(filtered) < 2 {
		return RateEstimate{}, false
	}
	totalDelta := 0.0
	totalMs := int64(0)
	pairCount := 0
	for i := 1; i < len(filtered); i++ {
		prev, cur := filtered[i-1], filtered[i]
		if !samplesShareWindow(prev, cur) {
			continue
		}
		dt := cur.RecordedAt - prev.RecordedAt
		if dt <= 0 {
			continue
		}
		delta, ok := consumptionDelta(prev, cur)
		if !ok {
			continue
		}
		totalDelta += delta
		totalMs += dt
		pairCount++
	}
	if pairCount == 0 || totalMs <= 0 {
		return RateEstimate{}, false
	}
	confidence := "medium"
	if pairCount >= 2 || len(filtered) >= 3 {
		confidence = "high"
	}
	return RateEstimate{
		BurnPerMs:    totalDelta / float64(totalMs),
		Confidence:   confidence,
		Since:        filtered[0].RecordedAt,
		Until:        filtered[len(filtered)-1].RecordedAt,
		Samples:      len(filtered),
		WindowToDate: windowToDate,
	}, true
}

func samplesShareWindow(prev, cur Sample) bool {
	if prev.ResetAt > 0 && cur.ResetAt > 0 && prev.ResetAt != cur.ResetAt {
		return false
	}
	if prev.ResetWindowID != "" && cur.ResetWindowID != "" && prev.ResetWindowID != cur.ResetWindowID {
		return false
	}
	if prev.WindowID != "" && cur.WindowID != "" && prev.WindowID != cur.WindowID {
		return false
	}
	if prev.WindowKind != "" && cur.WindowKind != "" && prev.WindowKind != cur.WindowKind {
		return false
	}
	if prev.Unit != "" && cur.Unit != "" && prev.Unit != cur.Unit {
		return false
	}
	return true
}

func consumptionDelta(prev, cur Sample) (float64, bool) {
	usedDelta := cur.Used - prev.Used
	if usedDelta > 0 {
		return usedDelta, true
	}
	remainingDelta := prev.Remaining - cur.Remaining
	if remainingDelta >= 0 {
		return remainingDelta, true
	}
	if usedDelta == 0 {
		return 0, true
	}
	return 0, false
}

func horizonLabel(d time.Duration) string {
	switch d {
	case 15 * time.Minute:
		return "15m"
	case time.Hour:
		return "1h"
	case 6 * time.Hour:
		return "6h"
	default:
		return d.String()
	}
}
