package clientpolicy

import (
	"strings"
	"testing"

	"github.com/jeprecated/usagent/internal/config"
)

func normalizedConfig(t *testing.T, mutate func(*config.Config)) config.Config {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(&cfg)
	}
	cfg, err := config.Normalize(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestResolveDestinationPrecedenceAndPartialLegacyOverrides(t *testing.T) {
	cfg := normalizedConfig(t, func(cfg *config.Config) {
		cfg.Server.Host = "configured-host"
		cfg.Server.Port = 8787
		cfg.Client.URL = "https://central.example:9443/"
		cfg.Client.Mode = config.ClientModeRequireDaemon
	})

	tests := []struct {
		name       string
		opts       FlagOptions
		wantOrigin string
		wantSource DestinationSource
	}{
		{name: "explicit daemon URL", opts: FlagOptions{DaemonURLSet: true, DaemonURL: "https://one-shot.example:8443/"}, wantOrigin: "https://one-shot.example:8443", wantSource: DestinationDaemonURL},
		{name: "host only selects legacy destination wholesale", opts: FlagOptions{HostSet: true, Host: "legacy-host"}, wantOrigin: "http://legacy-host:8787", wantSource: DestinationLegacy},
		{name: "port only selects legacy destination wholesale", opts: FlagOptions{PortSet: true, Port: 9999}, wantOrigin: "http://configured-host:9999", wantSource: DestinationLegacy},
		{name: "client URL", opts: FlagOptions{}, wantOrigin: "https://central.example:9443", wantSource: DestinationClientURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(cfg, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if got.Mode != config.ClientModeRequireDaemon || got.Origin != tt.wantOrigin || got.Source != tt.wantSource || got.Local {
				t.Fatalf("resolution=%+v", got)
			}
		})
	}
}

func TestResolveServerDerivedWildcardAndIPv6Origins(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		{host: "0.0.0.0", want: "http://127.0.0.1:8787"},
		{host: "::", want: "http://[::1]:8787"},
		{host: "[::]", want: "http://[::1]:8787"},
		{host: "::1", want: "http://[::1]:8787"},
		{host: "[::1]", want: "http://[::1]:8787"},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			cfg := normalizedConfig(t, func(cfg *config.Config) {
				cfg.Server.Host = tt.host
				cfg.Client.URL = ""
			})
			got, err := Resolve(cfg, FlagOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if got.Origin != tt.want || got.Source != DestinationServer {
				t.Fatalf("resolution=%+v want origin %q", got, tt.want)
			}
		})
	}
}

func TestResolveConflictMatrixAndLocalSelection(t *testing.T) {
	prefer := normalizedConfig(t, nil)
	local := normalizedConfig(t, func(cfg *config.Config) { cfg.Client.Mode = config.ClientModeLocalOnly })
	require := normalizedConfig(t, func(cfg *config.Config) { cfg.Client.Mode = config.ClientModeRequireDaemon })

	conflicts := []struct {
		name string
		cfg  config.Config
		opts FlagOptions
		text string
	}{
		{name: "daemon URL and host", cfg: prefer, opts: FlagOptions{DaemonURLSet: true, DaemonURL: "http://daemon", HostSet: true, Host: "host"}, text: "--host"},
		{name: "daemon URL and port", cfg: prefer, opts: FlagOptions{DaemonURLSet: true, DaemonURL: "http://daemon", PortSet: true, Port: 9999}, text: "--port"},
		{name: "daemon URL and offline", cfg: prefer, opts: FlagOptions{DaemonURLSet: true, DaemonURL: "http://daemon", Offline: true}, text: "--offline"},
		{name: "daemon URL and local only", cfg: local, opts: FlagOptions{DaemonURLSet: true, DaemonURL: "http://daemon"}, text: "local-only"},
		{name: "require daemon and offline", cfg: require, opts: FlagOptions{Offline: true}, text: "require-daemon"},
	}
	for _, tt := range conflicts {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Resolve(tt.cfg, tt.opts); err == nil || !strings.Contains(err.Error(), tt.text) {
				t.Fatalf("error=%v want text %q", err, tt.text)
			}
		})
	}

	for _, tt := range []struct {
		name string
		cfg  config.Config
		opts FlagOptions
	}{
		{name: "prefer offline ignores legacy flags", cfg: prefer, opts: FlagOptions{Offline: true, HostSet: true, Host: "unused", PortSet: true, Port: 9999}},
		{name: "local only ignores legacy flags", cfg: local, opts: FlagOptions{HostSet: true, Host: "unused", PortSet: true, Port: 9999}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.cfg, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Local || got.Origin != "" || got.Source != DestinationNone {
				t.Fatalf("resolution=%+v", got)
			}
		})
	}

	got, err := Resolve(require, FlagOptions{HostSet: true, Host: "central", PortSet: true, Port: 8788})
	if err != nil {
		t.Fatal(err)
	}
	if got.Local || got.Origin != "http://central:8788" || got.Source != DestinationLegacy {
		t.Fatalf("require-daemon legacy resolution=%+v", got)
	}
}

func TestResolveRejectsMalformedExplicitDaemonURL(t *testing.T) {
	cfg := normalizedConfig(t, nil)
	for _, raw := range []string{"", "unix:///tmp/usagent.sock", "http://host/v1", "http://user@host"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := Resolve(cfg, FlagOptions{DaemonURLSet: true, DaemonURL: raw}); err == nil {
				t.Fatalf("Resolve daemon URL %q succeeded", raw)
			}
		})
	}
}
