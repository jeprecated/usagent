# Delegation brief: Add provider and model tier metadata

Repository: `/home/jmo/Development/projects/usagent`

Use Jujutsu workflow only (`jj st`, `jj diff`); do not run direct `git` commands. Preserve unrelated existing changes. Implement active Frontloop task `.frontloop/usage-helper-apis/in_progress/5000-add-provider-and-model-tier-metadata.md`.

## Goal

Let usagent expose configurable provider/model metadata such as tier and tags so downstream services can decide whether a quota opportunity fits their task without usagent making that judgement.

## Acceptance criteria

- Config supports provider/model metadata such as tier (`medium`, `high`, `extra-high` or custom strings) and tags without storing secrets.
- Helper responses can include this metadata for providers and quota items where applicable.
- Filtering by tier/tags is supported where it is useful, especially expiring-usage and recommendation endpoints.
- Defaults do not hard-code task suitability; unknown providers simply omit tier metadata.
- Tests cover configured metadata, missing metadata, custom providers, and JSON response inclusion.

## Existing context

- `internal/config/config.go` owns YAML config and defaults.
- `model.Provider` and `model.QuotaItem` are stable `/v1/usage` structs; avoid breaking existing clients.
- `analysis.ExpiringUsageOpportunity` already has `ProviderTier` and `ProviderTags` fields but they may currently be empty.
- `GET /v1/usage/analysis` exists and can include metadata if useful.
- A future provider recommendation endpoint should be able to filter by metadata.

## Suggested design

Add a dedicated, non-secret config section such as:

```yaml
metadata:
  providers:
    chatgpt:
      tier: high
      tags: [chat, pro]
      items:
        chatgpt-primary:
          tier: high
          tags: [reasoning]
```

or equivalent. Keep it simple and documented. Model-level/item-level metadata may be keyed by quota item ID, provider+item ID, or model ID if model IDs exist. For current data, quota item ID is probably sufficient.

Implement a small resolver in config/app/analysis so helper responses can ask for metadata by provider/item. Avoid putting task-suitability policy in scoring.

## Filtering

Add tier/tag query filters where useful now:

- `/v1/expiring-usage?tier=high&tags=reasoning,pro` or similar.
- CLI flags for expiring-usage if straightforward (`--tier`, `--tags`) so parity is maintained.
- If adding filters to `/v1/usage/analysis` is useful and low-risk, include them; otherwise document that recommendation endpoint will consume metadata later.

Filtering semantics should be documented in tests: tier exact match; tags likely require all requested tags or any requested tag, but choose one and document in README/help.

## Tests

Cover:

- configured provider metadata appears in expiring-usage and/or usage-analysis JSON;
- item metadata overrides or augments provider metadata where applicable;
- missing metadata omits tier/tags and does not invent defaults;
- custom provider metadata works;
- tier/tag filters include/exclude expected opportunities;
- CLI query parameter forwarding if CLI flags are added.

Run:

```sh
devenv shell go test ./...
```

## No-go areas

- Do not store secrets in metadata.
- Do not hard-code examples as defaults; leave unknown providers empty unless configured.
- Do not alter scoring based on tier/tags.
- Do not break `/v1/usage` compatibility.

## Deliverable

Summary, changed files, validation results, residual risks, and workspace/change identifier for judgment/integration.
