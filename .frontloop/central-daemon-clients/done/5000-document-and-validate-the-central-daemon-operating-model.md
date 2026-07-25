---
title: Document and validate the central daemon operating model
priority: medium
frontloop_approval_task: 40194ceb490ef91f66f1bfa4a3a7ecf013ff32140550a120c22c017f2b9421dc-4
---

## Goal

Document the supported one-poller/many-reader topology, its security and OAuth limitations, and the new client policy. Finish with full Go and Nix package validation without changing or deploying host configuration.

## Acceptance Criteria

- README shows Lattice running one `serve` instance while its own CLI/MCP requires that local daemon, plus Overton-style clients requiring Lattice over a private URL.
- Documentation includes a generic Tailnet/MagicDNS example such as `http://lattice:8788` while explicitly deferring host-specific Nix changes.
- Security guidance states that readAuth remains none-only, plain HTTP must stay private, HTTPS may be externally terminated, and usagent must not be internet-exposed.
- Documentation warns that `allowResetConsume` exposes a mutating endpoint and should remain false on a broadly reachable central listener absent later security design.
- OAuth guidance states that the central daemon rereads access tokens but does not refresh OAuth grants.
- RATE_LIMITING states that require-daemon clients never poll providers and only the central daemon participates in refresh scheduling/backoff.
- DESIGN distinguishes server listen configuration from client destination/mode and records prefer-daemon, require-daemon, and local-only behavior.
- Help/docs explain legacy host/port compatibility, `--daemon-url` precedence/conflicts, its requirement for a loadable config, and prefer-daemon explicit-destination fallback behavior.
- Docs state that client mode does not disable `serve`; pure client hosts should disable all providers as defense in depth against accidentally running `serve`.
- Docs treat intended config-file presence as part of the fail-closed deployment contract and warn about the current CWD-relative example fallback when no user config exists.
- A version-skew note recommends upgrading the central daemon first; missing endpoints fail closed for require-daemon clients.
- `go test ./...` passes and `nix build .#usagent` succeeds using the repository's jj workflow when snapshotting is required; no host activation or mono-nix mutation occurs.

## Design Decisions

- Tailnet/private-network access is the security boundary for this increment.
- The product observes account-scoped usage; it does not reserve quota or prevent simultaneous model calls.
- Host-specific Nix composition, firewall rules, activation, and live deployment are a separate follow-up.
- No OAuth refresh, credential synchronization, database, replication, retries, or client cache is added.

## Implementation Notes

Depends on tasks 1–3. Likely files: README.md, docs/DESIGN.md, docs/RATE_LIMITING.md, config.example.yaml, command help/tests, and optionally a focused integration test. The final topology-aware draft was independently reviewed with Claude Fable 5 and received verdict ACCEPT. Task 4 of 4.


## Completion Summary

- Documented the supported Lattice/Overton one-poller/many-reader topology, client modes, destination precedence/conflicts, fail-closed config contract, serve independence, and private-network deployment boundary.
- Documented none-only read authentication, private HTTP/external TLS guidance, reset-consume risk, OAuth token rereading without grant refresh, provider-disable defense in depth, rate-limit behavior, and central-first version upgrades.
- Corrected mode-qualified fallback and MCP lazy config-validation guidance, and expanded root help/tests for legacy host/port compatibility.
- Validated all Go tests, vet, Markdown links, and `nix build .#usagent`; removed build artifacts, preserved mono-nix/host state, and passed independent review and re-review.

### Files Changed

- README.md
- docs/DESIGN.md
- docs/RATE_LIMITING.md
- config.example.yaml
- cmd/usagent/main.go
- cmd/usagent/main_test.go
- .gitignore
