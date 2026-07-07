# Usage Helper APIs Plan

## Context

`usagent` currently exposes normalized cached usage via:

- `GET /healthz`
- `GET /readyz`
- `GET /v1/usage`
- `GET /v1/providers`
- `GET /v1/chatgpt/reset-credits`
- `POST /v1/chatgpt/reset-credits/consume`

The existing `QuotaItem` model already has the core inputs helpers need: provider, unit, limit, used, remaining, percentUsed, state, severity, refresh metadata, and optional reset/window reset time.

A Fable expansion pass was attempted with `claude -p --model fable`, but the local Claude CLI reported the monthly spend limit was reached. The plan below is therefore recorded without Fable's additional brainstorm until credits/model access are available again.

## Proposed endpoints

### 1. `GET /v1/usage/analysis`

Derived analysis over the current cached usage snapshot.

Per visible quota item, compute where possible:

- `percentRemaining`
- `timeRemainingMs`
- `windowDurationMs`
- `windowStartedAt`
- `windowElapsedRatio`
- `timeRemainingRatio`
- `pace`: `underusing`, `on-track`, `overusing`, `unknown`
- `pressure`: `low`, `normal`, `high`, `critical`, `unknown`
- `projectedExhaustionAt`
- `projectedRemainingAtReset`
- `confidence`: `high`, `medium`, `low`
- `caveats[]`

This should be implemented first as a pure internal analysis package plus tests.

### 2. `GET /v1/availability`

Provider-level machine-readable summary for scripts and orchestrators.

Shape:

```json
{
  "providers": [
    {
      "id": "claude-code",
      "available": true,
      "state": "fresh",
      "severity": "warning",
      "constrainedBy": "claude-code-oauth-fable-weekly",
      "nextImprovementAt": 1783290000000,
      "summary": "Fable weekly quota is constrained; session quota is healthy",
      "blockers": []
    }
  ]
}
```

### 3. `GET /v1/providers/{providerID}/headroom`

Provider-centric rollup for clients that do not want to interpret every quota item.

Return:

- provider state
- all visible quota windows
- most constraining item
- next reset/improvement time
- safe-for-new-task boolean
- reasons and caveats

### 4. `GET /v1/resets`

Cross-provider reset calendar sorted by `resetAt` ascending.

Include provider, item ID, label, window, reset source, percent used, remaining, and state. This helps dashboards and wait-until-reset automation.

### 5. `GET /v1/recommendations/provider`

Rank providers for simple task profiles.

Example query params:

- `task=general|cheap|long-running|latency-insensitive|use-it-or-lose-it`
- `providers=claude-code,chatgpt,z-ai`
- `minimumRemainingPercent=20`
- `unit=percent|usd|tokens|credits`

Initial scoring should mean "least scarce / best local choice", not true external price. Each candidate must include reasons and caveats.

### 6. Future: `POST /v1/decision/provider`

Use when query params become too limiting.

Request body may include estimated tokens/cost, duration, preferred/excluded providers, minimum headroom, cost sensitivity, latency sensitivity, and risk tolerance. Response returns the selected provider, ranked alternatives, and explanations.

## Expiring usage / "use it or lose it"

The user wants other services to discover usage that is likely to go to waste so they can intentionally spend it before reset.

Refined decision from review: usagent should **surface facts and opportunity signals, not decide whether a provider/model is appropriate for the caller's task**. Other services can decide whether a medium, high, or extra-high tier model is suitable. usagent's job is to answer:

> Which quota buckets have meaningful remaining capacity, little time left before reset, and low likelihood of being naturally consumed at the current burn rate?

It should also expose provider/model metadata tags such as quality/cost tier so clients can filter or make their own tradeoffs.

### Proposed endpoint

`GET /v1/expiring-usage`

Possible query params:

- `withinMs=86400000` or `within=24h`
- `minimumRemainingPercent=10`
- `minimumRemainingAmount=...`
- `providers=...`
- `units=percent,tokens,usd,credits`
- `includeLowConfidence=false`

Response shape:

