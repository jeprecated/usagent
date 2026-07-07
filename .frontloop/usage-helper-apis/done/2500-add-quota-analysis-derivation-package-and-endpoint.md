---
title: Add quota analysis derivation package and endpoint
priority: high
---

## Goal

Create a pure analysis layer over cached `model.Usage` snapshots and expose it as `GET /v1/usage/analysis`. This establishes reusable calculations for later availability, recommendations, and expiring-usage helpers.

## Acceptance Criteria

- `internal/analysis` computes per-item percent remaining, reset/time remaining, inferred window duration/start where possible, elapsed/time remaining ratios, pace, pressure, projected exhaustion, confidence, and caveats.
- `GET /v1/usage/analysis` returns derived data from `api.App.Usage(time.Now())` only and never triggers provider refreshes.
- Unit tests cover known fixed windows, provider reset times, missing reset times, stale/error items, and percent/currency/count units.
- README and/or docs mention the new endpoint and its cached-only semantics.

## Design Decisions

- Keep `/v1/usage` schema stable; add separate helper response structs.
- Expose formula caveats/confidence rather than fabricating precision.
- Use pure functions so recommendation endpoints can reuse the same derivation.

## Implementation Notes

Relevant files: internal/model/model.go, internal/httpapi/httpapi.go, internal/app/app.go, internal/httpapi/httpapi_test.go. Start with conservative inference: session≈5h, weekly≈7d, monthly calendar/month-ish only when resetAt exists, daily≈24h.


## Completion Summary

- Delegated implementation to Agentleman run agm-run-20260707134913-9y1yql and integrated after Claude judge ACCEPT verdict.
- Added pure cached usage analysis derivation over `model.Usage` with per-item reset/window/pace/pressure/projection/confidence/caveat fields.
- Exposed cached-only `GET /v1/usage/analysis` and documented the endpoint.
- Added analysis and HTTP tests; verified in parent with `devenv shell go test ./...`.

### Files Changed

- README.md
- docs/DESIGN.md
- internal/analysis/usage_analysis.go
- internal/analysis/usage_analysis_test.go
- internal/httpapi/httpapi.go
- internal/httpapi/httpapi_test.go
- .frontloop/usage-helper-apis/done/2500-add-quota-analysis-derivation-package-and-endpoint.md
- .frontloop/usage-helper-apis/brief-quota-analysis.md
- .frontloop/usage-helper-apis/judge-quota-analysis.md
