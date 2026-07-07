# Original brief

See `.frontloop/usage-helper-apis/brief-provider-recommendations.md`. Acceptance criteria:

- Endpoint accepts filters such as providers, task profile, minimum remaining percent, and unit where practical.
- Response includes selected provider, ranked candidates, numeric scores, reasons, and caveats.
- Initial `cheap` profile means lowest local quota scarcity/opportunity cost, not true monetary price unless configured later.
- Recommendation scoring incorporates expiring-usage signals so scarce quota is protected while genuinely expiring quota can be prioritized for suitable work.
- Tests cover healthy providers, stale/error providers, constrained weekly quota, and a use-it-or-lose-it task profile.

# Payload

Repo: `/home/jmo/Development/workspaces/usagent/agm-default-4`
Change: `qkqrovktwxuuxyxkwsnovtrurtsmmmvn` / `862f0117972beaa9a12a940e4abb7a4dae4502e5` (`Add provider recommendation endpoint`)
Working copy: empty child `znmrtxkuzpooyrwyklmvrzlqnussxnrw`.

Changed files:

- `internal/analysis/provider_recommendations.go`
- `internal/analysis/provider_recommendations_test.go`
- `internal/httpapi/httpapi.go`
- `internal/httpapi/httpapi_test.go`
- `internal/httpapi/provider_recommendations.go`

# Delegate's report

Implemented cached-only `GET /v1/recommendations/provider` with provider/task/minimum/unit filters, scored ranked candidates, selected provider, reasons, caveats, and expiring-usage-aware scoring. Added analysis and HTTP tests covering healthy providers, stale/error providers, constrained weekly quota protection for cheap profile, use-it-or-lose-it prioritization, and query filtering. Created required jj payload commit.

Validation:

- `devenv shell go test ./...` passed.

Residual risks:

- Recommendation scoring is intentionally heuristic and based on cached quota signals rather than explicit task size/cost estimates.
- Provider/model metadata filters were not added; endpoint supports provider, task profile, minimum remaining percent, and unit filters.

# Judge instructions

Inspect against acceptance criteria. Verify endpoint is cached-only, scoring is deterministic/explainable, cheap does not imply real monetary price, expiring-usage signals are actually incorporated, and tests cover the required scenarios.
