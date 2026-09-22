package desktop

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/resetevents"
)

func TestNotificationPolicy(t *testing.T) {
	w, _, _ := watcher()
	for _, tt := range []struct {
		name, kind, window, provider string
		before                       float64
		want                         bool
	}{
		{"weekly high", "weekly", "W", "", 80, true},
		{"monthly high", "monthly", "M", "", 80, true},
		{"custom weekly label", "weekly", "Team", "", 80, true},
		{"legacy weekly", "", "W", "", 80, true},
		{"legacy monthly", "", "M", "", 80, true},
		{"legacy fable", "", "F", "claude-code", 80, true},
		{"legacy extra", "", "Extra", "claude-code", 80, true},
		{"screenshot", "rolling", "S", "claude-code", 64, false},
		{"session below", "rolling", "S", "claude-code", 19.99, true},
		{"session boundary", "rolling", "S", "chatgpt", 20, false},
		{"five hour empty", "rolling", "5h", "z-ai", 0, true},
		{"five hour boundary", "rolling", "5h", "z-ai", 20, false},
		{"legacy session", "", "S", "claude-code", 19, true},
		{"unknown", "custom", "Other", "", 0, false},
		{"daily", "daily", "D", "", 0, false},
		{"kind wins over legacy label", "daily", "W", "", 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := resetevents.Event{WindowKind: tt.kind, Window: tt.window, Provider: tt.provider, BeforePercent: tt.before, AfterPercent: 100}
			if got := w.shouldNotify(e); got != tt.want {
				t.Fatalf("shouldNotify(%+v) = %v", e, got)
			}
		})
	}
}

func TestConfiguredNotificationPolicy(t *testing.T) {
	for _, tt := range []struct {
		name                     string
		policy                   config.NotificationConfig
		weekly, monthly, session bool
	}{
		{"disabled", config.NotificationConfig{}, false, false, false},
		{"weekly only", config.NotificationConfig{Weekly: true}, true, false, false},
		{"monthly only", config.NotificationConfig{Monthly: true}, false, true, false},
		{"higher session threshold", config.NotificationConfig{SessionBelowPercent: 70}, false, false, true},
		{"strict threshold", config.NotificationConfig{SessionBelowPercent: 64}, false, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w, f, saved := watcher()
			w.Policy = tt.policy
			p := resetevents.Page{StreamID: "stream", Events: []resetevents.Event{
				{ID: 1, WindowKind: "weekly", Label: "Weekly"},
				{ID: 2, WindowKind: "monthly", Label: "Monthly"},
				ignored(3),
			}}
			for i, want := range []bool{tt.weekly, tt.monthly, tt.session} {
				if got := w.shouldNotify(p.Events[i]); got != want {
					t.Fatalf("event %d: got %v, want %v", i, got, want)
				}
			}
			if err := w.present(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			if !tt.weekly && !tt.monthly && !tt.session {
				if len(f.calls) != 0 || w.Cursor.After != 3 {
					t.Fatal("disabled alerts were not skipped")
				}
			} else if len(f.calls) != 1 || len(*saved) != 0 {
				t.Fatal("configured alerts were not left unread")
			}
		})
	}
}

func ignored(id uint64) resetevents.Event {
	return resetevents.Event{ID: id, Label: "Claude 5h", Window: "S", BeforePercent: 64, AfterPercent: 100}
}

func TestIgnoredEventsAreSavedWithoutPopup(t *testing.T) {
	w, f, saved := watcher()
	p := resetevents.Page{StreamID: "stream", Events: []resetevents.Event{ignored(1), ignored(2)}}
	if err := w.present(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 || len(*saved) != 1 || w.Cursor.After != 2 {
		t.Fatalf("calls=%v saved=%v cursor=%+v", f.calls, *saved, w.Cursor)
	}
	// The persisted cursor is sufficient to resume after a desktop restart.
	restarted, _, _ := watcher()
	restarted.Cursor = (*saved)[0]
	if err := restarted.present(context.Background(), events(3)); err != nil {
		t.Fatal(err)
	}
}

func TestIgnoredEventsSaveFailureRetries(t *testing.T) {
	w, f, _ := watcher()
	w.Save = func(Cursor) error { return errors.New("disk full") }
	p := resetevents.Page{StreamID: "stream", Events: []resetevents.Event{ignored(1)}}
	if w.present(context.Background(), p) == nil {
		t.Fatal("expected save failure")
	}
	if w.Cursor.After != 0 || len(f.calls) != 0 {
		t.Fatal("failed save skipped events or displayed popup")
	}
}

func TestMixedPageKeepsAlertsUnread(t *testing.T) {
	ctx := context.Background()
	w, f, saved := watcher()
	p := events(1, 2, 3)
	p.Events[0], p.Events[2] = ignored(1), ignored(3)
	if err := w.present(ctx, p); err != nil {
		t.Fatal(err)
	}
	if len(*saved) != 0 || len(f.calls) != 1 || strings.Contains(f.calls[0].Body, "5h") || f.calls[0].Summary != "Quota replenished" {
		t.Fatalf("saved=%v calls=%v", *saved, f.calls)
	}
	// A new ignored event must not reissue an unchanged alert.
	p.Events = append(p.Events, ignored(4))
	if err := w.present(ctx, p); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 {
		t.Fatal("ignored event reissued popup")
	}
	if err := w.handle(ctx, Signal{ID: w.id, Action: f.calls[0].Action}); err != nil {
		t.Fatal(err)
	}
	if w.Cursor.After != 2 {
		t.Fatal("action advanced past displayed alert")
	}
	if err := w.present(ctx, resetevents.Page{StreamID: "stream", Events: p.Events[2:]}); err != nil {
		t.Fatal(err)
	}
	if w.Cursor.After != 4 || len(f.calls) != 1 {
		t.Fatal("trailing ignored events were not skipped")
	}
}

func TestFullIgnoredPageDoesNotBlockNextAlert(t *testing.T) {
	w, f, _ := watcher()
	calls := 0
	w.Fetch = func(_ context.Context, c Cursor) (resetevents.Page, error) {
		calls++
		if calls > 2 {
			t.Fatal("unexpected extra fetch")
		}
		if c.After == 0 {
			p := resetevents.Page{StreamID: "stream"}
			for id := uint64(1); id <= resetevents.PageSize; id++ {
				p.Events = append(p.Events, ignored(id))
			}
			return p, nil
		}
		if c.After != resetevents.PageSize {
			t.Fatalf("unexpected cursor %+v", c)
		}
		return events(resetevents.PageSize + 1), nil
	}
	w.poll(context.Background())
	if calls != 2 || len(f.calls) != 1 || w.Cursor.After != resetevents.PageSize {
		t.Fatalf("calls=%d notifications=%v cursor=%+v", calls, f.calls, w.Cursor)
	}
}
