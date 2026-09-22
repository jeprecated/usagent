package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/jeprecated/usagent/internal/atomicfile"
	"github.com/jeprecated/usagent/internal/clientpolicy"
	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/desktop"
	"github.com/jeprecated/usagent/internal/resetevents"
)

type notifyOptions struct {
	UsageOptions
	StatePath string
	Interval  time.Duration
}

func parseNotifyFlags(args []string) (notifyOptions, error) {
	opts := notifyOptions{UsageOptions: UsageOptions{ConfigPath: config.DefaultConfigPath(), Timeout: 10 * time.Second}, Interval: time.Minute}
	fs := flag.NewFlagSet("usagent notify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "YAML config file")
	fs.StringVar(&opts.DaemonURL, "daemon-url", "", "daemon HTTP(S) origin")
	fs.StringVar(&opts.Host, "host", "", "daemon host")
	fs.IntVar(&opts.Port, "port", 0, "daemon port")
	fs.StringVar(&opts.StatePath, "state", "", "desktop acknowledgement state file")
	fs.DurationVar(&opts.Interval, "interval", opts.Interval, "cached event poll interval")
	fs.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "HTTP request timeout")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, fmt.Errorf("unexpected notify arguments")
	}
	if opts.Interval <= 0 || opts.Timeout <= 0 {
		return opts, fmt.Errorf("interval and timeout must be positive")
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "daemon-url":
			opts.DaemonURLSet = true
		case "host":
			opts.HostSet = true
		case "port":
			opts.PortSet = true
		}
	})
	return opts, nil
}

// Notification history belongs to the selected daemon. Never fall back to a
// different daemon, snapshot, credentials, or local provider polling.
func notifyOrigin(cfg config.Config, opts notifyOptions) (string, error) {
	resolved, err := clientpolicy.Resolve(cfg, opts.policyFlags())
	if err != nil {
		return "", err
	}
	if resolved.Local {
		return "", fmt.Errorf("notify requires a daemon; client.mode=local-only is unsupported")
	}
	return resolved.Origin, nil
}

func RunNotify(ctx context.Context, args []string, stderr io.Writer) error {
	opts, err := parseNotifyFlags(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return err
	}
	origin, err := notifyOrigin(cfg, opts)
	if err != nil {
		return err
	}
	path := opts.StatePath
	if path == "" {
		key := sha256.Sum256([]byte(origin))
		path = fmt.Sprintf("%%STATE%%/usagent/notifications-%x.json", key[:16])
	}
	path, err = config.ExpandRuntimePath(path)
	if err != nil {
		return err
	}
	unlock, err := lockNotifications(path)
	if err != nil {
		return err
	}
	defer unlock()
	cursor, err := loadNotificationCursor(path, origin)
	if err != nil {
		return err
	}
	notifier, err := desktop.Connect()
	if err != nil {
		return fmt.Errorf("connect to desktop notification bus: %w", err)
	}
	defer notifier.Disconnect()
	client := daemonClient{origin: origin, timeout: opts.Timeout}
	watcher := desktop.Watcher{
		Cursor: cursor, Notifier: notifier, Log: stderr,
		Fetch: func(ctx context.Context, c desktop.Cursor) (resetevents.Page, error) {
			var page resetevents.Page
			err := client.get(ctx, "/v1/reset-events", url.Values{"stream": {c.StreamID}, "after": {strconv.FormatUint(c.After, 10)}}, &page)
			return page, err
		},
		Save: func(c desktop.Cursor) error { return atomicfile.WriteJSON(path, c) },
	}
	return watcher.Run(ctx, opts.Interval)
}

func loadNotificationCursor(path, origin string) (desktop.Cursor, error) {
	cursor := desktop.Cursor{Origin: origin}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cursor, nil
	}
	if err != nil {
		return cursor, err
	}
	if err := json.Unmarshal(b, &cursor); err != nil {
		return cursor, fmt.Errorf("read notification state: %w", err)
	}
	if cursor.Origin != origin {
		return cursor, fmt.Errorf("notification state belongs to another daemon; use a separate --state file")
	}
	if cursor.After > 0 && cursor.StreamID == "" {
		return cursor, fmt.Errorf("invalid notification cursor")
	}
	return cursor, nil
}

func lockNotifications(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("notification watcher already running or lock unavailable: %w", err)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
