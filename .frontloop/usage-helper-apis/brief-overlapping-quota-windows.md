# Delegation brief: Model overlapping quota windows for actionable waste

Repository: `/home/jmo/Development/projects/usagent`

Use Jujutsu workflow only (`jj st`, `jj diff`, etc.); do not run direct `git` commands. Preserve unrelated existing changes. Implement active Frontloop task `.frontloop/usage-helper-apis/in_progress/2500-model-overlapping-quota-windows-for-actionable-waste.md`.

## Goal

After local usage history exists, teach `expiring-usage` to account for coupled quota windows such as 5h/session, weekly, and monthly buckets on the same provider. Distinguish quota that merely resets soon from quota that is actually worth prioritizing before parent windows reset.

## Acceptance criteria

- Quota items can be grouped by provider/model family and overlapping window relationship where the cached usage data provides enough signal.
- Expiring-usage derives both raw expiring unused amount and actionable waste adjusted by parent-window demand/capacity context.
- Short-window opportunities are down-weighted when longer parent windows are fresh and expected future short-window capacity can satisfy forecast demand.
- Short-window opportunities are up-weighted near parent-window exhaustion or parent reset when current short-window capacity is likely to matter.
- The response includes caveats when overlap relationships are inferred heuristically or unavailable.
- Tests cover a fresh weekly/monthly parent reducing apparent 5h waste, an end-of-week parent increasing actionable 5h waste, constrained parent quota limiting safe short-window spend, and independent windows remaining independently scored.

## Existing context

- `internal/analysis/expiring_usage.go` currently estimates natural use from local history burn rates or snapshot fallback.
- `analysis.ExpiringUsageOptions.BurnRates` comes from `internal/history`.
- `internal/analysis/usage_analysis.go` may provide useful window inference helpers/terms.
- Later task `5000-expose-raw-versus-actionable-expiring-usage-estimates` will polish CLI/API wording, but this task must add the underlying raw/actionable data now.

## Suggested model

Keep this conservative and explain caveats. One acceptable approach:

1. Compute the current raw natural/waste estimate exactly as today; expose it as raw expiring unused amount/percent.
2. Group quota items by provider and an inferred family key. Start with provider-level grouping if no model identifier exists, but avoid grouping obviously unrelated item families when labels/IDs differ enough.
3. Infer window duration classes from item window/id/label: session/5h < daily < weekly < monthly. Parent windows are same provider/family with longer inferred duration and later reset.
4. For a short-window item with a parent:
   - Forecast demand until parent reset from local history when available, else the same conservative rate used by expiring-usage.
   - Estimate future short-window capacity after the current short reset and before the parent reset.
   - Down-weight current short-window actionable waste when the parent is fresh and future short-window capacity can satisfy forecast demand.
   - Up-weight/preserve current short-window actionable waste when parent remaining is constrained or parent reset is close/exhaustion pressure is high.
5. Clamp actionable waste to `[0, raw waste]` and to parent-safe spend where parent remaining is lower than short remaining.
6. Keep independent/no-parent items scored from raw waste and caveat that no overlap relationship was found.

Field names can be additive, e.g.:

- `estimatedRawExpiringAmount`
- `estimatedRawExpiringPercent`
- `estimatedActionableWasteAmount`
- `estimatedActionableWastePercent`
- `overlapGroup`, `overlapParentItemIds`, or similar optional metadata

Preserve existing `estimatedWastedAmount`/`estimatedWastedPercent` for backward compatibility, preferably mapped to actionable waste after this task if tests/docs explain it, or leave mapped to raw and add new actionable fields clearly. Do not break existing JSON consumers unnecessarily.

## Tests

Add unit tests in `internal/analysis` covering:

- fresh weekly/monthly parent reduces apparent/actionable 5h waste below raw;
- end-of-week/end-of-month parent increases/preserves actionable 5h waste;
- constrained parent quota limits safe short-window spend/actionable waste;
- independent windows remain independently scored with caveats;
- existing expiring-usage tests still pass.

Run:

```sh
devenv shell go test ./...
```

## No-go areas

- Do not hard-code provider/model quality or task suitability.
- Do not trigger provider refreshes.
- Do not remove existing expiring-usage fields without compatibility docs/tests.
- Do not implement provider recommendation endpoint here.

## Deliverable

Summary, changed files, validation results, residual risks, and workspace/change identifier for judgment/integration.
