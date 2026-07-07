# Original brief

# Delegation brief: Add local usage history for smarter burn-rate estimates

Repository: `/home/jmo/Development/projects/usagent`

Use the repository's Jujutsu workflow. Do not run direct `git` commands. Use `jj st`, `jj diff`, etc. Do not commit unless explicitly required by Agentleman workflow; leave changes in the delegated workspace for integration.

## Goal

Implement the active Frontloop task `.frontloop/usage-helper-apis/in_progress/5000-add-local-usage-history-for-smarter-burn-rate-estimates.md`:

Persist lightweight normalized quota history by default so `GET /v1/expiring-usage` and `usagent expiring-usage` can prefer recent historical burn rates over snapshot-only elapsed-window inference.

## Product context

The current implementation in `internal/analysis/expiring_usage.go` infers burn rate from a single snapshot:

```text
windowDuration = inferred 5h / 1d / 7d / 30d
elapsed = duration - timeRemaining
burnRate = used / elapsed
estimatedNaturalUseBeforeReset = burnRate * timeRemaining
estimatedWastedAmount = max(0, remaining - estimatedNaturalUseBeforeReset)
```

This overstates waste for weekly/monthly quota near the start of a window. Later tasks will model overlapping 5h/weekly/monthly windows and expose raw versus actionable waste. This task should provide the history primitives those tasks need: normalized samples with enough window/reset metadata to calculate reliable recent burn rates and window-to-date rates, without storing provider secrets or raw provider payloads.

## Acceptance criteria

- A bounded local history of normalized quota item samples is enabled by default, stored without secrets/raw provider payloads, and loaded on startup.
- History supports recent burn-rate calculations over useful horizons such as 15m, 1h, 6h, and window-to-date.
- Expiring-usage responses prefer historical recent burn rate when available and include confidence/caveats indicating the rate source.
- Storage remains simple and robust; no database unless justified by tests/design.
- Tests cover sample retention, provider/item disappearance, counter resets, and burn-rate calculation across reset boundaries.
- History samples include normalized provider/item/window/reset/unit/limit/used/remaining/percent-used information sufficient for later overlapping-window modelling, but no raw provider payloads or credentials.
- The active Frontloop task file is updated if implementation details or decisions need to be recorded.

## Suggested implementation direction

- Likely add a new small package such as `internal/history`, or extend `internal/cache` if that is cleaner.
- Consider a sibling history file next to `server.statePath`, e.g. if snapshot is `.../snapshot.json`, history can default to `.../usage-history.json`. Avoid changing public config unless needed.
- Sample on every successful provider refresh, after provider results have been normalized to `model.QuotaItem`.
- Retention should be bounded. Suggested default: 30 days or a reasonable max samples per item/provider. Make the bound deterministic/testable.
- Treat percent-only buckets as normalized percentage burn rate, not exact request/token capacity.
- For burn-rate calculations, prefer deltas in `Used` or inverse deltas in `Remaining`, handle monotonic increases, ignore negative deltas caused by resets, and avoid crossing reset boundaries/window identity changes.
- For window-to-date, use samples since the current reset window began if derivable; otherwise use the earliest compatible sample in the current reset period.
- Integrate with `analysis.ExpiringUsage` in a minimally invasive way, e.g. add optional history/rate inputs to `analysis.ExpiringUsageOptions` rather than making analysis read files directly.
- `httpapi.expiringUsage` should pass app/store history-derived rate data when available. CLI local fallback should also work with local history if loaded by the app/store path; daemon JSON just displays server result.
- Keep response shape backward compatible. It is OK to add caveat strings such as `natural use estimated from 1h local history burn rate` and set confidence high/medium accordingly.

## Important existing files

- `internal/analysis/expiring_usage.go`
- `internal/analysis/expiring_usage_test.go`
- `internal/app/app.go`
- `internal/cache/snapshot.go`
- `internal/cache/snapshot_test.go`
- `internal/httpapi/expiring_usage.go`
- `internal/cli/expiring_usage.go`
- `internal/model/model.go`
- `README.md` if user-facing behavior/wording changes

