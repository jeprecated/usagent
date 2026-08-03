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
  usagent mcp [options]
  usagent serve [options]

Commands:
  usage, status  Show current quota (default)
  expiring       Show quota likely to expire unused
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

const serveHelp = `usagent serve — start the HTTP daemon

Usage:
  usagent serve [options]

Options:
  --config PATH  YAML config file
  --host HOST    Listen host override
  --port PORT    Listen port override
  -h, --help     Show this help
`
