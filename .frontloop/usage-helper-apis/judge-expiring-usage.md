# Original brief

# Delegation brief: expiring usage helper API

Repository: `/home/jmo/Development/projects/usagent`

## Goal

Implement the active frontloop task: expose `GET /v1/expiring-usage` to help local services discover quota that is likely to go unused before reset ("use it or lose it"). This endpoint must derive only from cached `api.App.Usage(time.Now())`; it must not synchronously call provider APIs.

## Context

Existing endpoints live in `internal/httpapi/httpapi.go`. Core models are in `internal/model/model.go`. Current usage snapshots expose `model.Usage` with `QuotaItems` containing provider, label, window, unit, limit, used, remaining, percentUsed, state, severity, optional refresh metadata, and optional reset/window reset time.

Planning files:

- `.frontloop/usage-helper-apis/plan.md`
- `.frontloop/usage-helper-apis/in_progress/2500-add-expiring-usage-opportunity-endpoint.md`

Important product decision: usagent should surface facts and opportunity signals, not decide whether a provider/model is suitable for a task. Provider/model tier metadata can be omitted or handled minimally in this task if full metadata config is deferred, but the response shape should be ready to include it when configured later.

## Acceptance criteria

- Add `GET /v1/expiring-usage`.
- Response opportunities include: provider, item ID, label, unit, remaining amount, percentRemaining, resetAt, timeRemainingMs, estimatedNaturalUseBeforeReset, estimatedWastedAmount, estimatedWastedPercent, opportunityScore, urgency, confidence, reasons, caveats.
- Include provider/model tier metadata fields if already available/configured; otherwise keep fields omitted and do not hard-code provider quality judgements.
- Query params support at least:
  - `within` duration string, e.g. `24h`, and/or `withinMs`
  - `minimumRemainingPercent`
  - `providers` comma-separated filter
  - `includeLowConfidence` boolean
- First implementation may use snapshot-only inference. It must clearly mark low/medium confidence when historical burn rate is unavailable.
- Exclude reset-credit bank items by default, especially `chatgpt-rate-limit-reset-credits`, unless explicitly included later.
- Prefer provider-reported reset times over inferred resets.
- Do not claim exact request/token capacity for percent-only providers.
- Add tests covering high-opportunity expiring quota, low remaining quota, no reset time, stale items, and rolling/session windows.
- Update README or docs to mention the new endpoint and cached-only semantics.

## Design guidance

Suggested implementation:

- Add an `internal/analysis` package with pure functions where practical, so future endpoints can reuse the logic.
- Define response structs in `internal/model` or `internal/analysis` and expose JSON with stable names.
- Ranking/scoring should be about likely waste, not provider usefulness.
- A simple snapshot-only estimate is acceptable:
  - Determine resetAt from `item.Reset.ResetAt` or `item.Window.ResetAt`.
  - Infer duration/start for known windows when resetAt exists:
    - session/rolling ≈ 5h
    - weekly ≈ 7d
    - daily ≈ 24h
    - monthly ≈ 30d or calendar approximation with caveat
  - Estimate elapsed ratio and average burn rate from current used over inferred elapsed time.
  - Estimate natural use before reset = burnRate * timeRemaining.
  - Estimated waste = max(0, remaining - estimatedNaturalUseBeforeReset).
  - Low confidence if no duration/start can be inferred; medium if using inferred duration; high only when future history or explicit window bounds exist.
- Urgency can be `low|normal|high|extreme`, based on time remaining.
- Opportunity score should combine meaningful remaining, urgency, underuse, and confidence. Do not include provider quality/usefulness in score.

## No-go areas

- Do not call external provider APIs from the HTTP request path.
- Do not use raw provider payloads or secrets.
- Do not introduce a database for this task.
- Do not change `/v1/usage` schema semantics.
- Follow repository workflow: use `jj` commands, not `git`.

## Validation commands

Run:

```sh
go test ./...
```

If formatting changes are needed, run `gofmt` on modified Go files.

## Deliverable

A working change in the delegated workspace with tests passing and a concise summary of files changed, behavior, validation commands, and residual risks.

# Payload

Repo: `/home/jmo/Development/workspaces/usagent/agm-default-1`
Change: `wnmupxlvlkrnvkpuovxlkvwxzuszvtqs` / commit `959fde3c2b3f8d573fcdede1edca6bed22598edf`
Base: `ommlvyookxnqtrmyxnttqmswozrprnup` / commit `c2508ef60b13a1270328ead16573e2ea07105a70`

# Delegate's report

Implemented cached-only GET /v1/expiring-usage with snapshot-derived opportunity analysis, query filters, tests, README documentation, and required jj payload commit.

Changed files:
- README.md
- internal/analysis/expiring_usage.go
- internal/analysis/expiring_usage_test.go
- internal/httpapi/expiring_usage.go
- internal/httpapi/httpapi.go
- internal/httpapi/httpapi_test.go

Validation:
- `go test ./...` passed
- payload commit created with `jj commit -m 'Add cached expiring usage endpoint'`

Residual risks:
- Opportunity estimates are snapshot-only and infer burn rate from window duration; confidence is capped at medium and stale/unknown-duration items are low confidence.
- Provider/model tier metadata is represented by omitempty response fields but no metadata is populated because no configured metadata source exists yet.
