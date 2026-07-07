# Delegation brief: Add quota analysis derivation package and endpoint

Repository: `/home/jmo/Development/projects/usagent`

Use Jujutsu workflow only (`jj st`, `jj diff`, etc.); do not run direct `git` commands. Preserve unrelated existing changes. Implement the active Frontloop task `.frontloop/usage-helper-apis/in_progress/2500-add-quota-analysis-derivation-package-and-endpoint.md`.

## Goal

Create a pure analysis layer over cached `model.Usage` snapshots and expose it as `GET /v1/usage/analysis`. This establishes reusable calculations for later availability, recommendations, and expiring-usage helpers.

## Acceptance criteria

- `internal/analysis` computes per-item percent remaining, reset/time remaining, inferred window duration/start where possible, elapsed/time remaining ratios, pace, pressure, projected exhaustion, confidence, and caveats.
- `GET /v1/usage/analysis` returns derived data from `api.App.Usage(time.Now())` only and never triggers provider refreshes.
- Unit tests cover known fixed windows, provider reset times, missing reset times, stale/error items, and percent/currency/count units.
- README and/or docs mention the new endpoint and its cached-only semantics.

## Existing context

`internal/analysis/expiring_usage.go` already exists and includes helpers such as percent remaining, inferred window duration, urgency, confidence constants, and optional burn-rate history input from the previous task. You may refactor carefully to avoid duplication, but do not break `/v1/expiring-usage` or CLI behavior.

Recent history support was added:

- `internal/history/history.go`
- `internal/app/app.go` has `BurnRateEstimates()` and history load/save
- `analysis.ExpiringUsageOptions` accepts `BurnRates`

## Suggested response shape

Add a new pure analysis function, e.g. `UsageAnalysis(usage model.Usage, opts UsageAnalysisOptions) UsageAnalysisResponse`, and response structs under `internal/analysis`.

For each quota item, include at least:

- provider, itemId, label, unit, window metadata
- used, remaining, limit, percentUsed, percentRemaining
- resetAt/timeRemainingMs when known
- inferredWindowDurationMs and inferredWindowStartAt when possible
- elapsedWindowMs, elapsedWindowRatio, timeRemainingRatio when possible
- burn/pace data using history burn-rate estimates when available, otherwise snapshot/window average fallback where possible
- projectedExhaustionAt or projectedExhaustionMs when rate suggests exhaustion before reset
- pressure/scarcity/opportunity-style numeric signals; define names clearly
- confidence and caveats

Keep formulae conservative and documented in code/tests. Do not fabricate exact request/token capacity from percent-only buckets.

## HTTP endpoint

- Register `GET /v1/usage/analysis` in `internal/httpapi/httpapi.go`.
- Implement handler under `internal/httpapi`.
- It must derive from `api.App.Usage(time.Now())` only; do not call refresh/provider APIs.
- If burn-rate history exists, pass it into the analysis function similarly to expiring-usage.

## Tests

Add/extend tests to cover:

- pure analysis for fixed/session/daily/weekly/month-ish windows;
- provider reset times preferred over inferred reset data;
- missing reset times and missing duration produce caveats/low confidence rather than bogus numbers;
- stale/error items lower confidence and include caveats;
- percent, currency, and count units compute percent remaining correctly;
- HTTP endpoint returns cached-only analysis and does not trigger refresh.

Run validation:

```sh
devenv shell go test ./...
```

If devenv is unavailable, run `go test ./...` and explain.

## No-go areas

- Do not change `/v1/usage` schema.
- Do not synchronously fetch provider APIs from the new endpoint.
- Do not hard-code provider/model quality or task suitability.
- Do not remove or regress expiring-usage fields/tests.

## Deliverable

Concise summary, changed files, validation commands/results, residual risks, and workspace/change identifier for judge/integration.
