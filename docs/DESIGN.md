# usagent design

## Goal

`usagent` is a standalone Go microservice that fetches provider usage on a schedule and exposes normalized schema-version-2 usage JSON for Noctalia or any other client.

## Architecture

```text
Claude Code OAuth usage API ──refresh loop───────────────┐
ChatGPT WHAM usage API ─────refresh loop──────────────────┤
OpenAI Admin Costs API ────refresh loop──────────────────┤
z.ai quota API ────────────refresh loop──────────────────┤
Custom HTTP JSON APIs ─────refresh loop──────────────────┤
                                                          ▼
                                         in-memory snapshot cache
                                                          │
                                         atomic JSON state snapshot
                                                          ▼
                            GET /v1/usage, /v1/providers, /healthz
                                                          ▼
                                                Noctalia usage widget
```

Package layout:

- `cmd/usagent` wires config, state load, refresh loop, and HTTP serving.
- `internal/config` parses YAML, flags, env overrides, validation, `~`, and `%STATE%` expansion.
- `internal/model` owns the schema v2 response structs.
- `internal/cache` owns current provider snapshots, stale/error metadata, and atomic disk persistence.
- `internal/providers` defines the provider interface.
- `internal/providers/claude` implements Claude Code OAuth fetching and normalization.
- `internal/providers/chatgpt` implements ChatGPT Pro/Codex WHAM usage fetching and normalization.
- `internal/providers/openai` implements optional OpenAI Platform Admin Costs API fetching and budget normalization.
- `internal/providers/zai` implements z.ai quota fetching and normalization.
- `internal/providers/custom` implements deterministic HTTP JSON mapping for user-defined providers.
- `internal/providers/noop` keeps disabled provider metadata stable when requested by `usageView.providers`.
- `internal/httpapi` exposes the stable HTTP contract.

## Provider sources

### Claude Code / Fable

Claude subscription usage is polled directly from the Claude Code OAuth usage endpoint:

```text
GET https://api.anthropic.com/api/oauth/usage
Authorization: Bearer <runtime credentials claudeAiOauth.accessToken>
anthropic-beta: oauth-2025-04-20
anthropic-version: 2023-06-01
User-Agent: claude-code/2.0
```

The response is normalized as follows:

- `limits[]` entry with `kind: "session"` → current 5h/session bucket.
- `limits[]` entry with `kind: "weekly_all"` → current week, all models.
- `limits[]` entry with `kind: "weekly_scoped"` and model display/id containing `Fable` → current week, Fable.
- `extra_usage` with `is_enabled: true` and `monthly_limit` → Extra Credits monthly balance. The endpoint reports `monthly_limit` and `used_credits` in cents, so usagent converts them to the reported currency unit and computes `remaining = (monthly_limit - used_credits) / 100`.

Missing buckets are not fabricated. Disabled or uncapped `extra_usage` blocks are skipped because they do not expose a finite remaining balance. The OAuth access token is read from the configured credentials file for each refresh and is never exposed in responses.

If Claude returns an error or rate limit after a previous success, the last cached quota items remain visible with stale/error metadata. `Retry-After` controls the next retry time when present.

### ChatGPT Pro / Codex

ChatGPT subscription usage is polled from the private ChatGPT/Codex WHAM endpoint when `providers.chatgpt.enabled=true`:

```text
GET https://chatgpt.com/backend-api/wham/usage
Authorization: Bearer <Codex/ChatGPT OAuth access token>
ChatGPT-Account-Id: <account id when available>
```

By default usagent reads `tokens.access_token` and `tokens.account_id` from `~/.codex/auth.json`, written by the Codex CLI ChatGPT login. `CHATGPT_ACCESS_TOKEN` and `CHATGPT_ACCOUNT_ID` can override the file. The provider normalizes `rate_limit.primary_window` as the session/5h row, `rate_limit.secondary_window` as the weekly row, any `additional_rate_limits[]` windows as model-specific rows, and `rate_limit_reset_credits.available_count` as a reset-credit count. Tokens are never exposed in responses.

