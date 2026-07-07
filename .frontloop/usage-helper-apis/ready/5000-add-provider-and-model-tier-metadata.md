---
title: Add provider and model tier metadata
priority: medium
---

## Goal

Let usagent expose configurable provider/model metadata such as tier and tags so downstream services can decide whether a quota opportunity fits their task without usagent making that judgement.

## Acceptance Criteria

- Config supports provider/model metadata such as tier (`medium`, `high`, `extra-high` or custom strings) and tags without storing secrets.
- Helper responses can include this metadata for providers and quota items where applicable.
- Filtering by tier/tags is supported where it is useful, especially expiring-usage and recommendation endpoints.
- Defaults do not hard-code task suitability; unknown providers simply omit tier metadata.
- Tests cover configured metadata, missing metadata, custom providers, and JSON response inclusion.

## Design Decisions

- Provider/model tier is metadata, not an implicit scoring policy.
- Downstream services decide whether a model/provider can do the task.
- Examples from review: z.ai GLM 5.2 as medium, ChatGPT 5.5 and Opus 4.8 as high, Fable over API/usage credits as extra-high.

## Implementation Notes

This task can be implemented before or alongside expiring-usage. Consider whether metadata belongs under usageView, providers, or a dedicated helper metadata config section.
