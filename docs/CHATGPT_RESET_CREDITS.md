# ChatGPT/Codex reset credits

`usagent` can show and optionally redeem ChatGPT/Codex banked rate-limit reset credits.

These credits are also called flexible rate-limit resets, reset banking, or banked resets in community tools. They reset the Codex 5h/session and weekly windows when redeemed through ChatGPT's private backend API.

## Sources and stability

This integration is based on observed/private ChatGPT endpoints, not a published stable OpenAI API:

- OpenAI Community discussion: <https://community.openai.com/t/flexible-rate-limit-resets-for-codex-and-a-method-to-get-a-reset/1383470/13>
- `aaamosh/codex-reset`: <https://github.com/aaamosh/codex-reset>
- `codex-reset` documents the relevant backend endpoints and notes that they were found in the official `openai.chatgpt` VS Code extension webview bundle.

Because these endpoints are undocumented, their shape or availability may change without notice. `usagent` keeps redemption explicit and opt-in.

## Credentials

The reset-credit APIs use the same ChatGPT/Codex OAuth credentials as normal ChatGPT quota tracking:

```text
~/.codex/auth.json
```

`usagent` reads:

- `tokens.access_token`
- `tokens.account_id`

Environment overrides are also supported:

```sh
CHATGPT_ACCESS_TOKEN=...
CHATGPT_ACCOUNT_ID=...
```

Tokens are runtime-only secrets and must not be put in Nix store-rendered config, images, or checked-in files.

## Provider configuration

Read-only reset-credit counting is available whenever `providers.chatgpt.enabled=true`.

Detailed listing uses:

```yaml
providers:
  chatgpt:
    enabled: true
    authPath: "~/.codex/auth.json"
    endpointUrl: "https://chatgpt.com/backend-api/wham/usage"
    resetCreditsEndpointUrl: "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
    resetConsumeEndpointUrl: "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume"
    allowResetConsume: false
```

`allowResetConsume` defaults to `false`. Set it to `true` only if trusted local callers should be able to spend a real banked reset credit via the `usagent` HTTP API.

## Normal usage stats

`GET /v1/usage` includes a quota item derived from `/backend-api/wham/usage`:

```json
{
  "id": "chatgpt-rate-limit-reset-credits",
  "provider": "chatgpt",
  "label": "ChatGPT resets",
  "window": { "id": "resetCredits", "label": "Resets", "kind": "credit" },
  "unit": "credits",
  "remaining": 4
}
```

This is just the available count from:

```text
rate_limit_reset_credits.available_count
```

## Listing banked credits

Use the detail endpoint to see IDs, status, grant time, and expiry:

```sh
curl -fsS http://127.0.0.1:8788/v1/chatgpt/reset-credits | jq
```

Example response:

```json
{
  "provider": "chatgpt",
  "availableCount": 4,
  "credits": [
    {
      "id": "RateLimitResetCredit_...",
      "status": "available",
      "title": "Full reset (Weekly + 5 hr)",
      "grantedAt": "2026-06-12T00:16:56.403038Z",
      "expiresAt": "2026-07-12T00:16:56.403038Z"
    }
  ],
  "fetchedAt": 1783410000000
}
```

This calls:

```text
GET https://chatgpt.com/backend-api/wham/rate-limit-reset-credits
Authorization: Bearer <access token>
ChatGPT-Account-Id: <account id>
```

## Redeeming a reset credit

Redeeming is mutating and spends a real banked reset credit. It is disabled unless:

```yaml
providers:
  chatgpt:
    allowResetConsume: true
```

A caller must also provide both an explicit action header and confirmation body:

```sh
curl -fsS -X POST http://127.0.0.1:8788/v1/chatgpt/reset-credits/consume \
  -H 'content-type: application/json' \
  -H 'x-usagent-action: consume-chatgpt-reset-credit' \
  -d '{
    "creditId": "RateLimitResetCredit_...",
    "confirm": "consume-chatgpt-reset-credit"
  }'
```

Optional caller-supplied idempotency/request ID:

```json
{
  "creditId": "RateLimitResetCredit_...",
  "redeemRequestId": "your-request-id",
  "confirm": "consume-chatgpt-reset-credit"
}
```

If `redeemRequestId` is omitted, `usagent` generates one.

Successful local response:

```json
{
  "provider": "chatgpt",
  "creditId": "RateLimitResetCredit_...",
  "redeemRequestId": "...",
  "consumedAt": 1783410000000,
  "windowsReset": 2,
  "code": "reset",
  "redeemedAt": "2026-06-13T13:12:31Z"
}
```

