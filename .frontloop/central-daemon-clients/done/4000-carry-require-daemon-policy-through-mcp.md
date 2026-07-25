---
title: Carry require-daemon policy through MCP
priority: high
frontloop_approval_task: 40194ceb490ef91f66f1bfa4a3a7ecf013ff32140550a120c22c017f2b9421dc-3
---

## Goal

Make stdio MCP use the same client policy as direct CLI commands so agent tool calls cannot request or trigger local polling under require-daemon configuration.

## Acceptance Criteria

- `usagent mcp` accepts `--daemon-url` and uses the same resolver, precedence, conflicts, and mode semantics as direct CLI commands.
- MCP rejects startup `--offline` when config can be loaded and proves require-daemon, before serving any request.
- Unrelated config-load failure does not newly make bare prefer-daemon MCP startup fatal; per-call loading remains authoritative if config changes during the process.
- With require-daemon config, per-tool `offline: true` returns an MCP tool error without provider requests or local state access.
- An MCP server launched with explicit `--daemon-url` rejects per-tool `offline: true` as the defined destination/offline conflict.
- Normal `usage` and `tokens_to_burn` calls in require-daemon mode use only the configured daemon.
- Prefer-daemon and local-only MCP behavior remains backward compatible where policy permits intentional offline calls.
- The stable `offline` tool property remains, but descriptions explain require-daemon and explicit-daemon conflict behavior; the existing usage-tool local-fallback wording is corrected.
- Tests cover startup conflict, per-call conflict, unreachable and healthy central daemons, zero-upstream guarantees for both tools, and the `expiring_usage` alias.

## Design Decisions

- MCP delegates policy decisions to shared CLI/client code and does not implement a second state machine.
- Retain a stable tool schema instead of conditionally hiding `offline` based on config that may change.
- Do not expose a per-tool or startup client-mode override.

## Implementation Notes

Depends on task 2. Likely files: internal/mcp/mcp.go, internal/mcp/mcp_test.go, shared client-policy code, and cmd/usagent/main.go help. Current MCP forwards startup and per-tool offline settings through baseCLIArgs; tests must prove these paths cannot bypass require-daemon. Task 3 of 4.



## Completion Summary

- Forwarded MCP daemon destination flags with raw presence while delegating all policy decisions to the shared client resolver and CLI state machine.
- Added startup and per-tool fail-closed enforcement for require-daemon and explicit-daemon offline conflicts without making unrelated startup config-load failures fatal.
- Preserved dynamic per-call config loading, prefer-daemon/local-only offline compatibility, stable tool schemas, aliases, and root help.
- Added healthy/unreachable central-daemon, zero-side-effect, explicit destination, reload, offline, and alias tests; full tests, race tests, vet, repeated tests, and independent review passed.

### Files Changed

- internal/mcp/mcp.go
- internal/mcp/mcp_test.go
- cmd/usagent/main.go
- cmd/usagent/main_test.go
