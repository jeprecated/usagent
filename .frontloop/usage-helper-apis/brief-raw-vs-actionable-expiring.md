# Delegation brief: Expose raw versus actionable expiring-usage estimates

Repository: `/home/jmo/Development/projects/usagent`

Use Jujutsu workflow only (`jj st`, `jj diff`); do not run direct `git` commands. Preserve unrelated existing changes. Implement active Frontloop task `.frontloop/usage-helper-apis/in_progress/5000-expose-raw-versus-actionable-expiring-usage-estimates.md`.

## Goal

Rename and extend expiring-usage outputs and CLI wording so users can tell the difference between simple reset-bound unused quota and overlap/history-adjusted actionable waste. This makes long-window results less misleading at the start of weekly or monthly periods.

## Acceptance criteria

- API responses expose separate fields for raw expiring unused amount/percent and actionable waste amount/percent, preserving backward compatibility where practical or documenting any migration.
- Opportunity score uses actionable waste when available, while still surfacing raw expiring amount for transparency.
- CLI text avoids overclaiming; low-context estimates are labelled as estimated unused or raw expiring amount rather than confident waste.
- Reasons/caveats explain whether the estimate came from history, window-average fallback, or overlapping-window modelling.
- README documents the distinction with at least one example involving 5h and weekly/monthly quotas.
- Tests cover JSON output fields, CLI formatting, and fallback behavior before overlap context is available.

## Existing context

Overlap work already added fields like raw/actionable waste and overlap context. This task should polish semantics, wording, tests, and docs. Provider recommendation and metadata may now exist as sibling/parent changes; preserve them.

## Specific guidance

- Keep JSON additive/backward-compatible. If `estimatedWastedAmount` remains legacy raw or maps to actionable, document it clearly in README and tests.
- Ensure `OpportunityScore` is based on actionable waste, not raw, when actionable exists.
- CLI should say things like:
  - `actionable waste ~X; raw expiring unused ~Y` when both differ.
  - `estimated unused ~X` or `raw expiring unused ~X` for low-context/snapshot-only cases.
  - Avoid implying that weekly/monthly quota at the start of the period is definitely waste.
- Reason/caveat strings should mention local history, window-average fallback, and overlap modelling clearly.
- README should include a short example involving a 5h window nested under a fresh weekly/monthly parent and explain why raw reset-bound unused can be much larger than actionable waste.

## Tests

Cover:

- JSON output includes raw and actionable fields with expected values;
- opportunity score uses actionable waste when raw and actionable differ;
- CLI formatting distinguishes actionable vs raw and avoids “estimated waste” overclaim where appropriate;
- fallback/no-overlap context still emits clear low/medium-confidence wording and raw/actionable equality.

Run:

```sh
devenv shell go test ./...
```

## No-go areas

- Do not remove existing fields without explicit migration docs.
- Do not change scoring to include provider/model quality.
- Do not trigger provider refreshes.

## Deliverable

Summary, changed files, validation results, residual risks, and workspace/change identifier for judgment/integration.
