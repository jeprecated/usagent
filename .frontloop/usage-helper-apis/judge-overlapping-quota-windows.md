# Original brief

See `.frontloop/usage-helper-apis/brief-overlapping-quota-windows.md`. Acceptance criteria:

- Quota items can be grouped by provider/model family and overlapping window relationship where cached usage data provides enough signal.
- Expiring-usage derives both raw expiring unused amount and actionable waste adjusted by parent-window demand/capacity context.
- Short-window opportunities are down-weighted when longer parent windows are fresh and expected future short-window capacity can satisfy forecast demand.
- Short-window opportunities are up-weighted near parent-window exhaustion or parent reset when current short-window capacity is likely to matter.
- Response includes caveats when overlap relationships are inferred heuristically or unavailable.
- Tests cover fresh weekly/monthly parent reducing apparent 5h waste, end-of-week parent increasing actionable 5h waste, constrained parent quota limiting safe short-window spend, and independent windows remaining independently scored.

# Payload

Repo: `/home/jmo/Development/workspaces/usagent/agm-default-2`
Change: `vzwpppuznlnzkquumsmqqrrlqymqsoqs` / `51678ebc62db77a9e9bfb11adac3c58148b9a9b4` (`Model overlapping quota windows in expiring usage`)
Base includes prior history and usage-analysis work.
Working copy: empty child `qrrqlovyktwunntlponvluruynkzkqtv`.

Changed files:

- `internal/analysis/expiring_usage.go`
- `internal/analysis/expiring_usage_test.go`
- `internal/cli/expiring_usage.go`

# Delegate's report

Implemented overlap-aware expiring-usage scoring. The response now exposes raw expiring waste, parent-adjusted actionable waste, and overlap context/caveats; scoring is down-weighted when fresh parent windows plus future short windows make current short-window capacity less important, up-weighted/urgency-adjusted near parent reset or exhaustion, and capped by constrained parent quota. Added coverage for fresh parent reduction, end-of-week parent urgency, constrained parent capacity, and independent windows. Payload commit: `51678ebc62db Model overlapping quota windows in expiring usage`.

Validation:

- `go test ./internal/analysis` passed.
- `devenv shell go test ./...` passed.

Residual risks:

- Overlap grouping is intentionally heuristic when provider metadata only exposes item IDs/labels/window durations; responses include caveats for inferred relationships.
- Percent-only providers still cannot provide exact request/token capacity; parent-window adjustments caveat that limitation.

# Judge instructions

Inspect the payload against acceptance criteria. Verify raw and actionable fields are coherent and backward-compatible, scoring uses actionable waste appropriately, and tests demonstrate the required scenarios rather than only checking nonzero output.