The upstream WHAM body nests the timestamp:

```json
{
  "code": "reset",
  "windows_reset": 2,
  "credit": {
    "id": "RateLimitResetCredit_...",
    "status": "redeemed",
    "redeemed_at": "2026-06-13T13:12:31Z"
  }
}
```

`usagent` accepts either a top-level or `credit.redeemed_at` timestamp. `already_redeemed` is treated as a confirmed idempotent success. A 2xx body that cannot be confirmed leaves reset-once `unknown` and is never retried. If a later refresh sees the claimed credit no longer available and the account-wide weekly window recovered, the toggle is marked `consumed` without another POST.

This calls:

```text
POST https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume
Authorization: Bearer <access token>
ChatGPT-Account-Id: <account id>
Content-Type: application/json

{"credit_id":"RateLimitResetCredit_...","redeem_request_id":"..."}
```

After a successful consume, `usagent` refreshes the ChatGPT provider so `/v1/usage` reflects the reset windows/count as soon as possible.

## One-shot automatic redemption

`reset-once` is a toggle, not a standing config permission. It authorizes the running daemon to spend **at most one** banked credit when the account-wide weekly window is freshly reported as exhausted (`used_percent == 100`, seven-day duration, reset still in the future). It does not fire on the 5h/session window or model-specific weekly windows.

```sh
usagent reset-once arm
usagent reset-once status
usagent reset-once cancel
```

Repeated `arm` commands do not stack credits. Authorization is stored next to the snapshot as `snapshot.json.reset-once.json` and survives daemon restarts. After a successful spend, or if no eligible credit exists, the toggle disarms.

Selection rule: the available, unexpired credit with the earliest `expiresAt`. Credits with missing or invalid expiry are skipped. The selected credit ID and a request ID are persisted **before** the consume POST. An ambiguous outcome (`unknown`) is never retried automatically; verify the account, then `usagent reset-once cancel --acknowledge-unknown` if you have reconciled it. That acknowledgement does not refund a credit or authorize another spend.

Requirements:

- The HTTP daemon must be running the version that includes `reset-once`. Local CLI refreshes never redeem.
- Control commands must connect directly over loopback (`127.0.0.1` / `::1` / `localhost`). Wildcard listeners (`0.0.0.0` / `::`) are supported: remote usage readers can use the same daemon, but remote reset control is rejected. If the configured client URL uses a network address, run on the daemon host with `--daemon-url http://127.0.0.1:8788`. A daemon bound only to a specific network IP must also be configured to accept loopback connections.
- `allowResetConsume` is **not** required for `reset-once`. Manual consume still requires that config flag.
- Detection uses the normal ChatGPT refresh cadence (usually five minutes), not instantaneous exhaustion.

Local control API (loopback peer, no `Origin`, no forwarded-client headers, explicit action header + JSON confirm):

- `GET /v1/chatgpt/reset-once`
- `POST /v1/chatgpt/reset-once/arm` with `x-usagent-action: arm-chatgpt-reset-once`
- `POST /v1/chatgpt/reset-once/cancel` with `x-usagent-action: cancel-chatgpt-reset-once`

`usagent` usage output includes the current `ChatGPT reset-once:` line when the toggle is not `off`.

## Service-to-service use

A trusted local service can list and redeem credits by calling the daemon HTTP API on the configured loopback address/port.

Recommended flow:

1. `GET /v1/chatgpt/reset-credits`
2. Choose an `available` credit, usually the one with the earliest `expiresAt`.
3. Confirm the policy decision in your service.
4. `POST /v1/chatgpt/reset-credits/consume` with the credit ID, action header, and confirmation body.
5. Re-read `/v1/usage` or `/v1/chatgpt/reset-credits`.

Current safety model:

- Intended for trusted localhost callers.
- `allowResetConsume` must be explicitly enabled.
- Header and body confirmations are required to avoid accidental generic POSTs.
- No separate write-token auth is implemented yet. If exposing `usagent` beyond trusted loopback callers, add a write-auth layer before enabling consume.

## Error behavior

Common local API errors:

- `403`: reset consume is disabled by config, or reset-once was requested through a non-loopback connection, non-loopback Host, browser Origin, or forwarded-client headers.
- `400`: missing `creditId`, missing confirmation header, or missing confirmation body.
- `409`: reset-once is busy, or a previous redemption outcome is still `unknown`.
- `415`: `content-type` is not JSON.
- `502`: upstream ChatGPT/OpenAI endpoint rejected the request or returned an unexpected error.

Upstream failures include bounded response details and retry-after details when present, using the same safe HTTP error formatting as other providers.
