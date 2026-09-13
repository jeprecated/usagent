# Anthropic Console/API credits

`providers.anthropic` estimates prepaid USD credits using Anthropic's organization Cost API. It is separate from `providers.claudeOAuth`, which tracks Claude subscription limits and subscription extra usage. Enabling it automatically adds `anthropic` to the usage view; existing provider order is preserved. Clients that filter windows should allow the `credits` window.

## Credentials

Create a dedicated key (for example, named `usagent`) in [Claude Console → Settings → Admin keys](https://platform.claude.com/settings/admin-keys). The key must belong to the **same organization** as the billing balance. Anthropic documents Admin API access as unavailable for individual accounts; ordinary workspace-scoped inference keys do not work here.

Console Admin keys have broad administrative access, not selectable read-only scopes. Usagent only makes GET cost-report requests, never buys credits or changes billing. Keep the key on the poller host, not on require-daemon readers.

Supply either:

- `ANTHROPIC_ADMIN_KEY` in the daemon's runtime environment (or the variable named by `apiKeyEnv`). A systemd service does not inherit exports from your interactive shell; use a protected EnvironmentFile/secret manager.
- A raw key file referenced by `apiKeyFile`. Put only the key in the file, restrict its permissions to the daemon user (`0600`), and keep it outside the repository/Nix store. `~` and `%STATE%` paths are expanded. The file is re-read at each refresh, so rotation needs no restart. An explicit file takes precedence over the environment, including when it is missing or empty: errors do not silently fall back to a different organization.

The key is never part of quota JSON or persisted snapshots. Redirects are refused to avoid forwarding the privileged key. The optional `costsEndpoint` override requires HTTPS, except HTTP loopback endpoints for tests/local proxies; query strings, embedded credentials, and fragments are rejected. The default is `https://api.anthropic.com/v1/organizations/cost_report`.

## Capture a checkpoint

There is no documented public prepaid-balance endpoint used by this integration. Instead, configure a balance and the **matching Cost API total at the time you recorded that balance**:

```text
used USD = current reported cost since reportStart − reportedCostUSD
estimated remaining USD = max(0, balanceUSD − used USD)
```

All three checkpoint fields are required; no starting balance, date, or spending baseline is hard-coded or automatically inferred. Zero is valid, but must be explicit. Amounts must be finite and non-negative.

**Do not simply set `reportStart` to the current time.** Cost reports use daily UTC buckets. `reportStart` must resolve to UTC midnight. `reportedCostUSD` subtracts costs already present in those buckets when you captured the balance, avoiding double-counting earlier spending that day.

1. Pause API/Console/Claude Code usage billed to this organization. Allow reporting to settle (typically five minutes, sometimes longer).
2. Read the remaining balance from the Console billing page.
3. Fetch the Cost API total from today's UTC midnight. The following Bash example requires `curl` and `jq`, and assumes the key is already in `ANTHROPIC_ADMIN_KEY`. It prints the two reporting fields, not your key:

   ```bash
   report_start="$(date -u +%Y-%m-%d)T00:00:00Z"
   report_end="$(date -u -d tomorrow +%Y-%m-%d)T00:00:00Z"
   report=$(curl --fail --silent --show-error --get \
     'https://api.anthropic.com/v1/organizations/cost_report' \
     -H "x-api-key: $ANTHROPIC_ADMIN_KEY" \
     -H 'anthropic-version: 2023-06-01' \
     -H 'User-Agent: usagent/0.1' \
     --data-urlencode "starting_at=$report_start" \
     --data-urlencode "ending_at=$report_end" \
     --data-urlencode 'bucket_width=1d' \
     --data-urlencode 'limit=31') &&
   reported_cost=$(printf '%s' "$report" | jq -er '
     if .has_more != false or (.data | type) != "array"
     then error("unexpected/incomplete report; do not capture checkpoint")
     else [.data[].results[] |
       if .currency != "USD" then error("non-USD cost")
       else (.amount | tonumber) end] | (add // 0) / 100
     end') &&
   printf 'reportStart: "%s"\nreportedCostUSD: %s\n' "$report_start" "$reported_cost"
   ```

   The setup query spans only one day, so it must not require pagination. Amount strings are **cents**, including fractional cents; divide their sum by 100 and retain the full precision for `reportedCostUSD`. Do not round each result or use token-price estimates. If capturing around UTC midnight, restart the capture with the new day.

4. Put the balance from step 2 and the two fields from step 3 into your private config. The placeholders below must be replaced with numbers/date; this is not a ready-to-run balance:

   ```yaml
   providers:
     anthropic:
       enabled: true
       apiKeyEnv: "ANTHROPIC_ADMIN_KEY"
       # Or: apiKeyFile: "~/.config/usagent/anthropic-admin-key"
       refreshMs: 300000
       staleMs: 900000
       checkpoint:
         balanceUSD: <remaining balance from Console>
         reportStart: "<UTC date>T00:00:00Z"
         reportedCostUSD: <USD total from the query above>
   ```

5. Restart the daemon with the updated config and run `usagent usage` or `usagent usage --json` against that daemon. The new configuration takes effect on the next due successful provider refresh; existing cached values can remain visible until then. Do not run local/offline polling on a require-daemon reader.

## What you see

The normal usage snapshot, CLI, HTTP API, and MCP usage tool expose one quota item:

- Provider: `anthropic` / **Anthropic API**
- Item: `anthropic-api-credits` / **Anthropic API credits (estimated)**
- Window: `credits`, kind `custom`, **no monthly reset or inferred expiry**
- Unit: `usd`; `limit` is checkpoint balance, `used` is spend since checkpoint, `remaining` is estimated balance
- `estimated: true` distinguishes the derived balance for machine consumers
- Warning at 80% spent, critical at exhaustion; overspending retains the full `used` amount while remaining is floored at zero and percent used is capped at 100

Displayed monetary amounts are rounded to two decimals only after summing the report and subtracting the baseline. Freshness describes when costs were fetched, **not** when Anthropic finished billing them. With reporting lag and five-minute polling, consumption is not real-time.

## Reconcile, top up, or change organizations

Repeat the checkpoint capture after a purchase, refund, credit expiration, manual billing adjustment, or change of organization/key. Replace all three checkpoint fields together. This starts a new tracking baseline; it does not silently rewrite previous local history. Do not reuse a checkpoint from a different organization.

A full report total below `reportedCostUSD` is treated as a refresh error, not as newly available credits: check the organization/date and reconcile. On failed requests (including 401/403, 429, malformed data, and incomplete pagination), usagent retains the previous values as stale rather than publishing partial spending as a fresh balance.

Each refresh queries all organization costs from `reportStart` through today's partial UTC bucket, including all workspaces and cost types returned by the endpoint. There is no workspace/key filter. Pagination uses `has_more`/`next_page` with 31-day pages, loop detection, and a 100-page safety limit; reconcile to a recent checkpoint before exceeding that range. Earlier buckets are fetched again to pick up reporting corrections. No additional database or automatic first-fetch checkpoint is used, so restarts cannot silently rebase your balance.

Anthropic excludes Priority Tier costs from this endpoint. This estimate therefore is not suitable as a complete billing balance for Priority Tier contracts. Top-ups, refunds, expiry, and other non-usage adjustments are not automatically discovered. The Console remains the authoritative balance.

## Official references

- [Usage & Cost API](https://platform.claude.com/docs/en/manage-claude/usage-cost-api): availability, authentication, reporting delay, cost types, and Priority Tier exclusion.
- [Get Cost Report](https://platform.claude.com/docs/en/api/admin/cost_report/retrieve): daily UTC buckets, decimal-string cents, and pagination.
- [Create an Admin API key](https://platform.claude.com/docs/en/manage-claude/admin-api-keys): Console key creation and privilege scope.
- [API billing and prepaid credits](https://support.claude.com/en/articles/8977456-how-do-i-pay-for-my-claude-api-usage).
