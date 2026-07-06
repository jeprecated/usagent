# usagent design

## Goal

Extract agent usage/quota tracking from the Noctalia/pi-session-monitor integration into a standalone microservice that can run via Docker or Nix. The service fetches or ingests provider usage on a schedule and exposes normalized usage as JSON for any client.

## Architecture

```text
Claude Code statusLine ──push──► POST /v1/ingest/claude-code ─┐
OpenAI Admin API ──poll───────────────────────────────────────┤
z.ai quota endpoint ──poll────────────────────────────────────┤
Anthropic Admin API ──optional poll────────────────────────────┤
                                                               ▼
                                                        usagent store
                                                               ▼
                                 GET /v1/usage, /v1/providers, /healthz
                                                               ▼
                                                     Noctalia usage widget
```

The existing Pi session tracker remains separate and does not render usage.

## Provider sources

### Claude Code / Fable

Claude subscription usage is push-based. A standalone service cannot poll Claude Code `/usage` directly. Instead, `usagent-claude-statusline` runs as the Claude Code `statusLine.command`, receives the statusline JSON on stdin, sanitizes it, and POSTs it to `usagent`.

Feasibility:

- Session / 5h window: available when statusline payload includes `five_hour`.
- Weekly / 7d window: available when statusline payload includes `seven_day`.
- Fable weekly: only available if the statusline payload includes model-scoped Fable rate-limit buckets.
- Monthly subscription window: not currently known to be available from Claude Code statusline. Do not fabricate it.

If Anthropic Admin API is added, label it as Claude API spend, not Claude Code subscription quota.

### OpenAI

Use Admin/Organization usage and cost APIs. Config defines budgets/windows for session-ish/hourly, weekly, and monthly views.

### z.ai

Use the real quota endpoint:

```text
https://api.z.ai/api/monitor/usage/quota/limit
```

Normalize token/session/weekly windows. Exclude web-search/TIME_LIMIT from the bar by default.

## JSON contract

`GET /v1/usage` returns:

```json
{
  "schemaVersion": 2,
  "service": "usagent",
  "generatedAt": 1783287000000,
  "stale": false,
  "providers": [
    {
      "id": "claude-code",
      "label": "Claude",
      "state": "fresh",
      "source": "push",
      "lastUpdatedAt": 1783286900000
    }
  ],
  "quotaItems": [
    {
      "id": "claude-code-5h",
      "provider": "claude-code",
      "label": "Claude session",
      "window": { "id": "session", "label": "S", "kind": "rolling", "resetAt": 1783289000000 },
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

`quotaItems` intentionally remains compatible with the current pi-meta `QuotaItem` shape.

## Config principles

- Config controls providers, windows, percent mode, reset display, and visibility.
- Config contains no plaintext secrets.
- Missing metrics render as unavailable/hidden depending on `hideUnavailable`.
- Web-search quota is excluded from z.ai bar output by default.

## Suggested UI default

Provider-first, remaining-percent mode:

```text
Usage: Claude S:100% W:50% F:24% R:134m · OpenAI W:50% M:70% · z.ai S:90% W:97%
```

Configurable alternatives:

```text
Usage: C S100 W50 F24 · O W50 M70 · Z S90 W97
Usage: S C100 O— Z90 · W C50 F24 O50 Z97 · M O70
Usage: Claude F:24% R:134m · OpenAI M:70% · z.ai W:97%
```

## Storage

V1 uses:

- in-memory current state
- atomic JSON snapshot on disk for restart hydration

No database in V1. Add SQLite/history later only if 24h trends are useful.

## Docker

Runtime image mounts:

- `/etc/usagent/config.yaml`
- `/run/secrets/*` or env vars
- optional state volume `/var/lib/usagent`

## Nix

Expose:

- `packages.default`
- `packages.usagent`
- `packages.oci`
- `nixosModules.usagent`
- `homeManagerModules.usagent`

Secrets should use agenix/sops-nix/runtime files, never Nix store text.

## Migration from pi-meta

1. Port quota core/adapters to `usagent`.
2. Run `usagent` side-by-side with pi-meta watcher.
3. Noctalia usage widget reads `GET /v1/usage` instead of local `overview.json` quota fields.
4. Remove quota polling from pi-session-monitor; keep session tracking there.
5. Replace Claude statusline wrapper with `usagent-claude-statusline`.

## Open questions

1. Does Claude statusline expose Fable weekly buckets? Capture a real payload.
2. Is Claude monthly expected to mean Anthropic API monthly spend, or should it be hidden?
3. Exact OpenAI budget limits and windows.
4. Exact z.ai session/weekly/monthly semantics; web-search excluded.
5. Whether Noctalia should use polling or SSE.
