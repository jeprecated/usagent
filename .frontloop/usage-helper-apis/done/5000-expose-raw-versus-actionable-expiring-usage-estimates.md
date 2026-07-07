---
title: Expose raw versus actionable expiring-usage estimates
priority: medium
---

## Goal

Rename and extend expiring-usage outputs and CLI wording so users can tell the difference between simple reset-bound unused quota and overlap/history-adjusted actionable waste. This makes long-window results less misleading at the start of weekly or monthly periods.

## Acceptance Criteria

- API responses expose separate fields for raw expiring unused amount/percent and actionable waste amount/percent, preserving backward compatibility where practical or documenting any migration.
- Opportunity score uses actionable waste when available, while still surfacing raw expiring amount for transparency.
- CLI text avoids overclaiming; low-context estimates are labelled as estimated unused or raw expiring amount rather than confident waste.
- Reasons/caveats explain whether the estimate came from history, window-average fallback, or overlapping-window modelling.
- README documents the distinction with at least one example involving 5h and weekly/monthly quotas.
- Tests cover JSON output fields, CLI formatting, and fallback behavior before overlap context is available.

## Design Decisions

- Prefer additive response fields over breaking renames unless a migration note is added.
- Keep confidence and caveats prominent when only snapshot inference is available.

## Implementation Notes

Best done after the history and overlapping-window tasks, because naming should reflect the final maths. Consider retaining `estimatedWastedAmount` as legacy raw or actionable value only with clear documentation.


## Completion Summary

- Agentleman launch failed due current Agentleman Pi-extension configuration, so completed this polish task directly in the parent workspace.
- Updated CLI wording to distinguish actionable waste from raw expiring unused quota and avoid generic “estimated waste” overclaiming for low-context estimates.
- Documented raw versus actionable expiring-usage fields and legacy `estimatedWasted*` compatibility semantics in README with a 5h plus weekly/monthly example.
- Clarified overlap down-weighting reason text and added tests for actionable-score use, CLI formatting, and fallback estimated-unused wording.
- Verified with `devenv shell go test ./...`.

### Files Changed

- README.md
- internal/analysis/expiring_usage.go
- internal/analysis/expiring_usage_test.go
- internal/cli/expiring_usage.go
- internal/cli/expiring_usage_test.go
- .frontloop/usage-helper-apis/done/5000-expose-raw-versus-actionable-expiring-usage-estimates.md
- .frontloop/usage-helper-apis/brief-raw-vs-actionable-expiring.md
