package desktop

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type testNotificationServer struct{ received chan wireNotification }
type wireNotification struct {
	app     string
	replace uint32
	actions []string
	hints   map[string]dbus.Variant
	expiry  int32
}

func (s *testNotificationServer) GetCapabilities() ([]string, *dbus.Error) {
	return []string{"body", "actions"}, nil
}
func (s *testNotificationServer) Notify(app string, replace uint32, icon, summary, body string, actions []string, hints map[string]dbus.Variant, expiry int32) (uint32, *dbus.Error) {
	s.received <- wireNotification{app, replace, actions, hints, expiry}
	return 42, nil
}
func (s *testNotificationServer) CloseNotification(uint32) *dbus.Error { return nil }

// Run with dbus-run-session -- env USAGENT_TEST_DBUS=1 go test ./internal/desktop.
// Never take over the real user's notification service during ordinary tests.
func TestDesktopDBusProtocol(t *testing.T) {
	if os.Getenv("USAGENT_TEST_DBUS") != "1" {
		t.Skip("requires isolated dbus-run-session")
	}
	server, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	obj := &testNotificationServer{received: make(chan wireNotification, 1)}
	if err := server.Export(obj, notificationPath, notificationService); err != nil {
		t.Fatal(err)
	}
	reply, err := server.RequestName(notificationService, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("request name: %v %v", reply, err)
	}
	d, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Disconnect()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := d.Show(ctx, 12, Notification{Summary: "test", Body: "body", Action: "read:stream:7"})
	if err != nil || id != 42 {
		t.Fatalf("Notify: %d %v", id, err)
	}
	got := <-obj.received
	if got.app != "usagent" || got.replace != 12 || got.expiry != 0 || got.hints["transient"].Value() != false || len(got.actions) != 2 || got.actions[0] != "read:stream:7" {
		t.Fatalf("bad wire payload: %+v", got)
	}
	if err := server.Emit(notificationPath, notificationService+".ActionInvoked", id, "read:stream:7"); err != nil {
		t.Fatal(err)
	}
	select {
	case sig := <-d.Signals():
		if sig.ID != id || sig.Action != "read:stream:7" {
			t.Fatalf("bad action: %+v", sig)
		}
	case <-ctx.Done():
		t.Fatal("no action signal")
	}
	if err := server.Emit(notificationPath, notificationService+".NotificationClosed", id, uint32(1)); err != nil {
		t.Fatal(err)
	}
	select {
	case sig := <-d.Signals():
		if sig.ID != id || !sig.Closed {
			t.Fatalf("bad close: %+v", sig)
		}
	case <-ctx.Done():
		t.Fatal("no close signal")
	}
	if _, err := server.ReleaseName(notificationService); err != nil {
		t.Fatal(err)
	}
	select {
	case sig := <-d.Signals():
		if !sig.Restarted {
			t.Fatalf("lost owner change: %+v", sig)
		}
	case <-ctx.Done():
		t.Fatal("no restart signal")
	}
}
