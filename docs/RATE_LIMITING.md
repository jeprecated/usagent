# Rate-limit policy

`usagent` is a polling daemon. HTTP readers (`/v1/usage`, `usagent usage`) never call upstream provider APIs when the daemon is available; they read the latest persisted snapshot. Provider calls happen only in the refresh loop or in explicit local/offline CLI fallback.

## Global behavior

- At most one in-flight refresh per provider is allowed.
- Each provider snapshot stores `nextRefreshAt`.
- Successful refreshes set `nextRefreshAt = now + refreshMs`.
- Failed refreshes set `nextRefreshAt = max(now + refreshMs, upstream retry-after/reset delay)`.
- This means a short or missing upstream retry header cannot create a tight retry loop.
- The loop wakes once per second only to check the local snapshot; it does not call providers unless due.

## Built-in provider minimums

The built-in provider refresh intervals are clamped during config normalization:

| Provider | Minimum refresh interval | Reason |
| --- | ---: | --- |
| Claude OAuth usage | 5 minutes | Anthropic documents 429 + `retry-after`; OAuth usage is also account-scoped and should not be polled per widget render. |
| ChatGPT WHAM usage | 5 minutes | The ChatGPT/Codex usage endpoint is private and account-scoped; it should not be polled per widget render. |
| OpenAI organization Costs | 10 minutes | The Costs API is an admin/organization endpoint for Platform/API spend and quota changes slowly; OpenAI recommends pacing requests and respecting retry headers. |
| z.ai quota endpoint | 5 minutes | Public docs list 429 rate-limit/overload errors but no stable reset headers for the quota endpoint. |

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

Anthropic's API rate-limit documentation says 429 responses include `retry-after`, and Anthropic responses expose request/token remaining/reset headers. The Claude Code OAuth usage endpoint is not the normal Messages API, but it returns HTTP 429 in the same style, so `usagent` applies the Anthropic retry policy and the 5-minute minimum.

### ChatGPT WHAM usage

ChatGPT Pro/Codex quota is read from an undocumented ChatGPT web endpoint using OAuth credentials from the Codex/ChatGPT login. `usagent` respects retry headers and otherwise waits at least the configured refresh interval. The default/minimum ChatGPT refresh is 5 minutes.

### OpenAI Costs

OpenAI documents 429 rate-limit errors and recommends pacing requests, avoiding unnecessary calls, and respecting response headers. The Costs endpoint is queried once per refresh for the widest configured budget range, then weekly/monthly budget rows are derived locally. The default/minimum OpenAI API-cost refresh is 10 minutes.

### z.ai quota

Z.ai public docs list 429 errors for request rate limiting and temporary overload. The quota endpoint used here is the same internal quota endpoint used by third-party quota tools; it may not provide reset headers. `usagent` respects retry headers if present and otherwise waits until the next configured refresh.
