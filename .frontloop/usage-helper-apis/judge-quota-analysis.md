# Original brief

See `.frontloop/usage-helper-apis/brief-quota-analysis.md` in the parent repo. Key acceptance criteria:

- `internal/analysis` computes per-item percent remaining, reset/time remaining, inferred window duration/start where possible, elapsed/time remaining ratios, pace, pressure, projected exhaustion, confidence, and caveats.
- `GET /v1/usage/analysis` returns derived data from `api.App.Usage(time.Now())` only and never triggers provider refreshes.
- Unit tests cover known fixed windows, provider reset times, missing reset times, stale/error items, and percent/currency/count units.
- README and/or docs mention the new endpoint and its cached-only semantics.

# Payload

Repo: `/home/jmo/Development/workspaces/usagent/agm-default-2`
Change: `txzsrqyxzmsvwokwknqswkzzmynzqpvx` / `42cc72d4f206d6a6dfbbbe5ba7379e50f2043299` (`add cached usage analysis endpoint`)
Base: `kvswspsrlrvlxzrzrrnzxqmztvtknnxt` / `d77b701658a45aef48c2dc99f6118be992eba096`
Working copy: empty child `qlmnwtuozktsrtxpyupsrnoqwkkvvuqo`.

Changed files:

- `README.md`
- `docs/DESIGN.md`
- `internal/analysis/usage_analysis.go`
- `internal/analysis/usage_analysis_test.go`
- `internal/httpapi/httpapi.go`
- `internal/httpapi/httpapi_test.go`

# Delegate's report

Implemented pure cached usage analysis in `internal/analysis` and exposed `GET /v1/usage/analysis`. Added endpoint/docs/tests covering fixed/reset/missing/stale-error/unit scenarios and committed the payload.

Validation:

- `devenv shell go test ./...` passed.
- `jj st` clean after commit.

Residual risk: Pace, pressure, and exhaustion projections are intentionally conservative because they use only the current cached snapshot, not historical usage samples.

# Judge instructions

Inspect the payload against acceptance criteria. Verify the endpoint is cached-only and does not refresh provider APIs. Verify tests are meaningful and response field naming/formulae are coherent and conservative.