Reset banking uses the same credentials. `GET /v1/chatgpt/reset-credits` fetches `GET /backend-api/wham/rate-limit-reset-credits` and returns banked credit IDs/status/grant/expiry metadata. `POST /v1/chatgpt/reset-credits/consume` calls `POST /backend-api/wham/rate-limit-reset-credits/consume` with a `credit_id` and generated/requested `redeem_request_id`. The consume endpoint is disabled by default, requires `providers.chatgpt.allowResetConsume=true`, and requires both `x-usagent-action: consume-chatgpt-reset-credit` and body `confirm: "consume-chatgpt-reset-credit"`. See [`CHATGPT_RESET_CREDITS.md`](CHATGPT_RESET_CREDITS.md) for source references, request/response examples, and service-to-service guidance.

### OpenAI API costs

OpenAI Platform API spend is polled from the Admin Costs API when `providers.openai.enabled=true`. This is not ChatGPT Plus/Pro subscription quota:

```text
GET https://api.openai.com/v1/organization/costs?start_time=<unix>&end_time=<unix>&bucket_width=1d
Authorization: Bearer <runtime OPENAI_ADMIN_KEY>
```

Each configured budget produces one quota item. The provider requests the widest configured range once, follows `next_page` pagination with the `page` cursor and explicit `limit`, sums `amount.value` buckets matching the budget currency/window, and reports `used`, `remaining`, and `percentUsed` against the configured budget limit. Non-2xx responses honor `Retry-After` and are handled by stale-if-error cache behavior.

### z.ai

z.ai is polled when `providers.zAi.enabled=true`:

```text
GET https://api.z.ai/api/monitor/usage/quota/limit
Authorization: Bearer <runtime ZAI_API_KEY or GLM_API_KEY>
```

`data.limits[]` entries are normalized into quota items for token, session, rate, and count limits. `nextResetTime` is propagated to `window.resetAt`/`reset`. `percentage` is used directly when present; otherwise percent is computed from used and remaining values. `TIME_LIMIT`/web-search limits are excluded by default and may be changed in config. Unknown limit types are ignored only if excluded; otherwise they are exposed with stable IDs and custom windows.

### Custom HTTP JSON providers

`providers.custom[]` lets users map arbitrary JSON endpoints into quota items. Each endpoint can add static headers and runtime env-var auth (`none`, `bearer`, or raw `header`). `itemsPath` selects an array; omitting it maps the whole response as one item. Mapping string values are JSON dot paths by default, while `literal:<value>` forces a constant. Reset times support `unixMs`, `unixSeconds`, and `rfc3339`. Secrets are never read from generated config values; token values come from env vars.

## Cache and refresh behavior

HTTP callers never synchronously call provider APIs. The refresh loop checks provider `nextRefreshAt`, starts at most one in-flight refresh per provider, applies `refreshMs`/`staleMs`, and saves successful snapshots atomically to `server.statePath`. On startup the snapshot is loaded before serving.

## JSON contract

`GET /v1/usage` returns schema version 2:

```json
{
  "schemaVersion": 2,
  "service": "usagent",
  "generatedAt": 1783287000000,
  "startedAt": 1783286900000,
  "stale": false,
  "providers": [
    {
      "id": "claude-code",
      "label": "Claude",
      "state": "fresh",
      "source": "pull",
      "lastUpdatedAt": 1783286900000
    }
  ],
  "quotaItems": [
    {
      "id": "claude-code-oauth-session",
      "provider": "claude-code",
      "label": "Claude 5h",
      "window": { "id": "session", "label": "S", "kind": "rolling" },
      "unit": "percent",
      "limit": 100,
      "used": 10,
      "remaining": 90,
      "percentUsed": 10,
      "state": "fresh",
      "severity": "ok",
      "visible": true
    }
  ]
}
```

`GET /v1/providers` returns `{ "providers": [...] }` using the same provider objects.

## Storage

V1 uses:

- in-memory current state
- atomic JSON snapshot on disk for restart hydration

No database in V1. Add SQLite/history later only if 24h trends are useful.

## Docker and Nix

The Dockerfile builds the Go binary in a multi-stage image and runs it without Node.

The flake exposes `packages.default`, `packages.usagent`, `packages.oci`, `apps.default`, `nixosModules.usagent`, and `homeManagerModules.usagent`.

Nix-generated config lives in the store and must not contain plaintext secrets. Use runtime credential paths, systemd credentials, agenix, sops-nix, or an environment file outside the store.
