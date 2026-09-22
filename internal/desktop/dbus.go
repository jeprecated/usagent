package desktop

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/godbus/dbus/v5"
)

const notificationService = "org.freedesktop.Notifications"
const notificationPath = dbus.ObjectPath("/org/freedesktop/Notifications")

type Desktop struct {
	conn    *dbus.Conn
	signals chan Signal
	owner   string // owner of the last delivered notification; accessed by the watcher only
}

func Connect() (*Desktop, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	d := &Desktop{conn: conn, signals: make(chan Signal, 64)}
	raw := make(chan *dbus.Signal, 64)
	conn.Signal(raw)
	for _, options := range [][]dbus.MatchOption{
		{dbus.WithMatchSender(notificationService), dbus.WithMatchObjectPath(notificationPath), dbus.WithMatchInterface(notificationService)},
		{dbus.WithMatchSender("org.freedesktop.DBus"), dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, notificationService)},
	} {
		if err := conn.AddMatchSignal(options...); err != nil {
			conn.Close()
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var owner string
	_ = conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, notificationService).Store(&owner)
	go d.forward(raw, owner)
	return d, nil
}

func (d *Desktop) Disconnect()            { _ = d.conn.Close() }
func (d *Desktop) Signals() <-chan Signal { return d.signals }

func (d *Desktop) forward(raw <-chan *dbus.Signal, owner string) {
	defer close(d.signals)
	for sig := range raw {
		event := Signal{}
		if sig.Name == "org.freedesktop.DBus.NameOwnerChanged" && sig.Sender == "org.freedesktop.DBus" {
			var name, old, next string
			if dbus.Store(sig.Body, &name, &old, &next) != nil || name != notificationService {
				continue
			}
			owner = next
			event.Restarted = true
		} else {
			if sig.Sender != owner || sig.Path != notificationPath {
				continue
			}
			switch sig.Name {
			case notificationService + ".ActionInvoked":
				if dbus.Store(sig.Body, &event.ID, &event.Action) != nil {
					continue
				}
			case notificationService + ".NotificationClosed":
				var reason uint32
				if dbus.Store(sig.Body, &event.ID, &reason) != nil {
					continue
				}
				event.Closed = true
			default:
				continue
			}
		}
		select {
		case d.signals <- event:
		case <-d.conn.Context().Done():
			return
		}
	}
}

func (d *Desktop) Show(ctx context.Context, replace uint32, n Notification) (uint32, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	object := d.conn.Object(notificationService, notificationPath)
	var capabilities []string
	if err := object.CallWithContext(ctx, notificationService+".GetCapabilities", 0).Store(&capabilities); err != nil {
		return 0, err
	}
	if !slices.Contains(capabilities, "actions") {
		return 0, fmt.Errorf("desktop notifications must support actions (Mark read)")
	}
	// Address the unique owner, not the service name: a shell restart between
	// calls must not replace or close an unrelated notification with a recycled ID.
	var owner string
	if err := d.conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, notificationService).Store(&owner); err != nil {
		return 0, err
	}
	object = d.conn.Object(owner, notificationPath)
	if owner != d.owner {
		replace = 0
	}
	hints := map[string]dbus.Variant{
		"urgency":   dbus.MakeVariant(byte(1)),
		"resident":  dbus.MakeVariant(true),
		"transient": dbus.MakeVariant(false),
	}
	var id uint32
	err := object.CallWithContext(ctx, notificationService+".Notify", 0,
		"usagent", replace, "dialog-information", n.Summary, n.Body,
		[]string{n.Action, "Mark read"}, hints, int32(0),
	).Store(&id)
	if err == nil && id == 0 {
		err = fmt.Errorf("desktop returned an invalid notification ID")
	}
	if err == nil {
		d.owner = owner
	}
	return id, err
}

func (d *Desktop) Close(ctx context.Context, id uint32) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if d.owner == "" {
		return
	}
	_ = d.conn.Object(d.owner, notificationPath).CallWithContext(ctx, notificationService+".CloseNotification", 0, id).Err
}
