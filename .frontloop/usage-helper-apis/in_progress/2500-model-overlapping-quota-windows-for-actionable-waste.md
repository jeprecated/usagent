---
title: Model overlapping quota windows for actionable waste
priority: high
---

## Goal

After local usage history exists, teach expiring-usage to account for coupled quota windows such as 5h/session, weekly, and monthly buckets on the same provider. This should distinguish quota that merely resets soon from quota that is actually worth prioritizing before parent windows reset.

## Acceptance Criteria

- Quota items can be grouped by provider/model family and overlapping window relationship where the cached usage data provides enough signal.
- Expiring-usage derives both raw expiring unused amount and actionable waste adjusted by parent-window demand/capacity context.
- Short-window opportunities are down-weighted when longer parent windows are fresh and expected future short-window capacity can satisfy forecast demand.
- Short-window opportunities are up-weighted near parent-window exhaustion or parent reset when current short-window capacity is likely to matter.
- The response includes caveats when overlap relationships are inferred heuristically or unavailable.
- Tests cover a fresh weekly/monthly parent reducing apparent 5h waste, an end-of-week parent increasing actionable 5h waste, constrained parent quota limiting safe short-window spend, and independent windows remaining independently scored.

## Design Decisions

- Keep raw expiring amount separate from actionable waste so callers can choose their own policy.
- Do not hard-code model quality or task suitability into waste scoring.
- Use local history forecasts from the history task as the preferred demand signal.

## Implementation Notes

Depends on `5000-add-local-usage-history-for-smarter-burn-rate-estimates`. Likely touches `internal/analysis/expiring_usage.go` and may need a small quota-window grouping abstraction in the analysis layer. The maths should model current short-window capacity against expected demand before the parent reset and future short-window capacity after the current short reset.
