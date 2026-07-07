---
title: Expose reset calendar and provider availability helpers
priority: medium
---

## Goal

Add simple cached-only helper endpoints for upcoming resets and provider availability so scripts and dashboards can consume usagent without interpreting every quota item.

## Acceptance Criteria

- `GET /v1/resets` returns visible quota items with reset times sorted by ascending resetAt, including provider, item ID, label, window, remaining, percent used, state, and reset source.
- `GET /v1/availability` returns provider-level availability, severity, blockers, constrainedBy, nextImprovementAt, and a short summary.
- Availability uses the analysis package and cached usage only.
- Tests cover empty snapshots, stale/error providers, no-reset items, and providers with multiple quota windows.

## Design Decisions

- Read-only GET endpoints only.
- Machine-friendly JSON first; do not add text formatting in HTTP responses initially.
- Availability should be conservative when usage data is stale or missing.

## Implementation Notes

This can follow the analysis endpoint task and should reuse its pressure/constrained-item calculations.


## Completion Summary

- Agentleman launch remained unavailable due the local Agentleman Pi-extension path issue, so implemented the final helper endpoints directly in the parent workspace.
- Added pure analysis helpers for reset calendars and conservative provider availability summaries over cached `model.Usage` snapshots.
- Exposed cached-only `GET /v1/resets` and `GET /v1/availability` and documented/listed them in README.
- Added tests covering reset sorting/no-reset omission, empty snapshots, stale/error providers, multiple quota windows, and HTTP cached-only behavior.
- Verified with `devenv shell go test ./...`.

### Files Changed

- README.md
- internal/analysis/reset_availability.go
- internal/analysis/reset_availability_test.go
- internal/httpapi/reset_availability.go
- internal/httpapi/httpapi.go
- internal/httpapi/httpapi_test.go
- .frontloop/usage-helper-apis/done/5000-expose-reset-calendar-and-provider-availability-helpers.md
