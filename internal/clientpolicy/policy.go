package clientpolicy

import (
	"fmt"
	"net"
	"strings"

	"github.com/jeprecated/usagent/internal/config"
)

type DestinationSource string

const (
	DestinationNone      DestinationSource = "none"
	DestinationDaemonURL DestinationSource = "daemon-url"
	DestinationLegacy    DestinationSource = "legacy-host-port"
	DestinationClientURL DestinationSource = "client-url"
	DestinationServer    DestinationSource = "server"
)

// FlagOptions keeps raw values and flag presence separate. Presence cannot be
// reconstructed after legacy host/port overrides have been applied to Config.
type FlagOptions struct {
	DaemonURL    string
	DaemonURLSet bool
	Host         string
	HostSet      bool
	Port         int
	PortSet      bool
	Offline      bool
}

// Resolution describes whether a client invocation should use a daemon and,
// when it should, the exact origin selected by the precedence contract.
type Resolution struct {
	Mode   config.ClientMode
	Origin string
	Source DestinationSource
	Local  bool
}

func Resolve(cfg config.Config, opts FlagOptions) (Resolution, error) {
	resolution := Resolution{Mode: cfg.Client.Mode}

	if opts.DaemonURLSet {
		if opts.HostSet || opts.PortSet {
			return resolution, fmt.Errorf("--daemon-url cannot be combined with --host or --port")
		}
		if opts.Offline {
			return resolution, fmt.Errorf("--daemon-url cannot be combined with --offline")
		}
		if cfg.Client.Mode == config.ClientModeLocalOnly {
			return resolution, fmt.Errorf("--daemon-url cannot be used when client.mode=%s", config.ClientModeLocalOnly)
		}
		origin, err := config.NormalizeClientURL(opts.DaemonURL)
		if err != nil {
			return resolution, fmt.Errorf("invalid --daemon-url: %w", err)
		}
		if origin == "" {
			return resolution, fmt.Errorf("--daemon-url must not be empty")
		}
		resolution.Origin = origin
		resolution.Source = DestinationDaemonURL
		return resolution, nil
	}

	if opts.Offline {
		if cfg.Client.Mode == config.ClientModeRequireDaemon {
			return resolution, fmt.Errorf("--offline cannot be used when client.mode=%s", config.ClientModeRequireDaemon)
		}
		resolution.Local = true
		resolution.Source = DestinationNone
		return resolution, nil
	}
	if cfg.Client.Mode == config.ClientModeLocalOnly {
		resolution.Local = true
		resolution.Source = DestinationNone
		return resolution, nil
	}

	if opts.HostSet || opts.PortSet {
		host, port := cfg.Server.Host, cfg.Server.Port
		if opts.HostSet {
			host = opts.Host
		}
		if opts.PortSet {
			port = opts.Port
		}
		origin, err := serverOrigin(host, port)
		if err != nil {
			return resolution, err
		}
		resolution.Origin = origin
		resolution.Source = DestinationLegacy
		return resolution, nil
	}
	if cfg.Client.URL != "" {
		resolution.Origin = cfg.Client.URL
		resolution.Source = DestinationClientURL
		return resolution, nil
	}
	origin, err := serverOrigin(cfg.Server.Host, cfg.Server.Port)
	if err != nil {
		return resolution, err
	}
	resolution.Origin = origin
	resolution.Source = DestinationServer
	return resolution, nil
}

func serverOrigin(host string, port int) (string, error) {
	if port <= 0 || port > 65535 {
		return "", fmt.Errorf("daemon port must be 1..65535")
	}
	host = clientHost(host)
	return "http://" + net.JoinHostPort(host, fmt.Sprint(port)), nil
}

func clientHost(host string) string {
	switch host {
	case "", "0.0.0.0":
		return "127.0.0.1"
	case "::", "[::]":
		return "::1"
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	return host
}
