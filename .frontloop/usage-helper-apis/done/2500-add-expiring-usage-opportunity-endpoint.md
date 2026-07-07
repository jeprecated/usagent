---
title: Add expiring usage opportunity endpoint
priority: high
---

## Goal

Expose `GET /v1/expiring-usage` to help local services discover quota that is likely to go unused before reset, the user's “use it or lose it” concept.

## Acceptance Criteria

- Endpoint returns opportunities with provider, item ID, remaining amount/percent, provider/model tier metadata where configured, resetAt, timeRemainingMs, estimatedNaturalUseBeforeReset, estimatedWastedAmount/Percent, opportunityScore, urgency, confidence, reasons, and caveats.
- Query params support at least `within` or `withinMs`, `minimumRemainingPercent`, `providers`, and `includeLowConfidence`.
- The first implementation uses snapshot-only inference and clearly marks low/medium confidence when historical burn rate is unavailable.
- Reset-credit bank items are excluded by default unless explicitly requested later.
- Tests cover high-opportunity expiring quota, low remaining quota, no reset time, stale items, and rolling/session windows.

## Design Decisions

- Frame results as opportunity signals, not commands to spend quota.
- Do not make task-suitability judgements; callers decide whether a provider/model tier fits their work.
- Provider/model tiers and tags are metadata/filter inputs, not implicit scoring policy.
- Prefer provider-reported reset times over inferred resets.
- Do not claim exact request/token capacity for percent-only providers.

## Implementation Notes

Critique from planning: a single snapshot cannot know true current spending velocity. Use inferred average pace initially, then add local history sampling in a later task for smarter natural-use estimates. User review clarified that the endpoint's purpose is to find likely-wasted credits; downstream services decide whether a medium/high/extra-high model is appropriate for their task.


## Completion Summary

- Delegated implementation to Agentleman run agm-run-20260707092029-bsc6g4 and integrated after Claude judge ACCEPT verdict.
- Added cached-only GET /v1/expiring-usage with snapshot-derived waste/opportunity scoring and query filters.
- Added analysis package tests, HTTP integration coverage, and README endpoint documentation.
- Verified in parent repo with `devenv shell go test ./...`.

### Files Changed

- README.md
- internal/analysis/expiring_usage.go
- internal/analysis/expiring_usage_test.go
- internal/httpapi/expiring_usage.go
- internal/httpapi/httpapi.go
- internal/httpapi/httpapi_test.go
- .frontloop/usage-helper-apis/in_progress/2500-add-expiring-usage-opportunity-endpoint.md -> .frontloop/usage-helper-apis/done/2500-add-expiring-usage-opportunity-endpoint.md
- .frontloop/usage-helper-apis/brief-expiring-usage.md
- .frontloop/usage-helper-apis/judge-expiring-usage.md
