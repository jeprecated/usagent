package desktop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/resetevents"
)

type fakeNotifier struct {
	calls        []Notification
	replacements []uint32
	closed       []uint32
	signals      chan Signal
	fail         bool
}

func (f *fakeNotifier) Show(_ context.Context, replace uint32, n Notification) (uint32, error) {
	if f.fail {
		return 0, errors.New("desktop unavailable")
	}
	f.calls = append(f.calls, n)
	f.replacements = append(f.replacements, replace)
	return uint32(len(f.calls)), nil
}
func (f *fakeNotifier) Close(_ context.Context, id uint32) { f.closed = append(f.closed, id) }
func (f *fakeNotifier) Signals() <-chan Signal             { return f.signals }
func events(ids ...uint64) resetevents.Page {
	p := resetevents.Page{StreamID: "stream"}
	for _, id := range ids {
		p.Events = append(p.Events, resetevents.Event{ID: id, Label: "Claude weekly", Window: "W", BeforePercent: 4, AfterPercent: 100})
	}
	return p
}
func watcher() (*Watcher, *fakeNotifier, *[]Cursor) {
	f := &fakeNotifier{signals: make(chan Signal, 4)}
	saved := &[]Cursor{}
	w := &Watcher{Cursor: Cursor{Origin: "http://daemon"}, Notifier: f,
		Fetch: func(context.Context, Cursor) (resetevents.Page, error) { return events(), nil },
		Save:  func(c Cursor) error { *saved = append(*saved, c); return nil }}
	return w, f, saved
}
func TestDeliveryAndCloseNeverAcknowledge(t *testing.T) {
	ctx := context.Background()
	w, f, saved := watcher()
	if err := w.present(ctx, events(1, 2)); err != nil {
		t.Fatal(err)
	}
	if err := w.present(ctx, events(1, 2)); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || len(*saved) != 0 {
		t.Fatal("repeated delivery or automatic acknowledgement")
	}
	id := w.id
	if err := w.handle(ctx, Signal{ID: id, Closed: true}); err != nil {
		t.Fatal(err)
	}
	if len(*saved) != 0 {
		t.Fatal("close acknowledged unseen events")
	}
	if err := w.present(ctx, events(1, 2)); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 || f.replacements[1] != 0 {
		t.Fatal("unread popup not restored")
	}
	action := f.calls[1].Action
	if err := w.handle(ctx, Signal{ID: w.id, Action: action}); err != nil {
		t.Fatal(err)
	}
	if len(*saved) != 1 || (*saved)[0].After != 2 {
		t.Fatal("explicit action did not persist cursor")
	}
	if err := w.handle(ctx, Signal{ID: id, Action: action}); err != nil {
		t.Fatal(err)
	}
	if len(*saved) != 1 {
		t.Fatal("duplicate action acknowledged twice")
	}
}
func TestOldActionDoesNotAcknowledgeNewEvents(t *testing.T) {
	ctx := context.Background()
	w, f, saved := watcher()
	_ = w.present(ctx, events(1))
	oldAction := f.calls[0].Action
	_ = w.present(ctx, events(1, 2))
	if f.replacements[1] != 1 {
		t.Fatal("new events did not replace grouped popup")
	}
	if err := w.handle(ctx, Signal{ID: 1, Action: oldAction}); err != nil {
		t.Fatal(err)
	}
	if len(*saved) != 1 || (*saved)[0].After != 1 {
		t.Fatal("new unseen event was acknowledged")
	}
}
func TestAcknowledgementAfterClose(t *testing.T) {
	ctx := context.Background()
	w, f, saved := watcher()
	_ = w.present(ctx, events(1))
	id, action := w.id, f.calls[0].Action
	_ = w.handle(ctx, Signal{ID: id, Closed: true})
	_ = w.handle(ctx, Signal{ID: id, Action: action})
	if len(*saved) != 1 {
		t.Fatal("lost action arriving after close")
	}
}
func TestDeliveryAndPersistenceFailureRemainUnread(t *testing.T) {
	ctx := context.Background()
	w, f, saved := watcher()
	f.fail = true
	if w.present(ctx, events(1)) == nil {
		t.Fatal("expected delivery error")
	}
	if len(*saved) != 0 || w.Cursor.After != 0 {
		t.Fatal("failed delivery marked read")
	}
	f.fail = false
	_ = w.present(ctx, events(1))
	w.Save = func(Cursor) error { return errors.New("disk full") }
	if w.handle(ctx, Signal{ID: w.id, Action: f.calls[0].Action}) == nil {
		t.Fatal("expected save failure")
	}
	if w.Cursor.After != 0 {
		t.Fatal("failed persistence advanced cursor")
	}
}
func TestRestartAndStreamReplacement(t *testing.T) {
	ctx := context.Background()
	w, f, saved := watcher()
	_ = w.present(ctx, events(1))
	oldAction := f.calls[0].Action
	_ = w.handle(ctx, Signal{Restarted: true})
	_ = w.present(ctx, events(1))
	if len(f.calls) != 2 {
		t.Fatal("shell restart lost alert")
	}
	replacement := events(1)
	replacement.StreamID = "replacement"
	_ = w.present(ctx, replacement)
	_ = w.handle(ctx, Signal{ID: 1, Action: oldAction})
	if len(*saved) != 0 {
		t.Fatal("old stream action marked new events read")
	}
	if w.Cursor.StreamID != "replacement" || len(f.calls) != 3 {
		t.Fatal("replacement daemon event lost")
	}
}
func TestBatchFormattingAndValidation(t *testing.T) {
	ctx := context.Background()
	w, f, _ := watcher()
	p := events(1, 2, 3, 4, 5, 6)
	p.Events[5].Label = "<b>unsafe</b>"
	if err := w.present(ctx, p); err != nil {
		t.Fatal(err)
	}
	n := f.calls[0]
	if !strings.Contains(n.Summary, "6 quota") || !strings.Contains(n.Body, "2 earlier") || !strings.Contains(n.Body, "&lt;b&gt;") {
		t.Fatalf("bad summary: %+v", n)
	}
	if w.present(ctx, events(2, 1)) == nil {
		t.Fatal("accepted unordered response")
	}
	if w.present(ctx, resetevents.Page{}) == nil {
		t.Fatal("accepted invalid stream")
	}
}
func TestRunCancellationDoesNotAcknowledge(t *testing.T) {
	w, _, saved := watcher()
	w.Fetch = func(context.Context, Cursor) (resetevents.Page, error) { return events(1), nil }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.Run(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	if len(*saved) != 0 {
		t.Fatal("shutdown acknowledged event")
	}
}
