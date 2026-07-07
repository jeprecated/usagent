# Delegation brief: Add provider recommendation ranking endpoint

Repository: `/home/jmo/Development/projects/usagent`

Use Jujutsu workflow only (`jj st`, `jj diff`); do not run direct `git` commands. Preserve unrelated existing changes. Implement active Frontloop task `.frontloop/usage-helper-apis/in_progress/5000-add-provider-recommendation-ranking-endpoint.md`.

## Goal

Expose `GET /v1/recommendations/provider` so other local services can rank providers by freshness, headroom, quota pressure, task fit, and opportunity cost.

## Acceptance criteria

- Endpoint accepts filters such as providers, task profile, minimum remaining percent, and unit where practical.
- Response includes selected provider, ranked candidates, numeric scores, reasons, and caveats.
- Initial `cheap` profile means lowest local quota scarcity/opportunity cost, not true monetary price unless configured later.
- Recommendation scoring incorporates expiring-usage signals so scarce quota is protected while genuinely expiring quota can be prioritized for suitable work.
- Tests cover healthy providers, stale/error providers, constrained weekly quota, and a use-it-or-lose-it task profile.

## Existing context

- `GET /v1/usage/analysis` exists and derives cached usage facts.
- `GET /v1/expiring-usage` exists and now includes raw/actionable expiring waste, overlap context, metadata tier/tags, and history-derived burn rates.
- Provider/model metadata tier/tags exist in config/model/app and expiring-usage filters.
- Use cached `api.App.Usage(time.Now())`; do not trigger refresh/provider fetches.

## Suggested design

Add pure recommendation logic in `internal/analysis`, e.g. `ProviderRecommendations(usage, usageAnalysis, expiringUsage, opts)` or a simpler function that can call existing pure analysis helpers.

Response should be deterministic and explainable, e.g.:

```json
{
  "generatedAt": 123,
  "selectedProvider": "z-ai",
  "profile": "cheap",
  "candidates": [
    {
      "provider": "z-ai",
      "label": "z.ai",
      "score": 0.82,
      "state": "fresh",
      "tier": "medium",
      "tags": ["..."] ,
      "reasons": ["..."],
      "caveats": ["..."]
    }
  ]
}
```

Profiles:

- `cheap`: minimize local scarcity/opportunity cost, not true money. Prefer fresh providers with healthy headroom and low scarcity; protect constrained/near-exhausted quota; allow expiring actionable waste to increase score because using soon-to-expire quota can be cheap locally.
- `use-it-or-lose-it`: explicitly prioritize actionable expiring waste while still avoiding stale/error providers.
- Unknown profile should probably 400, or default to cheap with caveat; choose and test.

Filters:

- `providers=a,b`
- `profile=cheap|use-it-or-lose-it`
- `minimumRemainingPercent=N`
- `unit=percent|tokens|usd|...` if practical
- `tier=` and `tags=` if straightforward, using existing metadata semantics

Scoring should not hard-code provider quality or task suitability. Metadata filters can exclude/include, but tier should not secretly multiply score unless explicitly explained and tests cover it.

## Tests

Cover pure analysis and/or HTTP endpoint for:

- healthy providers ranked deterministically;
- stale/error providers are penalized or caveated;
- constrained weekly quota is protected under `cheap`;
- use-it-or-lose-it profile prioritizes provider with actionable expiring waste;
- filters (providers, minimum remaining percent, unit, tier/tags if implemented);
- endpoint is cached-only and does not refresh providers.

Run:

```sh
devenv shell go test ./...
```

## No-go areas

- Do not assume real monetary price.
- Do not trigger provider refreshes.
- Do not encode provider/model quality judgements.
- Do not break existing endpoints.

## Deliverable

Summary, changed files, validation results, residual risks, and workspace/change identifier for judgment/integration.
