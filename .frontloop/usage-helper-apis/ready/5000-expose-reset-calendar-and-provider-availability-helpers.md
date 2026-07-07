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
