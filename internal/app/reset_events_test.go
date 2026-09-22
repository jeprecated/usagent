package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/model"
	"github.com/jeprecated/usagent/internal/providers"
)

func TestResetEventsOnlyFromSuccessfulDaemonRefreshes(t *testing.T) {
	cfg := config.Default()
	cfg.Server.StatePath = filepath.Join(t.TempDir(), "snapshot.json")
	a := New(cfg, nil)
	p := &slowProvider{id: "fake"}
	result := providers.Result{Items: []model.QuotaItem{{ID: "weekly", Provider: "fake", Visible: true, State: "fresh", Unit: "percent", Limit: 100, Remaining: 4}}}
	a.recordRefresh(p, result, nil, time.Unix(1, 0))
	if _, err := os.Stat(filepath.Join(filepath.Dir(cfg.Server.StatePath), "reset-events.json")); !os.IsNotExist(err) {
		t.Fatal("local refresh touched event log")
	}
	if err := a.EnableResetEvents(); err != nil {
		t.Fatal(err)
	}
	a.recordRefresh(p, result, nil, time.Unix(2, 0))
	result.Items[0].Remaining = 100
	a.recordRefresh(p, result, errors.New("provider unavailable"), time.Unix(3, 0))
	events, err := a.ResetEvents.Page("", 0)
	if err != nil || len(events.Events) != 0 {
		t.Fatalf("failed refresh generated alert: %+v %v", events, err)
	}
	a.recordRefresh(p, result, nil, time.Unix(4, 0))
	events, err = a.ResetEvents.Page("", 0)
	if err != nil || len(events.Events) != 1 {
		t.Fatalf("successful refresh lost event: %+v %v", events, err)
	}
	restarted := New(cfg, nil)
	if err := restarted.EnableResetEvents(); err != nil {
		t.Fatal(err)
	}
	events, err = restarted.ResetEvents.Page("", 0)
	if err != nil || len(events.Events) != 1 {
		t.Fatal("daemon restart lost alert")
	}
}
