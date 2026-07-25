---
title: Enforce daemon policies through shared CLI access
priority: critical
frontloop_approval_task: 40194ceb490ef91f66f1bfa4a3a7ecf013ff32140550a120c22c017f2b9421dc-2
---

## Goal

Refactor `usage` and `expiring-usage` onto one daemon client and one three-mode state machine. Prove that a require-daemon client fails closed without touching alternate daemons, local state/history, credentials, refresh locks, or provider APIs.

## Acceptance Criteria

- Shared daemon request code resolves endpoint URLs, applies timeout, performs GET, checks 2xx, and decodes endpoint-specific responses without duplicate URL construction.
- `usage` and `expiring-usage` implement identical prefer-daemon, require-daemon, and local-only semantics.
- Prefer-daemon preserves current daemon-first behavior, user-config daemon fallback, local refresh locking, stale snapshots, and diagnostics.
- An explicit `--daemon-url` in prefer-daemon suppresses the alternate user-config daemon attempt but still permits local fallback.
- The primary config mode governs the invocation; a fallback user config contributes only its destination, including its client URL, and its own mode is ignored.
- Local-only and permitted prefer-daemon `--offline` calls bypass daemon lookup and retain current local behavior.
- Require-daemon connection, timeout, non-2xx, and response-validation failures return clear errors naming the selected origin and that local fallback is forbidden.
- Require-daemon never calls user-daemon fallback, local usage derivation, app construction, provider fetch, refresh-lock acquisition, or local state/history load.
- `--offline` with require-daemon fails before any daemon or provider request; destination conflicts are enforced consistently.
- Fake daemon/provider tests prove healthy central reads, zero provider calls and no snapshot/history creation on require-daemon failure, zero network calls for policy conflicts, no second-daemon call for explicit URL, preserved prefer-daemon fallback, and preserved local-only refresh.
- Root dispatch/help covers `--daemon-url` and the `status` and `expiring` aliases.

## Design Decisions

- Do not add client retries; polling/backoff remains the central daemon's responsibility.
- Keep `/v1/usage` schema-v2 validation and endpoint-specific expiring-usage validation.
- Prefer a narrow shared daemon client over a generalized transport framework.
- Explicit destination conflicts are errors rather than silently selecting a winner.

## Implementation Notes

Depends on task 1. Likely files: internal/cli/usage.go, internal/cli/expiring_usage.go, their tests, cmd/usagent/main.go/tests, and potentially internal/cli/daemon_client.go or internal/client. Preserve `loadUserDaemonConfig` only for non-explicit prefer-daemon destinations. Task 2 of 4.



## Completion Summary

- Unified `usage` and `expiring-usage` behind a shared daemon HTTP client and three-mode access state machine.
- Added raw `--daemon-url` handling, precedence/conflict enforcement, primary-mode preservation, alternate-daemon suppression, and fail-closed require-daemon errors.
- Preserved prefer-daemon/local-only behavior, aliases, diagnostics, refresh paths, and endpoint-specific response validation.
- Added side-effect and fake-daemon/provider coverage, including structurally invalid expiring responses; full tests, vet, race tests, repeated tests, and independent re-review passed.

### Files Changed

- internal/cli/daemon_client.go
- internal/cli/daemon_policy_test.go
- internal/cli/usage.go
- internal/cli/usage_test.go
- internal/cli/expiring_usage.go
- internal/cli/expiring_usage_test.go
- cmd/usagent/main.go
- cmd/usagent/main_test.go
