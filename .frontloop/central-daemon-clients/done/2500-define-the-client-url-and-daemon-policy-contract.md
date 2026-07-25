---
title: Define the client URL and daemon policy contract
priority: high
frontloop_approval_task: 40194ceb490ef91f66f1bfa4a3a7ecf013ff32140550a120c22c017f2b9421dc-1
---

## Goal

Add a typed top-level client configuration that separates daemon consumption from server listening and centralizes destination resolution. Preserve existing configurations by defaulting to prefer-daemon while making require-daemon a hard, testable client policy.

## Acceptance Criteria

- Config supports `client.url` and exactly three modes: `prefer-daemon`, `require-daemon`, and `local-only`; missing mode normalizes to `prefer-daemon`.
- An empty client URL derives the daemon origin from effective `server.host`/`server.port`, including wildcard IPv4/IPv6 to loopback compatibility.
- Explicit client URLs accept absolute HTTP/HTTPS origins and reject unsupported schemes, missing hosts, userinfo, query, fragment, and path prefixes; normalization is idempotent.
- A single destination resolver receives normalized config and raw flag presence/values separately, rather than inferring explicit overrides from post-override config.
- Destination precedence is `--daemon-url`, explicit legacy `--host`/`--port`, `client.url`, then server-derived origin.
- The conflict matrix rejects `--daemon-url` with `--host`/`--port`, `--offline`, or local-only; explicit host-only/port-only selects legacy derivation wholesale and never modifies `client.url`.
- Legacy host/port flags remain accepted but unused with `--offline` or local-only; under require-daemon they select a legacy HTTP destination and remain fail-closed.
- Server-derived and legacy host/port destinations remain HTTP; HTTPS requires `client.url` or `--daemon-url`.
- Tests cover all modes, malformed URLs, trailing-slash normalization, bracketed IPv6, wildcard derivation, partial legacy overrides, conflicts, precedence, and double normalization.
- Client mode governs client commands only; root tests prove `serve` remains independent and can start from a config whose own CLI/MCP policy is require-daemon.
- `config.example.yaml` demonstrates the client section without changing the default local-daemon experience.

## Design Decisions

- Use the explicit names `prefer-daemon`, `require-daemon`, and `local-only`; do not use the ambiguous term hybrid.
- `client.mode` is configuration policy; do not add a CLI mode override.
- Require-daemon forbids every local refresh path, not merely automatic fallback.
- `serve` independently uses `server` and `providers`, allowing Lattice to serve and point its own clients at that daemon using one config.
- No bearer authentication, built-in TLS listener, or auth fields are added in this increment.

## Implementation Notes

Likely files: internal/config/config.go, internal/config/config_test.go, cmd/usagent/main.go, cmd/usagent/main_test.go, config.example.yaml, plus a focused client-policy/resolution file if useful. `LoadWithOverrides` currently normalizes twice and mutates server host/port, so resolver inputs must retain raw flag explicitness. Shared normalization intentionally means an invalid client URL also invalidates `serve` startup. Task 1 of 4.



## Completion Summary

- Added typed `client.url` and exact client modes with backward-compatible normalization defaults and strict HTTP/HTTPS origin validation.
- Added centralized raw-flag-aware destination policy resolution with required precedence, conflicts, legacy partial overrides, wildcard/IPv6 handling, and fail-closed require-daemon semantics.
- Kept `serve` independent from client policy and documented the new configuration contract.
- Added comprehensive config, policy, and root serve tests; full Go tests, vet, race-focused tests, repeated focused tests, and independent review all passed.

### Files Changed

- internal/config/config.go
- internal/config/client.go
- internal/config/config_test.go
- internal/clientpolicy/policy.go
- internal/clientpolicy/policy_test.go
- cmd/usagent/main.go
- cmd/usagent/main_test.go
- config.example.yaml
