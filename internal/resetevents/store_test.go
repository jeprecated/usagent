package resetevents

import (
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/model"
)

func quota(remaining float64) model.QuotaItem {
	return model.QuotaItem{ID: "weekly", Provider: "test", Label: "Test weekly", Window: model.Window{ID: "week", Kind: "weekly", Label: "W"}, Unit: "percent", Limit: 100, Remaining: remaining, State: "fresh", Visible: true}
}
func loaded(t *testing.T, path string) *Store {
	t.Helper()
	s := New(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	return s
}
func record(t *testing.T, s *Store, item model.QuotaItem, second int64) {
	t.Helper()
	if err := s.Record("test", []model.QuotaItem{item}, time.Unix(second, 0)); err != nil {
		t.Fatal(err)
	}
}
func page(t *testing.T, s *Store) Page {
	t.Helper()
	p, err := s.Page("", 0)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDetectOnlyMeaningfulCompatibleIncreases(t *testing.T) {
	tests := []struct {
		name          string
		before, after model.QuotaItem
		want          int
	}{
		{"natural reset", quota(4), quota(100), 1},
		{"manual reset same window", quota(0), quota(80), 1},
		{"boundary", quota(30), quota(40), 1},
		{"small correction", quota(30), quota(39.99), 0},
		{"spending", quota(80), quota(40), 0},
		{"unchanged", quota(100), quota(100), 0},
	}
	for name, mutate := range map[string]func(*model.QuotaItem){
		"hidden":         func(q *model.QuotaItem) { q.Visible = false },
		"stale":          func(q *model.QuotaItem) { q.State = "stale" },
		"error":          func(q *model.QuotaItem) { q.Error = &model.ItemError{} },
		"zero limit":     func(q *model.QuotaItem) { q.Limit = 0 },
		"new limit":      func(q *model.QuotaItem) { q.Limit = 200 },
		"new unit":       func(q *model.QuotaItem) { q.Unit = "usd" },
		"new window":     func(q *model.QuotaItem) { q.Window.ID = "month" },
		"banked credit":  func(q *model.QuotaItem) { q.Window.Kind = "credit" },
		"nan":            func(q *model.QuotaItem) { q.Remaining = math.NaN() },
		"infinite limit": func(q *model.QuotaItem) { q.Limit = math.Inf(1) },
	} {
		after := quota(100)
		mutate(&after)
		tests = append(tests, struct {
			name          string
			before, after model.QuotaItem
			want          int
		}{name, quota(5), after, 0})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := loaded(t, "")
			record(t, s, tt.before, 1)
			if len(page(t, s).Events) != 0 {
				t.Fatal("initial reading generated alert")
			}
			record(t, s, tt.after, 2)
			record(t, s, tt.after, 3)
			if got := len(page(t, s).Events); got != tt.want {
				t.Fatalf("got %d events, want %d", got, tt.want)
			}
		})
	}
}

func TestRestartPreservesBaselinesAndCatchup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	s := loaded(t, path)
	record(t, s, quota(4), 1)
	s = loaded(t, path)
	record(t, s, quota(100), 2)
	stream := page(t, s).StreamID
	s = loaded(t, path)
	record(t, s, quota(100), 3)
	p := page(t, s)
	if p.StreamID != stream || len(p.Events) != 1 || p.Events[0].BeforePercent != 4 || p.Events[0].AfterPercent != 100 || p.Events[0].WindowKind != "weekly" {
		t.Fatalf("unexpected catchup: %+v", p)
	}
	p, err := s.Page(stream, 1)
	if err != nil || len(p.Events) != 0 {
		t.Fatalf("acknowledged event replayed: %+v %v", p, err)
	}
	p, err = s.Page("old-daemon", 9999)
	if err != nil || len(p.Events) != 1 {
		t.Fatalf("new stream hidden: %+v %v", p, err)
	}
}

func TestMissingItemsAndOldReadingsDoNotInventResets(t *testing.T) {
	s := loaded(t, "")
	record(t, s, quota(4), 2)
	record(t, s, quota(100), 1)
	if err := s.Record("test", nil, time.Unix(3, 0)); err != nil {
		t.Fatal(err)
	}
	record(t, s, quota(100), 4)
	if len(page(t, s).Events) != 0 {
		t.Fatal("out-of-order or reappearing item generated event")
	}
}

func TestFailedWriteDoesNotAdvanceBaseline(t *testing.T) {
	dir := t.TempDir()
	s := loaded(t, filepath.Join(dir, "snapshot.json"))
	record(t, s, quota(4), 1)
	path := s.path
	s.path = filepath.Join(dir, "not-directory", "state.json")
	if err := os.WriteFile(filepath.Join(dir, "not-directory"), []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("test", []model.QuotaItem{quota(100)}, time.Unix(2, 0)); err == nil {
		t.Fatal("expected write failure")
	}
	if len(page(t, s).Events) != 0 {
		t.Fatal("unpersisted event exposed")
	}
	s.path = path
	record(t, s, quota(100), 3)
	if len(page(t, s).Events) != 1 {
		t.Fatal("retry lost event")
	}
}

func TestCorruptLogIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reset-events.json")
	for _, content := range []string{"broken", "", `{}`, `{"version":1,"streamId":"s","baselines":{},"events":[{"id":2}]}`} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		s := New(filepath.Join(dir, "snapshot.json"))
		if s.Load() == nil {
			t.Fatalf("accepted corrupt log %q", content)
		}
		if s.Record("test", nil, time.Now()) == nil {
			t.Fatal("recorded despite corrupt log")
		}
		b, _ := os.ReadFile(path)
		if string(b) != content {
			t.Fatal("overwrote corrupt state")
		}
	}
}

func TestConcurrentProvidersAndPagination(t *testing.T) {
	s := loaded(t, filepath.Join(t.TempDir(), "snapshot.json"))
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			provider := string(rune('a' + i))
			for n := int64(1); n <= 60; n++ {
				remaining := 0.0
				if n%2 == 0 {
					remaining = 100
				}
				if err := s.Record(provider, []model.QuotaItem{quota(remaining)}, time.Unix(n, 0)); err != nil {
					t.Error(err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	first := page(t, s)
	if len(first.Events) != PageSize {
		t.Fatalf("page length %d", len(first.Events))
	}
	second, err := s.Page(first.StreamID, first.Events[len(first.Events)-1].ID)
	if err != nil || len(second.Events) != 20 {
		t.Fatalf("second page: %d %v", len(second.Events), err)
	}
	reloaded := loaded(t, filepath.Join(filepath.Dir(s.path), "snapshot.json"))
	if len(reloaded.state.Events) != 120 {
		t.Fatal("concurrent events lost on disk")
	}
}
