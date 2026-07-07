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


## Completion Summary

- Delegated implementation to Agentleman run agm-run-20260707141017-05wo0y and integrated after Claude judge ACCEPT verdict.
- Resolved parent integration conflict in `internal/analysis/expiring_usage.go` by combining provider/model metadata filters with overlap-aware expiring-usage fields.
- Added configurable non-secret provider/model tier and tag metadata with normalization and defaults that omit unknown metadata.
- Propagated metadata into provider/quota JSON and expiring-usage opportunities, plus tier/tag filters for HTTP and CLI expiring-usage paths.
- Added tests for configured, missing, custom provider metadata, JSON inclusion, and filters; verified with `devenv shell go test ./...`.

### Files Changed

- config.example.yaml
- internal/config/config.go
- internal/config/config_test.go
- internal/model/model.go
- internal/cache/snapshot.go
- internal/app/app.go
- internal/app/app_test.go
- internal/analysis/expiring_usage.go
- internal/analysis/expiring_usage_test.go
- internal/httpapi/expiring_usage.go
- internal/httpapi/httpapi_test.go
- internal/cli/expiring_usage.go
- .frontloop/usage-helper-apis/done/5000-add-provider-and-model-tier-metadata.md
- .frontloop/usage-helper-apis/brief-provider-metadata.md
- .frontloop/usage-helper-apis/judge-provider-metadata.md
