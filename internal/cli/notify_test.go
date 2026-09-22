package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/jeprecated/usagent/internal/atomicfile"
	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/desktop"
)

func TestNotifyFlagsAndDestination(t *testing.T) {
	for _, args := range [][]string{{"--interval", "0s"}, {"--timeout", "-1s"}, {"--offline"}, {"extra"}} {
		if _, err := parseNotifyFlags(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	cfg := config.Default()
	cfg.Client.URL = "http://central:8787"
	for _, mode := range []config.ClientMode{config.ClientModeRequireDaemon, config.ClientModePreferDaemon} {
		cfg.Client.Mode = mode
		opts, _ := parseNotifyFlags(nil)
		got, err := notifyOrigin(cfg, opts)
		if err != nil || got != cfg.Client.URL {
			t.Fatalf("destination %q %v", got, err)
		}
	}
	opts, _ := parseNotifyFlags([]string{"--daemon-url", "http://override:8787"})
	got, err := notifyOrigin(cfg, opts)
	if err != nil || got != "http://override:8787" {
		t.Fatalf("override %q %v", got, err)
	}
	opts, _ = parseNotifyFlags([]string{"--daemon-url", "http://override:8787", "--host", "localhost"})
	if _, err := notifyOrigin(cfg, opts); err == nil {
		t.Fatal("accepted conflicting flags")
	}
	cfg.Client.Mode = config.ClientModeLocalOnly
	opts, _ = parseNotifyFlags(nil)
	if _, err := notifyOrigin(cfg, opts); err == nil {
		t.Fatal("allowed local provider polling")
	}
}
func TestNotificationCursorAndSingleInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notifications.json")
	cursor, err := loadNotificationCursor(path, "http://central")
	if err != nil || cursor.After != 0 {
		t.Fatalf("initial cursor %+v %v", cursor, err)
	}
	cursor.StreamID = "stream"
	cursor.After = 42
	if err := atomicfile.WriteJSON(path, cursor); err != nil {
		t.Fatal(err)
	}
	got, err := loadNotificationCursor(path, cursor.Origin)
	if err != nil || got != cursor {
		t.Fatalf("lost saved cursor %+v %v", got, err)
	}
	if _, err := loadNotificationCursor(path, "http://other"); err == nil {
		t.Fatal("reused another daemon's cursor")
	}
	unlock, err := lockNotifications(path)
	if err != nil {
		t.Fatal(err)
	}
	if release, err := lockNotifications(path); err == nil {
		release()
		t.Fatal("allowed duplicate watcher")
	}
	unlock()
	release, err := lockNotifications(path)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadNotificationCursor(path, cursor.Origin); err == nil {
		t.Fatal("ignored corrupt cursor")
	}
}
func TestNotifyRejectsLocalModeBeforeDesktopOrProviderAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("client:\n  mode: local-only\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RunNotify(context.Background(), []string{"--config", path}, io.Discard); err == nil {
		t.Fatal("accepted local mode")
	}
}
func TestInvalidCursor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := atomicfile.WriteJSON(path, desktop.Cursor{Origin: "http://daemon", After: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadNotificationCursor(path, "http://daemon"); err == nil {
		t.Fatal("accepted cursor without stream")
	}
}
