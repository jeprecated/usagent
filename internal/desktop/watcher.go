// Package desktop delivers reset events without treating delivery or expiry as acknowledgement.
package desktop

import (
	"context"
	"fmt"
	"html"
	"io"
	"strings"
	"time"

	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/resetevents"
)

type Cursor struct {
	Origin   string `json:"origin"`
	StreamID string `json:"streamId"`
	After    uint64 `json:"after"`
}

type Signal struct {
	ID        uint32
	Action    string
	Closed    bool
	Restarted bool
}

type Notification struct{ Summary, Body, Action string }

type Notifier interface {
	Show(context.Context, uint32, Notification) (uint32, error)
	Close(context.Context, uint32)
	Signals() <-chan Signal
}

type Watcher struct {
	Policy    config.NotificationConfig
	Cursor    Cursor
	Fetch     func(context.Context, Cursor) (resetevents.Page, error)
	Save      func(Cursor) error
	Notifier  Notifier
	Log       io.Writer
	id        uint32
	shownLast uint64
	actions   map[string]Cursor
}

// Run retries outages on the ordinary desktop poll interval. It never polls
// provider APIs. Alerts require an explicit action; filtered events are skipped.
func (w *Watcher) Run(ctx context.Context, interval time.Duration) error {
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if w.id != 0 {
			w.Notifier.Close(closeCtx, w.id)
		}
	}()
	w.poll(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case sig, ok := <-w.Notifier.Signals():
			if !ok {
				return fmt.Errorf("desktop notification connection closed")
			}
			if err := w.handle(ctx, sig); err != nil {
				return err
			}
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

func (w *Watcher) poll(ctx context.Context) {
	for ctx.Err() == nil {
		before := w.Cursor
		page, err := w.Fetch(ctx, w.Cursor)
		if err == nil {
			err = w.present(ctx, page)
		}
		if err != nil {
			if ctx.Err() == nil && w.Log != nil {
				fmt.Fprintf(w.Log, "reset notifications: %v; will retry\n", err)
			}
			return
		}
		// Drain full pages of ignored events without waiting another poll interval.
		if len(page.Events) < resetevents.PageSize || w.Cursor == before {
			return
		}
	}
}

func (w *Watcher) present(ctx context.Context, page resetevents.Page) error {
	if page.StreamID == "" {
		return fmt.Errorf("daemon returned an invalid reset event stream")
	}
	after := w.Cursor.After
	if page.StreamID != w.Cursor.StreamID {
		after = 0
	}
	for _, event := range page.Events {
		if event.ID <= after {
			return fmt.Errorf("daemon returned unordered reset events")
		}
		after = event.ID
	}
	if page.StreamID != w.Cursor.StreamID {
		if w.id != 0 {
			w.Notifier.Close(ctx, w.id)
		}
		w.id, w.shownLast, w.actions = 0, 0, nil
		w.Cursor.StreamID, w.Cursor.After = page.StreamID, 0
	}
	if len(page.Events) == 0 {
		return nil
	}
	var alerts []resetevents.Event
	for _, event := range page.Events {
		if w.shouldNotify(event) {
			alerts = append(alerts, event)
		}
	}
	if len(alerts) == 0 {
		cursor := Cursor{Origin: w.Cursor.Origin, StreamID: page.StreamID, After: after}
		if err := w.Save(cursor); err != nil {
			return fmt.Errorf("save skipped notification events: %w", err)
		}
		w.Cursor = cursor
		return nil
	}
	last := alerts[len(alerts)-1].ID
	if w.id != 0 && last == w.shownLast {
		return nil
	}
	action := fmt.Sprintf("read:%s:%d", page.StreamID, last)
	id, err := w.Notifier.Show(ctx, w.id, formatNotification(alerts, action))
	if err != nil {
		return err
	}
	w.id, w.shownLast = id, last
	if w.actions == nil {
		w.actions = map[string]Cursor{}
	}
	w.actions[action] = Cursor{Origin: w.Cursor.Origin, StreamID: page.StreamID, After: last}
	return nil
}

func (w *Watcher) handle(ctx context.Context, sig Signal) error {
	if sig.Restarted {
		w.id, w.shownLast, w.actions = 0, 0, nil
		return nil
	}
	if sig.Action != "" {
		cursor, ok := w.actions[sig.Action]
		if !ok || cursor.StreamID != w.Cursor.StreamID || cursor.After <= w.Cursor.After {
			return nil
		}
		// Persist first. If this fails, leaving alerts unread is safer than losing them.
		if err := w.Save(cursor); err != nil {
			return fmt.Errorf("save notification acknowledgement: %w", err)
		}
		w.Cursor = cursor
		if w.id != 0 {
			w.Notifier.Close(ctx, w.id)
		}
		w.id, w.shownLast, w.actions = 0, 0, nil
		w.poll(ctx)
	} else if sig.Closed && sig.ID == w.id {
		// Expiry, overflow, DND, and shell restarts are not proof that someone read it.
		// Keep action tokens: some shells emit Closed immediately before ActionInvoked.
		w.id = 0
	}
	return nil
}

// Older stored events have only a display window label, not a window kind.
func (w *Watcher) shouldNotify(event resetevents.Event) bool {
	kind := event.WindowKind
	window := strings.ToLower(strings.TrimSpace(event.Window))
	if kind == "" {
		switch window {
		case "w", "weekly":
			kind = "weekly"
		case "m", "monthly":
			kind = "monthly"
		case "f":
			if event.Provider == "claude-code" {
				kind = "weekly"
			}
		case "extra":
			if event.Provider == "claude-code" {
				kind = "monthly"
			}
		}
	}
	switch kind {
	case "weekly":
		return w.Policy.Weekly
	case "monthly":
		return w.Policy.Monthly
	}
	return (window == "s" || window == "5h") && event.BeforePercent < w.Policy.SessionBelowPercent
}

func formatNotification(events []resetevents.Event, action string) Notification {
	summary := "Quota replenished"
	if len(events) > 1 {
		summary = fmt.Sprintf("%d quota replenishments", len(events))
	}
	lines := []string{}
	start := max(0, len(events)-4)
	if start > 0 {
		lines = append(lines, fmt.Sprintf("%d earlier replenishments; latest:", start))
	}
	for _, e := range events[start:] {
		label := e.Label
		if label == "" {
			label = e.Provider + " " + e.Window
		}
		lines = append(lines, fmt.Sprintf("%s: %.0f%% → %.0f%% remaining (%s)", html.EscapeString(label), e.BeforePercent, e.AfterPercent, time.UnixMilli(e.ObservedAt).Local().Format("Jan 2 15:04")))
	}
	lines = append(lines, "Mark read to clear these alerts.")
	return Notification{Summary: summary, Body: strings.Join(lines, "\n"), Action: action}
}
