# Rate-limit policy

`usagent` is a polling daemon. HTTP readers never call upstream provider APIs; they read the daemon's latest snapshot. Client commands may call providers only through policy-permitted local fallback or intentional local/offline operation.

## Central daemon clients

In a one-poller/many-reader deployment, every reader uses `client.mode: require-daemon`. Require-daemon CLI and MCP clients never poll providers: on connection, timeout, non-2xx, missing-endpoint, or response-validation failure they fail closed without entering refresh scheduling, backoff, local locks, state, or history paths. Only the central `usagent serve` process participates in provider refresh scheduling and upstream retry/backoff.

`prefer-daemon` intentionally retains local fallback and its shared refresh lock for backward compatibility. An explicit `--daemon-url` suppresses an alternate-daemon attempt but still permits local fallback in prefer-daemon mode. `local-only` and policy-permitted `--offline` are intentional local polling modes and should not be used on pure reader hosts.

Upgrade the central daemon before its require-daemon clients. If a newer client requests an endpoint an older daemon does not provide, require-daemon fails closed instead of polling the provider itself.

## Global behavior

- At most one in-flight refresh per provider is allowed.
- Each provider snapshot stores `nextRefreshAt`.
- Successful refreshes set `nextRefreshAt = now + refreshMs`.
- Failed refreshes set `nextRefreshAt = max(now + refreshMs, upstream retry-after/reset delay)`.
- This means a short or missing upstream retry header cannot create a tight retry loop.
- The loop wakes once per second only to check the local snapshot; it does not call providers unless due.
- Local/offline CLI fallback takes a shared refresh lock before calling providers so concurrent CLI invocations do not stampede upstream APIs when the daemon is unavailable.

## Built-in provider minimums

The built-in provider refresh intervals are clamped during config normalization:

| Provider | Minimum refresh interval | Reason |
| --- | ---: | --- |
| Claude OAuth usage | 15 minutes | The OAuth usage endpoint is account-scoped and has proven sensitive to polling bursts; local/offline fallback also uses a cross-process refresh lock. |
| ChatGPT WHAM usage | 5 minutes | The ChatGPT/Codex usage endpoint is private and account-scoped; it should not be polled per widget render. |
| OpenAI organization Costs | 10 minutes | The Costs API is an admin/organization endpoint for Platform/API spend and quota changes slowly; OpenAI recommends pacing requests and respecting retry headers. |
| Anthropic organization Costs | 5 minutes | Cost reporting typically lags by five minutes; all readers share the cached estimate. |
| z.ai quota endpoint | 5 minutes | Public docs list 429 rate-limit/overload errors but no stable reset headers for the quota endpoint. |
| Cursor dashboard RPC | 5 minutes | The account-scoped Connect RPC is private and must not be polled per widget render. |

Custom providers keep their configured interval because their rate-limit contract is provider-specific, but they still honor `Retry-After` / `Retry-After-Ms` and the no-tight-loop failure rule.

## Header support

`usagent` parses:

- `Retry-After` seconds
- `Retry-After` HTTP-date
- `Retry-After-Ms`

For Claude/Anthropic responses, if `Retry-After` is absent, `usagent` also looks at exhausted Anthropic buckets:

- `anthropic-ratelimit-requests-remaining: 0` + `anthropic-ratelimit-requests-reset`
- `anthropic-ratelimit-tokens-remaining: 0` + `anthropic-ratelimit-tokens-reset`

When both request and token buckets are exhausted, the later reset wins.

## Provider-specific notes

### Claude OAuth usage

Anthropic's API rate-limit documentation says 429 responses include `retry-after`, and Anthropic responses expose request/token remaining/reset headers. The Claude Code OAuth usage endpoint is not the normal Messages API, but it returns HTTP 429 in the same style, so `usagent` applies the Anthropic retry policy and a conservative 15-minute minimum.

### ChatGPT WHAM usage

ChatGPT Pro/Codex quota is read from undocumented ChatGPT web endpoints using OAuth credentials from the Codex/ChatGPT login. `usagent` respects retry headers and otherwise waits at least the configured refresh interval. The default/minimum ChatGPT refresh is 5 minutes. Reset-credit listing/consumption is documented in [`CHATGPT_RESET_CREDITS.md`](CHATGPT_RESET_CREDITS.md); consumption is mutating, explicitly triggered, and not part of the refresh loop.

### Cursor dashboard usage

Cursor Models/Other Models usage is read from a private, account-scoped dashboard RPC. `usagent` honors retry headers and otherwise waits at least five minutes before another poll. See [`CURSOR_USAGE.md`](CURSOR_USAGE.md).

### OpenAI Costs

OpenAI documents 429 rate-limit errors and recommends pacing requests, avoiding unnecessary calls, and respecting response headers. The Costs endpoint is queried once per refresh for the widest configured budget range, then weekly/monthly budget rows are derived locally. The default/minimum OpenAI API-cost refresh is 10 minutes.

### Anthropic Costs

Anthropic permits sustained polling once per minute, but usagent uses a five-minute minimum/default to match typical reporting latency. Each refresh follows report pagination across all days since the configured checkpoint, with a 100-page safety limit. It honors Anthropic retry/reset headers and retains the cached estimate as stale on any failure instead of publishing partial totals. See [`ANTHROPIC_CREDITS.md`](ANTHROPIC_CREDITS.md).

### z.ai quota

Z.ai public docs list 429 errors for request rate limiting and temporary overload. The quota endpoint used here is the same internal quota endpoint used by third-party quota tools; it may not provide reset headers. `usagent` respects retry headers if present and otherwise waits until the next configured refresh.
