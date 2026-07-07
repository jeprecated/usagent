# Original brief

See `.frontloop/usage-helper-apis/brief-provider-metadata.md`. Acceptance criteria:

- Config supports provider/model metadata such as tier (`medium`, `high`, `extra-high` or custom strings) and tags without storing secrets.
- Helper responses can include this metadata for providers and quota items where applicable.
- Filtering by tier/tags is supported where it is useful, especially expiring-usage and recommendation endpoints.
- Defaults do not hard-code task suitability; unknown providers simply omit tier metadata.
- Tests cover configured metadata, missing metadata, custom providers, and JSON response inclusion.

# Payload

Repo: `/home/jmo/Development/workspaces/usagent/agm-default-3`
Change: `vumtsqvrwvvlkkktwrmqkyynknwnplvw` / `711f0d281c974b5801217843bc3b94cd3de75bbb` (`Add provider metadata filters`)
Working copy: empty child `yuxkollqyoskmmqzvqkrtoyxpxuxlumo`.

Changed files:

- `config.example.yaml`
- `internal/analysis/expiring_usage.go`
- `internal/analysis/expiring_usage_test.go`
- `internal/app/app.go`
- `internal/app/app_test.go`
- `internal/cache/snapshot.go`
- `internal/cli/expiring_usage.go`
- `internal/config/config.go`
- `internal/config/config_test.go`
- `internal/httpapi/expiring_usage.go`
- `internal/httpapi/httpapi_test.go`
- `internal/model/model.go`

# Delegate's report

Implemented configurable non-secret provider/model metadata (tier/tags), propagated it into provider and quota JSON responses, added tier/tag filtering for expiring-usage HTTP/CLI paths, and covered configured, missing, custom, and JSON inclusion cases with tests. Created payload commit `Add provider metadata filters`.

Validation:

- `go test ./...` passed.
- `devenv shell go test ./...` passed.

Residual risk: No recommendation endpoint exists in this branch, so metadata filtering was added to expiring-usage HTTP and CLI paths only.

# Judge instructions

Inspect against acceptance criteria. Verify metadata is non-secret and configurable, defaults do not hard-code tiers, item/provider inheritance or overrides are coherent, JSON fields are included only where appropriate, filtering semantics are tested, and existing `/v1/usage` compatibility is acceptable.
