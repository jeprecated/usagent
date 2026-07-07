---
title: Add provider recommendation ranking endpoint
priority: medium
---

## Goal

Expose `GET /v1/recommendations/provider` so other local services can rank providers by freshness, headroom, quota pressure, task fit, and opportunity cost.

## Acceptance Criteria

- Endpoint accepts filters such as providers, task profile, minimum remaining percent, and unit where practical.
- Response includes selected provider, ranked candidates, numeric scores, reasons, and caveats.
- Initial `cheap` profile means lowest local quota scarcity/opportunity cost, not true monetary price unless configured later.
- Recommendation scoring incorporates expiring-usage signals so scarce quota is protected while genuinely expiring quota can be prioritized for suitable work.
- Tests cover healthy providers, stale/error providers, constrained weekly quota, and a use-it-or-lose-it task profile.

## Design Decisions

- Implement as GET while request shape remains simple.
- Keep scoring explainable and deterministic.
- Avoid hidden provider pricing assumptions.

## Implementation Notes

This should depend on the analysis and expiring-usage helpers. Consider future POST /v1/decision/provider only after real clients need richer inputs like estimated tokens/duration.


## Completion Summary

- Delegated implementation to Agentleman run agm-run-20260707142159-89iwg3 and integrated after Claude judge ACCEPT verdict.
- Added cached-only `GET /v1/recommendations/provider` with providers/task profile/minimum remaining/unit filters.
- Added deterministic provider ranking with selected provider, score components, reasons, caveats, and cheap/use-it-or-lose-it profile semantics.
- Recommendation scoring uses expiring-usage signals to protect scarce quota and prioritize actionable expiring quota for use-it-or-lose-it.
- Added analysis and HTTP tests; verified in parent with `devenv shell go test ./...`.

### Files Changed

- internal/analysis/provider_recommendations.go
- internal/analysis/provider_recommendations_test.go
- internal/httpapi/provider_recommendations.go
- internal/httpapi/httpapi.go
- internal/httpapi/httpapi_test.go
- .frontloop/usage-helper-apis/done/5000-add-provider-recommendation-ranking-endpoint.md
- .frontloop/usage-helper-apis/brief-provider-recommendations.md
- .frontloop/usage-helper-apis/judge-provider-recommendations.md