## No-go areas / constraints

- Do not store raw provider payloads, access tokens, credentials, or request headers in history.
- Do not hard-code provider/model quality or task suitability into scoring.
- Do not implement overlapping quota window modelling in this task beyond preserving the data needed later.
- Do not break existing `/v1/usage`, `/v1/expiring-usage`, or CLI JSON fields.
- Be careful: the parent working copy already has unrelated modifications. In the delegated workspace, preserve unrelated existing changes and avoid broad rewrites.

## Validation commands

Run:

```sh
devenv shell go test ./...
```

If `devenv` is unavailable in the delegated environment, run:

```sh
go test ./...
```

Report exactly which command(s) you ran and their results.

## Deliverable

A concise implementation summary, changed files, validation results, and residual risks. Leave the payload in the Agentleman workspace for parent integration and judgment.

# Acceptance criteria

- A bounded local history of normalized quota item samples is enabled by default, stored without secrets/raw provider payloads, and loaded on startup.
- History supports recent burn-rate calculations over useful horizons such as 15m, 1h, 6h, and window-to-date.
- Expiring-usage responses prefer historical recent burn rate when available and include confidence/caveats indicating the rate source.
- Storage remains simple and robust; no database unless justified by tests/design.
- Tests cover sample retention, provider/item disappearance, counter resets, and burn-rate calculation across reset boundaries.
- History samples include normalized provider/item/window/reset/unit/limit/used/remaining/percent-used information sufficient for later overlapping-window modelling, but no raw provider payloads or credentials.
- The active Frontloop task file is updated if implementation details or decisions need to be recorded.

# Payload

Repo: `/home/jmo/Development/workspaces/usagent/agm-default-1`
Change: `kvswspsrlrvlxzrzrrnzxqmztvtknnxt` / `f19ca9615b24` (`Add local usage history burn-rate estimates`)
Base: `zylqluyrpmxvuuuqxymnzupnxwokumkx` / `7d6f74133c7d`
Working copy: empty child `wnkptvwykrkl` with same tree as payload parent.

Changed files reported by delegate:

- `.frontloop/usage-helper-apis/ready/5000-add-local-usage-history-for-smarter-burn-rate-estimates.md`
- `internal/analysis/expiring_usage.go`
- `internal/analysis/expiring_usage_test.go`
- `internal/app/app.go`
- `internal/app/app_test.go`
- `internal/cli/expiring_usage.go`
- `internal/cli/usage.go`
- `internal/history/history.go`
- `internal/history/history_test.go`
- `internal/httpapi/expiring_usage.go`

# Delegate's report

Implemented bounded local normalized quota history and wired historical burn-rate estimates into expiring-usage analysis, HTTP, and CLI local fallback. History is stored by default as `usage-history.json` next to the configured snapshot, sampled after successful provider refreshes, loaded on startup, bounded by 30 days/per-series cap, and used for 15m/1h/6h/window-to-date burn rates before snapshot inference. Created required payload commit `Add local usage history burn-rate estimates`.

Validation reported:

- `go test ./...` passed.
- `devenv shell go test ./...` passed.

Residual risks reported:

- The brief file named in the prompt was absent from the isolated workspace; delegate read it from the parent tree and updated the corresponding workspace task card that existed under `.frontloop/usage-helper-apis/ready`.
- History load failures are logged and ignored so corrupt local history does not block startup; snapshot loading remains strict.
- Window-to-date rate derivation uses compatible samples for the current reset identity/reset time but does not infer an exact window start when providers do not expose one.

# Judge instructions

Inspect the payload against the acceptance criteria. Pay special attention to:

- whether local history is actually loaded on startup and saved after successful refresh;
- whether storage contains only normalized sample data, not raw payloads/secrets;
- whether burn-rate calculations handle counter resets and reset boundaries;
- whether `expiring-usage` truly prefers historical rates and marks confidence/caveats correctly;
- whether CLI local fallback and HTTP daemon path both pass history-derived rates where available;
- whether tests are meaningful rather than only superficial.
