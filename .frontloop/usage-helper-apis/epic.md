---
title: Usage Helper APIs
slug: usage-helper-apis
status: active
created_at: 2026-07-07
completed_at:
---

## Goal

Add derived, cached-only helper API endpoints that make `usagent` useful as a local provider-selection service for other tools. Helpers should explain quota pressure, provider availability, upcoming resets, recommendation rankings, and "use it or lose it" expiring usage opportunities without destabilizing the existing `/v1/usage` schema.

## Principles

- Keep `/v1/usage` schema version 2 stable.
- Helper endpoints must derive from cached snapshots only; they must not call provider APIs synchronously.
- Prefer read-only GET endpoints for simple helpers; reserve POST for richer decision requests.
- Every recommendation should include scores, inputs, and caveats so clients can trust or override it.
- Do not claim true monetary cheapness unless explicit pricing/cost hints exist; initial "cheapest" means least scarce / lowest opportunity cost / safest local quota.
- Treat missing reset/window data as lower-confidence analysis, not as a reason to fabricate precision.
