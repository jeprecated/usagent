# Cursor usage

Cursor's public documentation says its monthly allowance has separate **Cursor Models** and **Other Models** pools. Cursor Grok 4.5 shares the Cursor Models pool with Composer, so Cursor does not expose Grok-only remaining quota.

`usagent` reads Cursor's undocumented Connect RPC:

```text
POST https://api2.cursor.sh/aiserver.v1.DashboardService/GetCurrentPeriodUsage
Authorization: Bearer <Cursor access token>
Content-Type: application/json
Connect-Protocol-Version: 1
{}
```

The response supplies the billing-cycle end and `planUsage.autoPercentUsed` / `planUsage.apiPercentUsed`. They are normalized as `cursor-models` and `cursor-other-models` percentage quota items. The Cursor Models response identifies the supported Grok 4.5 model IDs but has no per-model usage breakdown.

By default, the provider rereads `~/.config/cursor/auth.json` and uses its `accessToken`; `CURSOR_ACCESS_TOKEN` overrides it. Cursor owns refresh-token rotation, so a refreshed login is picked up on the next poll. The RPC is private and may change; keep polling at the configured five-minute minimum.

Sources:

- [Cursor usage and limits](https://cursor.com/help/models-and-usage/usage-limits)
- [Cursor models and pricing](https://cursor.com/docs/models-and-pricing)
