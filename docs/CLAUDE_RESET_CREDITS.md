# Claude Code banked reset credits

`usagent` can show and optionally redeem Claude Code banked rate-limit resets.

Anthropic announced these alongside Opus 5.5 as a banked reset. Claude Code 2.1.278 implements them as the `cedar_ember` program: `/limit-reset` in the CLI, and a `cedar_ember` block on the OAuth usage API.

## Sources and stability

This integration is based on observed Claude Code CLI behavior, not a published stable Anthropic API:

- Status lives on `GET /api/oauth/usage?cedar_ember=1` as `cedar_ember`.
- Redemption is `POST /api/organizations/{organizationUuid}/reset_rate_limits` with `program=cedar_ember`.
- Claude Code's `/limit-reset` command is the official user-facing surface.

Because these endpoints are undocumented, their shape or availability may change without notice. `usagent` keeps redemption explicit and opt-in.

## Credentials

Reset APIs use the same Claude Code OAuth credentials as normal quota tracking:

```text
~/.claude/.credentials.json
```

`usagent` reads `claudeAiOauth.accessToken`. Organization identity for consume comes from Claude Code's account file:

```text
~/.claude.json
```

`usagent` reads `oauthAccount.organizationUuid`. If that file is missing the UUID, it falls back to `GET /api/oauth/profile`.

## Provider configuration

Read-only reset counting is available whenever `providers.claudeOAuth.enabled=true` and the usage payload includes `cedar_ember`.

```yaml
providers:
  claudeOAuth:
    enabled: true
    credentialsPath: "~/.claude/.credentials.json"
    accountPath: "~/.claude.json"
    endpointUrl: "https://api.anthropic.com/api/oauth/usage"
    profileEndpointUrl: "https://api.anthropic.com/api/oauth/profile"
    resetConsumeEndpointUrl: "https://api.anthropic.com/api/organizations/{organizationUuid}/reset_rate_limits"
    allowResetConsume: false
```

`allowResetConsume` defaults to `false`. Set it to `true` only if trusted local callers should be able to spend a real banked reset via the `usagent` HTTP API.

## Normal usage stats

`GET /v1/usage` includes a quota item derived from `cedar_ember.grants[].resets_left`:

```json
{
  "id": "claude-code-rate-limit-reset-credits",
  "provider": "claude-code",
  "label": "Claude resets",
  "window": { "id": "resetCredits", "label": "Resets", "kind": "credit" },
  "unit": "credits",
  "remaining": 1
}
```

The item is omitted when `cedar_ember` is absent. Missing blocks are not fabricated.

## Listing banked credits

```sh
curl -fsS http://127.0.0.1:8788/v1/claude-code/reset-credits | jq
```

Example response:

```json
{
  "provider": "claude-code",
  "availableCount": 1,
  "eligible": true,
  "nextGrantId": "grant_abc",
  "credits": [
    {
      "id": "grant_abc",
      "status": "available",
      "title": "Full reset",
      "grantedAt": "2026-09-22T00:00:00Z",
      "expiresAt": "2026-10-22T00:00:00Z",
      "resetsLeft": 1,
      "clears": ["five_hour", "seven_day"]
    }
  ],
  "fetchedAt": 1783410000000
}
```

This calls:

```text
GET https://api.anthropic.com/api/oauth/usage?cedar_ember=1
Authorization: Bearer <access token>
anthropic-beta: oauth-2025-04-20
```

A grant is `available` when it still has `resets_left`, is `usable_now`, is not paused, and has not expired.

## Redeeming a reset credit

Redeeming is mutating and spends a real banked reset. It is disabled unless:

```yaml
providers:
  claudeOAuth:
    allowResetConsume: true
```

```sh
curl -fsS -X POST http://127.0.0.1:8788/v1/claude-code/reset-credits/consume \
  -H 'content-type: application/json' \
  -H 'x-usagent-action: consume-claude-code-reset-credit' \
  -d '{
    "creditId": "grant_abc",
    "confirm": "consume-claude-code-reset-credit"
  }'
```

This calls:

```text
POST https://api.anthropic.com/api/organizations/{organizationUuid}/reset_rate_limits
Authorization: Bearer <access token>
Content-Type: application/json

{"program":"cedar_ember","grant_id":"...","request_id":"..."}
```

Confirmed success results are `reset` and `already_used` (idempotent). Other 2xx results such as `not_limited`, `cooldown`, and `ineligible` are treated as a confirmed non-spend and are not retried automatically. An unreadable 2xx body leaves reset-once `unknown`.

After a successful consume, `usagent` refreshes the Claude provider so `/v1/usage` reflects the reset windows/count as soon as possible.

## One-shot automatic redemption

`reset-once --provider claude-code` is a toggle, not a standing config permission. It authorizes the running daemon to spend **at most one** banked reset when the account-wide weekly window is freshly reported as exhausted (`weekly_all` utilization >= 100, reset still in the future). It does not fire on the 5h/session window or Fable-scoped weekly windows.

```sh
usagent reset-once arm --provider claude-code
usagent reset-once status --provider claude-code
usagent reset-once cancel --provider claude-code
```

The ChatGPT toggle is independent and remains the default when `--provider` is omitted. Authorization is stored next to the snapshot as `snapshot.json.claude-reset-once.json`.

Selection rule: the available, unexpired grant with the earliest `expiresAt`. Grants with missing or invalid expiry are skipped. The selected grant ID and a request ID are persisted **before** the consume POST.

`allowResetConsume` is **not** required for `reset-once`. Manual consume still requires that config flag.

Local control API (loopback peer, no `Origin`, no forwarded-client headers, explicit action header + JSON confirm):

- `GET /v1/claude-code/reset-once`
- `POST /v1/claude-code/reset-once/arm` with `x-usagent-action: arm-claude-code-reset-once`
- `POST /v1/claude-code/reset-once/cancel` with `x-usagent-action: cancel-claude-code-reset-once`
