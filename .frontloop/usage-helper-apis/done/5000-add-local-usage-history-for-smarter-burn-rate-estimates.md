---
title: Add local usage history for smarter burn-rate estimates
priority: medium
---

## Goal

Persist lightweight usage samples by default so expiring-usage estimates can use recent spending velocity rather than only snapshot/window-average inference.

## Acceptance Criteria

- A bounded local history of normalized quota item samples is enabled by default, stored without secrets/raw provider payloads, and loaded on startup.
- History supports recent burn-rate calculations over useful horizons such as 15m, 1h, 6h, and window-to-date.
- Expiring-usage responses prefer historical recent burn rate when available and include confidence/caveats indicating the rate source.
- Storage remains simple and robust; no database unless justified by tests/design.
- Tests cover sample retention, provider/item disappearance, counter resets, and burn-rate calculation across reset boundaries.
- Samples preserve normalized provider/item/window/reset/unit/limit/used/remaining/percent-used metadata needed for later overlapping-window modelling, while still excluding raw provider payloads and credentials.

## Design Decisions

- Do not block initial helper endpoints on history; snapshot-only expiring usage is useful enough for v1.
- History should be enabled by default because it stores only normalized quota samples.
- History must remain local and must not store raw secrets or provider payloads.
- Handle percent-only buckets as normalized percentage burn rate, not exact request counts.

## Implementation Notes

Likely touches internal/cache or a new internal/history package. Consider extending the existing atomic snapshot file or adding a sibling history file under the same state directory. Suggested default retention: 14-30 days, sampled on every successful provider refresh. Prefer a design that exposes burn-rate inputs to `internal/analysis` without making analysis read files directly, so later overlapping-window modelling can consume the same normalized history.

## Implementation Notes Added

- Implemented `internal/history` with a sibling `usage-history.json` file next to the configured snapshot path.
- History is sampled after successful provider refreshes only, stores normalized quota fields (provider/item/window/reset/unit/limit/used/remaining/percent), and is bounded by 30 days plus a per-series sample cap.
- Expiring-usage analysis accepts optional local history burn rates and prefers 15m, 1h, 6h, then window-to-date estimates before falling back to snapshot elapsed-window inference.


## Completion Summary

- Delegated implementation to Agentleman run agm-run-20260707133031-gxrshp and gated it with Claude judge ACCEPT verdict.
- Integrated payload in parent after resolving the Frontloop ready-vs-in_progress path conflict and preserving the active task card.
- Added bounded local normalized usage history persisted as usage-history.json next to the configured snapshot, sampled after successful provider refreshes, and loaded on startup.
- Expiring-usage now accepts optional burn-rate estimates and prefers local history horizons before snapshot elapsed-window inference, with confidence/caveats indicating rate source.
- Added history, app, analysis, CLI, and HTTP tests; verified with `devenv shell go test ./...`.

### Files Changed

- internal/history/history.go
- internal/history/history_test.go
- internal/app/app.go
- internal/app/app_test.go
- internal/analysis/expiring_usage.go
- internal/analysis/expiring_usage_test.go
- internal/httpapi/expiring_usage.go
- internal/cli/expiring_usage.go
- internal/cli/usage.go
- .frontloop/usage-helper-apis/done/5000-add-local-usage-history-for-smarter-burn-rate-estimates.md
- .frontloop/usage-helper-apis/brief-local-usage-history.md
- .frontloop/usage-helper-apis/judge-local-usage-history.md