```json
{
  "generatedAt": 1783287000000,
  "opportunities": [
    {
      "provider": "chatgpt",
      "itemId": "chatgpt-primary",
      "label": "ChatGPT 5h",
      "unit": "percent",
      "remaining": 50,
      "percentRemaining": 50,
      "providerTier": "high",
      "providerTags": ["chat", "subscription", "high"],
      "resetAt": 1783290600000,
      "timeRemainingMs": 3600000,
      "estimatedNaturalUseBeforeReset": 5,
      "estimatedWastedAmount": 45,
      "estimatedWastedPercent": 45,
      "opportunityScore": 0.94,
      "urgency": "extreme",
      "confidence": "medium",
      "reasons": [
        "50% remains with about 1h until reset",
        "current inferred burn rate is unlikely to consume the remainder"
      ],
      "caveats": [
        "burn rate inferred from window elapsed ratio, not historical samples"
      ]
    }
  ]
}
```

### Critique and caveats

The idea is valuable, but "based on my current spending" requires care:

1. **A single snapshot is not enough to know actual current burn rate.**
   - With only `used`, `remaining`, and `resetAt`, usagent can infer average pace across the whole window only if it knows/infers the window duration/start.
   - That is useful, but it is not the same as recent spending velocity.

2. **History makes this much smarter and should be enabled by default.**
   - To estimate natural use before reset, usagent should keep a short local history of normalized quota samples.
   - Then it can calculate recent burn rates over 15m/1h/6h/window-to-date.
   - User decision: local history should be on by default because it stores only normalized quota samples, not secrets or raw provider payloads.
   - Suggested default retention: 14-30 days, sampled on every successful refresh.

3. **Some windows are hard to model.**
   - Rolling 5h windows may not behave like fixed daily/weekly/monthly resets.
   - Provider-reported reset times should be trusted more than inferred reset times.

4. **Percent buckets hide actual capacity.**
   - Claude/ChatGPT Pro subscription quota often exposes percent usage, not exact message/token capacity.
   - The service can still rank opportunity, but should not pretend it knows the absolute number of usable requests.

5. **OpenAI API spend budgets are not the target use case.**
   - The user is using ChatGPT Pro subscription quota, not OpenAI API spend budgets.
   - API budget items, if configured by someone else, should be excluded from expiring-usage by default because unused budget is usually money not spent, not prepaid quota going to waste.

6. **Reset credits are not normal expiring usage.**
   - ChatGPT reset credits are banked credits and should be excluded by default unless they have expiry metadata and the endpoint is explicitly about expiring credits.

7. **Avoid making task-suitability judgements.**
   - The API should expose opportunities and estimated waste, not say "spend this now" or "this model is good enough". Clients can decide whether useful work exists for that provider/model tier.

8. **Provider/model tier is metadata, not scoring policy.**
   - Examples: z.ai GLM 5.2 might be tagged `medium`; OpenAI ChatGPT 5.5 and Claude Opus 4.8 might be `high`; Fable via API/usage credits might be `extra-high`.
   - usagent should return these tags so callers can filter, but should not hard-code what tasks deserve which tier.

### Suggested scoring

For each quota item with a reset time:

```text
opportunityScore =
  remainingScore
  * urgencyScore
  * underuseScore
  * confidenceMultiplier
```

Where:

- `remainingScore` rises with meaningful remaining quota.
- `urgencyScore` rises as reset approaches.
- `underuseScore` rises when projected natural use before reset is much lower than current remaining.
- `confidenceMultiplier` penalizes inferred windows and stale data.

Do **not** include a provider quality/usefulness multiplier in the default waste score. Provider/model tier should be response metadata and query/filter input, not an implicit judgement about whether a caller should spend that quota.

Initial burn-rate fallback:

1. Prefer historical recent burn rate when available.
2. Else infer average burn rate from `percentUsed / elapsedWindowMs` when window start/duration can be inferred.
3. Else report low-confidence opportunity based only on `remaining` and `timeRemainingMs`.

## Staged implementation

1. Add `internal/analysis` with pure derivation functions and unit tests.
2. Implement `GET /v1/usage/analysis`.
3. Implement `GET /v1/resets` and `GET /v1/availability`.
4. Implement `GET /v1/expiring-usage` using snapshot-only inferred burn rate.
5. Add default-enabled lightweight local history sampling to improve expiring-usage estimates.
6. Add configurable provider/model metadata tags and tiers for clients to consume.
7. Implement `GET /v1/recommendations/provider` using analysis + expiring usage signals, while keeping task suitability decisions explainable and client-overridable.
8. Consider `POST /v1/decision/provider` once real clients need richer decision payloads.
