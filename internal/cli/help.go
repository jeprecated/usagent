package cli

import (
	"io"
	"strings"
)

func WriteHelp(w io.Writer, command string) error {
	_, err := io.WriteString(w, FormatHelp(command, shouldColor(w)))
	return err
}

func FormatHelp(command string, color bool) string {
	text := rootHelp
	switch command {
	case "usage", "status":
		text = usageHelp
	case "expiring", "expiring-usage":
		text = expiringHelp
	case "notify":
		text = notifyHelp
	case "reset-once":
		text = resetOnceHelp
	case "mcp":
		text = mcpHelp
	case "serve":
		text = serveHelp
	}
	if !color {
		return text
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for i, line := range lines {
		switch {
		case i == 0:
			lines[i] = colorize(line, ansiBold+ansiCyan, true)
		case strings.HasSuffix(line, ":"):
			lines[i] = colorize(line, ansiBold, true)
		case strings.HasPrefix(line, "  -"):
			trimmed := strings.TrimSpace(line)
			flag, description, found := strings.Cut(trimmed, "  ")
			if found {
				lines[i] = "  " + colorize(flag, ansiCyan, true) + "  " + description
			}
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

const rootHelp = `usagent — usage and quota status for AI coding providers

Usage:
  usagent [options]
  usagent usage [options]
  usagent expiring [options]
  usagent reset-once arm|status|cancel [options]
  usagent notify [options]
  usagent mcp [options]
  usagent serve [options]

Commands:
  usage, status  Show current quota (default)
  expiring       Show quota likely to expire unused
  reset-once     Arm/cancel one ChatGPT or Claude reset at weekly exhaustion
  notify         Watch for quota replenishments with sticky desktop alerts
  mcp            Start the stdio MCP server
  serve          Start the HTTP daemon
  help           Show this help

Common options:
  --config PATH     YAML config file
  --daemon-url URL  Daemon HTTP(S) origin
  --host HOST       Daemon/listen host override
  --port PORT       Daemon/listen port override

Run 'usagent <command> --help' for all options.
`

const usageHelp = `usagent usage — show current quota

Usage:
  usagent [options]
  usagent usage [options]

Alias: usagent status

Options:
  --config PATH       YAML config file
  --daemon-url URL    Daemon HTTP(S) origin
  --host HOST         Daemon host override
  --port PORT         Daemon port override
  --timeout DURATION  Request/refresh timeout (default 10s)
  --offline           Refresh and read locally
  --json              Print raw schema v2 JSON
  -h, --help          Show this help
`

const expiringHelp = `usagent expiring — show quota likely to expire unused

Usage:
  usagent expiring [options]

Alias: usagent expiring-usage

Options:
  --within DURATION                Only resets within this duration (for example 24h)
  --within-ms N                    Same filter in milliseconds
  --minimum-remaining-percent N    Minimum remaining percentage (0–100)
  --providers a,b,c                Include provider IDs
  --tiers high,extra-high          Include provider/model tiers
  --tags chat,codex                Include provider/model tags
  --include-low-confidence         Include low-confidence opportunities
  --config PATH                    YAML config file
  --daemon-url URL                 Daemon HTTP(S) origin
  --host HOST                      Daemon host override
  --port PORT                      Daemon port override
  --timeout DURATION               Request/refresh timeout (default 10s)
  --offline                        Derive locally
  --json                           Print raw JSON
  -h, --help                       Show this help
`

const mcpHelp = `usagent mcp — start the stdio MCP server

Usage:
  usagent mcp [options]

Options:
  --config PATH       YAML config file
  --daemon-url URL    Daemon HTTP(S) origin
  --host HOST         Daemon host override
  --port PORT         Daemon port override
  --timeout DURATION  Request timeout (default 10s)
  --offline           Use local usage data
  -h, --help          Show this help
`

const resetOnceHelp = `usagent reset-once — use one reset when weekly quota reaches zero

Usage:
  usagent reset-once arm [options]
  usagent reset-once status [options]
  usagent reset-once cancel [options]

Uses the earliest-expiring eligible credit, then disarms. Repeated arm commands
never stack credits. Authorization survives daemon restarts. Only the running
daemon can redeem automatically; local usage refreshes never spend. Control it
through a direct loopback connection; remote usage readers remain supported.
The check runs on the normal provider refresh cadence. For ChatGPT, interactive
arm offers immediate use when cached weekly usage is zero; confirmation makes
the daemon recheck live usage before spending. In scripts, use arm --now to
explicitly request the same immediate check. No recurring-auto-reset config
or allowResetConsume change is needed. Provider toggles are independent.

Options:
  --provider NAME        chatgpt (default) or claude-code
  --now                  ChatGPT arm: run a fresh spend check immediately
  --config PATH          YAML config file
  --daemon-url URL       Local daemon HTTP(S) origin (no remote/fallback)
  --host HOST            Loopback daemon host
  --port PORT            Daemon port
  --timeout DURATION     Request timeout (default 10s)
  --json                 Print JSON state
  --acknowledge-unknown  Cancel only: acknowledge a manually reconciled outcome
  -h, --help             Show this help

If a redemption outcome is unknown, nothing is retried. Verify the credit and
quota on the provider before using cancel --acknowledge-unknown. This does not
refund a credit or authorize another spend. Use arm separately only when ready.
`

const notifyHelp = `usagent notify — desktop alerts when quota replenishes

Usage:
  usagent notify [options]

Reads saved reset events from the selected daemon only (no provider polling or
local/alternate-daemon fallback). Missed alerts are grouped after login.
Default alerts: weekly/monthly replenishments, plus 5-hour/session replenishments
only when the last observed remaining quota was below 20%. Configure YAML
notifications.weekly, notifications.monthly, and notifications.sessionBelowPercent
(0..100; 0 disables session alerts). Other events are skipped.
Click Mark read to acknowledge; delivery, timeout, and closing a popup do not
mark alerts read. Unread popups return on the next poll or login.
Requires a Linux desktop notification service with action support.
For Noctalia, enable Notifications > respect application expiry timeout.

Options:
  --config PATH       YAML config file
  --daemon-url URL    Daemon HTTP(S) origin
  --host HOST         Daemon host override
  --port PORT         Daemon port override
  --state PATH        Desktop acknowledgement file (default: XDG state, per daemon)
  --interval DURATION Cached event poll interval (default 1m)
  --timeout DURATION  HTTP request timeout (default 10s)
  -h, --help          Show this help
`

const serveHelp = `usagent serve — start the HTTP daemon

Usage:
  usagent serve [options]

Options:
  --config PATH  YAML config file
  --host HOST    Listen host override
  --port PORT    Listen port override
  -h, --help     Show this help
`
