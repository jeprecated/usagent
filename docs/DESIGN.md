# usagent design

## Goal

`usagent` is a standalone Go microservice that fetches provider usage on a schedule and exposes normalized schema-version-2 usage JSON for Noctalia or any other client.

## Architecture

```text
Claude Code OAuth usage API ──refresh loop───────────────┐
OpenAI placeholder provider ──refresh loop───────────────┤
z.ai placeholder provider ───refresh loop────────────────┤
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
- `internal/providers/noop` keeps OpenAI and z.ai metadata stable until their real fetchers are added.
- `internal/httpapi` exposes the stable HTTP contract.

## Provider sources

### Claude Code / Fable

Claude subscription usage is polled directly from the Claude Code OAuth usage endpoint:

```text
GET https://api.anthropic.com/api/oauth/usage
Authorization: Bearer <runtime credentials claudeAiOauth.accessToken>
anthropic-beta: oauth-2025-04-20
```

The response `limits[]` entries are normalized as follows:

- `kind: "session"` → current 5h/session bucket.
- `kind: "weekly_all"` → current week, all models.
- `kind: "weekly_scoped"` with model display/id containing `Fable` → current week, Fable.

Missing buckets are not fabricated. The OAuth access token is read from the configured credentials file for each refresh and is never exposed in responses.

If Claude returns an error or rate limit after a previous success, the last cached quota items remain visible with stale/error metadata. `Retry-After` controls the next retry time when present.

### OpenAI and z.ai

OpenAI and z.ai are currently placeholder providers that return no quota items while preserving provider metadata in `/v1/usage` and `/v1/providers`.

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
