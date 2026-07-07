# Delegation brief: CLI access for expiring usage helper

Repository: `/home/jmo/Development/projects/usagent`

## Goal

Implement the active frontloop task: make `GET /v1/expiring-usage` accessible from the `usagent` CLI with daemon-first behavior and offline/local fallback matching the existing `usagent usage` command.

## Context

Existing CLI command code lives mostly in:

- `cmd/usagent/main.go`
- `internal/cli/usage.go`
- `internal/cli/usage_test.go`

The expiring-usage HTTP endpoint and analysis were recently added in:

- `internal/httpapi/expiring_usage.go`
- `internal/analysis/expiring_usage.go`

Current `usagent usage` behavior:

- attempts daemon first (`/v1/usage`)
- falls back to user daemon config if useful
- then falls back to local usage refresh/read when daemon is unavailable
- supports `--json`, `--offline`, `--timeout`, config/host/port flags

## Acceptance criteria

- Add `usagent expiring-usage` and a sensible alias, e.g. `usagent expiring`.
- Command exposes the same data as `GET /v1/expiring-usage`.
- CLI flags include:
  - `--config PATH`
  - `--host HOST`
  - `--port PORT`
  - `--timeout 10s`
  - `--offline`
  - `--json`
  - `--within DURATION`, e.g. `24h`
  - `--within-ms N`
  - `--minimum-remaining-percent N`
  - `--providers a,b,c`
  - `--include-low-confidence`
- Daemon mode calls `/v1/expiring-usage` with query params.
- If daemon is unavailable, fall back to local usage derivation using `cli.LocalUsage()` and `analysis.ExpiringUsage()`.
- Default non-JSON output is readable and includes provider/item, remaining, estimated waste, time to reset, urgency, and confidence.
- `--json` emits the raw expiring-usage response JSON, indented like usage JSON.
- Add tests for daemon fetch, local fallback, JSON output, and root command dispatch/help.
- Update README command docs to mention the command.

## Design constraints

- Use `jj`, not git.
- Do not change `/v1/usage` semantics.
- Do not call provider APIs from daemon-backed CLI request path.
- Offline/local mode may use the existing `LocalUsage()` behavior, which refreshes due providers locally, just like `usagent usage --offline`.
- Prefer adding `internal/cli/expiring_usage.go` rather than bloating `usage.go`, but reuse helper patterns where practical.

## Validation commands

Run:

```sh
devenv shell go test ./...
```

If Go files change, run `gofmt` on modified Go files.

## Deliverable

A committed payload in the delegated workspace with tests passing, a summary of behavior/files changed, validation commands, and any residual risks.
