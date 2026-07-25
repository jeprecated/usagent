package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/jeprecated/usagent/internal/clientpolicy"
	"github.com/jeprecated/usagent/internal/config"
)

type daemonClient struct {
	origin  string
	timeout time.Duration
}

func (c daemonClient) get(ctx context.Context, path string, query url.Values, target any) error {
	u, err := url.Parse(c.origin)
	if err != nil {
		return err
	}
	u.Path = path
	u.RawQuery = query.Encode()

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("GET %s returned HTTP %d", u.String(), resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode GET %s: %w", u.String(), err)
	}
	return nil
}

type daemonAccessResult struct {
	useLocal   bool
	diagnostic error
}

// accessDaemon is the common client policy state machine used by every CLI
// daemon-backed command. The callback performs one endpoint-specific request.
func accessDaemon(
	ctx context.Context,
	cfg config.Config,
	configPath string,
	flags clientpolicy.FlagOptions,
	timeout time.Duration,
	request func(daemonClient) error,
) (daemonAccessResult, error) {
	resolution, err := clientpolicy.Resolve(cfg, flags)
	if err != nil {
		return daemonAccessResult{}, err
	}
	if resolution.Local {
		return daemonAccessResult{useLocal: true}, nil
	}

	primaryErr := request(daemonClient{origin: resolution.Origin, timeout: timeout})
	if primaryErr == nil {
		return daemonAccessResult{}, nil
	}
	if resolution.Mode == config.ClientModeRequireDaemon {
		return daemonAccessResult{}, fmt.Errorf("daemon at %s failed: %w; local fallback is forbidden by client.mode=%s", resolution.Origin, primaryErr, config.ClientModeRequireDaemon)
	}

	// An explicit daemon URL is authoritative for daemon access. Prefer-daemon
	// may still refresh locally, but must not contact an alternate daemon.
	if resolution.Source != clientpolicy.DestinationDaemonURL {
		if fallback, ok := loadUserDaemonConfig(configPath); ok {
			// The fallback file contributes only its destination. Invocation policy
			// always comes from the primary configuration.
			fallback.Client.Mode = resolution.Mode
			fallbackResolution, resolveErr := clientpolicy.Resolve(fallback, clientpolicy.FlagOptions{})
			if resolveErr == nil && !fallbackResolution.Local {
				if err := request(daemonClient{origin: fallbackResolution.Origin, timeout: timeout}); err == nil {
					return daemonAccessResult{}, nil
				}
			}
		}
	}
	return daemonAccessResult{useLocal: true, diagnostic: primaryErr}, nil
}
