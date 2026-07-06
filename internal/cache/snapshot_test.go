package cache

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmalloc/usagent/internal/model"
)

func TestStoreSaveLoadAtomicSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state", "snapshot.json")
	store := NewStore(path)
	now := time.UnixMilli(1000)
	store.UpsertSuccess("claude-code", "Claude", []model.QuotaItem{{ID: "x", Provider: "claude-code", Label: "X", Window: model.Window{ID: "session", Label: "S", Kind: "rolling"}, Unit: "percent", Limit: 100, Used: 1, Remaining: 99, PercentUsed: 1, Visible: true}}, now, 1000, 2000)
	if err := store.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state not saved: %v", err)
	}
	loaded := NewStore(path)
	if err := loaded.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	usage := loaded.Overview(now, []model.Provider{{ID: "claude-code", Label: "Claude", Source: "pull"}})
	if len(usage.QuotaItems) != 1 || usage.QuotaItems[0].ID != "x" {
		t.Fatalf("items=%+v", usage.QuotaItems)
	}
}

func TestFailureAfterPriorSuccessServesStaleCachedItems(t *testing.T) {
	for _, tc := range []struct{ id, label string }{{"claude-code", "Claude"}, {"openai", "OpenAI"}, {"z-ai", "z.ai"}, {"custom-one", "Custom"}} {
		t.Run(tc.id, func(t *testing.T) {
			store := NewStore("")
			store.UpsertSuccess(tc.id, tc.label, []model.QuotaItem{{ID: "x", Provider: tc.id, Label: "X", Window: model.Window{ID: "session", Label: "S", Kind: "rolling"}, Unit: "percent", Limit: 100, Used: 10, Remaining: 90, PercentUsed: 10, Severity: "ok", Visible: true}}, time.UnixMilli(1000), 100, 200)
			store.MarkFailure(tc.id, tc.label, time.UnixMilli(1500), 3*time.Second, errors.New("rate limited"), 100)
			usage := store.Overview(time.UnixMilli(1500), []model.Provider{{ID: tc.id, Label: tc.label, Source: "pull"}})
			if len(usage.QuotaItems) != 1 {
				t.Fatalf("items=%+v", usage.QuotaItems)
			}
			item := usage.QuotaItems[0]
			if item.State != "stale" || item.Severity != "warning" || item.Error == nil {
				t.Fatalf("item=%+v", item)
			}
			if usage.Providers[0].State != model.ProviderStateError {
				t.Fatalf("provider state=%s", usage.Providers[0].State)
			}
		})
	}
}
