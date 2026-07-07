---
title: Add CLI access for expiring usage helper
priority: high
---

## Goal

Make the implemented expiring-usage helper available from the usagent CLI with daemon-first and offline/local modes, matching the HTTP endpoint's filters.

## Acceptance Criteria

- `usagent expiring-usage` and a sensible alias expose the same cached-only opportunity data as `GET /v1/expiring-usage`.
- CLI supports config/host/port/timeout/offline/json flags plus endpoint filters: within, withinMs, minimumRemainingPercent, providers, includeLowConfidence.
- Daemon mode calls `/v1/expiring-usage` and falls back to local cached/refresh usage derivation when the daemon is unavailable, consistent with the existing usage command.
- Default non-JSON output is readable and includes provider/item, remaining, estimated waste, time to reset, urgency, and confidence.
- Tests cover daemon fetch, local fallback, JSON output, and root command dispatch/help.

## Design Decisions

- Helper endpoints should have CLI access as they are implemented.
- Keep HTTP and CLI behavior aligned by reusing `internal/analysis` for local fallback.
- Do not call provider APIs from daemon-backed CLI requests; offline/local mode may use the existing local refresh behavior like `usagent usage`.

## Implementation Notes

Relevant files: cmd/usagent/main.go, internal/cli/usage.go, internal/cli/usage_test.go, internal/analysis/expiring_usage.go. Consider a new internal/cli/expiring_usage.go to avoid bloating usage.go.


## Completion Summary

- Delegated CLI implementation to Agentleman run agm-run-20260707100820-aw73ht and integrated after Claude judge ACCEPT verdict.
- Added `usagent expiring-usage` plus `usagent expiring` alias with daemon-first `/v1/expiring-usage` access and local fallback.
- Added CLI flags for JSON/offline/timeout/config/host/port plus expiring-usage filters, readable text output, tests, and README docs.
- Verified in parent repo with `devenv shell go test ./...`.

### Files Changed

- README.md
- cmd/usagent/main.go
- cmd/usagent/main_test.go
- internal/cli/expiring_usage.go
- internal/cli/expiring_usage_test.go
- .frontloop/usage-helper-apis/in_progress/2500-add-cli-access-for-expiring-usage-helper.md -> .frontloop/usage-helper-apis/done/2500-add-cli-access-for-expiring-usage-helper.md
- .frontloop/usage-helper-apis/brief-cli-expiring-usage.md
- .frontloop/usage-helper-apis/judge-cli-expiring-usage.md
