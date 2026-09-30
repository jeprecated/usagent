# ChatGPT Work/Codex spending credits

`usagent` reports ChatGPT spending-credit balances alongside subscription quota and [banked rate-limit resets](CHATGPT_RESET_CREDITS.md). These are the flexible-usage credits used by ChatGPT Work/Codex, not OpenAI Platform/API billing credits or rate-limit reset vouchers.

## Setup

Enable `providers.chatgpt` with an existing Codex or Pi OAuth login, as described in the [README](../README.md#secrets). No additional credential, config field, or request is needed: the balance comes from the existing read-only `GET https://chatgpt.com/backend-api/wham/usage` poll.

The normal CLI, cached `GET /v1/usage`, and MCP usage output include it automatically:

```sh
usagent usage
usagent usage --json
curl -fsS http://127.0.0.1:8788/v1/usage | jq '.quotaItems[] | select(.id == "chatgpt-spending-credits")'
```

## Representation

An observed usage response includes:

```json
{
  "credits": {
    "has_credits": true,
    "unlimited": false,
    "balance": "62500"
  }
}
```

A finite nonnegative `credits.balance` (string or number) becomes:

- Item: `chatgpt-spending-credits` / **ChatGPT spending credits**
- Provider: `chatgpt`
- Window: `spendingCredits`, label **Credits**, kind `credit`
- Unit: `credits`; `remaining` is the reported balance
- `limit`, `used`, and `percentUsed`: zero because no budget or cumulative spend is known
- No reset or expiry timestamp; `estimated` is false/omitted
- Normal provider refresh timestamps, persistence, and stale/error handling

The CLI shows the balance without a percentage. Balance-only credits do not change subscription availability or provider recommendations: an empty optional spending balance must not block included usage, and a funded balance alone does not prove an exhausted subscription can run. Analysis reports low confidence with caveats rather than deriving a percentage or pace; these credits do not appear in the reset calendar or expiring-usage opportunities.

A reported zero balance is displayed. Missing, null, malformed, negative, or nonfinite balances are omitted without losing subscription usage. `unlimited: true` is omitted rather than represented as zero or an invented finite balance. `has_credits` alone is not a numeric balance.

The usage response does not provide the original grant amount or grant expiry. An expiry mentioned in an email cannot be recovered from this endpoint. Approximate local/cloud message counts are not treated as quota or spending balances.

## Sources and stability

This is a private ChatGPT endpoint, not a documented public billing API; its schema may change.

- [OpenAI help: flexible-usage credits](https://help.openai.com/en/articles/12642688-using-credits-for-flexible-usage-in-chatgpt-plus-pro)
- [Official Codex credit payload fields](https://github.com/openai/codex/blob/92bc601ad60542c92bf0bb1e7a2eb70b84ac49d2/codex-rs/codex-backend-openapi-models/src/models/credit_status_details.rs#L14-L39)
- [Official Codex maps the balance independently of rate limits](https://github.com/openai/codex/blob/92bc601ad60542c92bf0bb1e7a2eb70b84ac49d2/codex-rs/backend-client/src/client.rs#L756-L764)
