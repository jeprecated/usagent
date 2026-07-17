# usagent

Standalone Go usage/quota microservice for agent providers.

`usagent` refreshes provider usage for Claude Code/Fable, ChatGPT Pro/Codex, optional OpenAI API costs, and z.ai through a cache coordinator, normalizes the current snapshot to schema version 2, and exposes it over HTTP for Noctalia or any other client.

## Commands

```sh
devenv shell
check                 # go test ./...
start                 # go run ./cmd/usagent serve --config config.example.yaml
usage                 # go run ./cmd/usagent usage --config config.example.yaml
expiring              # go run ./cmd/usagent expiring-usage --config config.example.yaml --within 24h
mcp                   # go run ./cmd/usagent mcp --config config.example.yaml
```

Without devenv:

```sh
go test ./...
go run ./cmd/usagent serve --config config.example.yaml --host 127.0.0.1 --port 8787
go run ./cmd/usagent usage --config config.example.yaml
go run ./cmd/usagent expiring-usage --config config.example.yaml --within 24h
go run ./cmd/usagent mcp --config config.example.yaml
```

## CLI usage summary

`usagent` or `usagent usage` prints remaining quota for all providers in the configured usage view. It first calls the running daemon's `/v1/usage` endpoint; if the daemon is unavailable, it loads the same config/state and refreshes due providers locally. Use `usagent serve` to run the daemon.

```sh
usagent
usagent usage --config ~/.config/usagent/config.yaml
usagent usage --json
usagent usage --offline  # skip daemon lookup and refresh/read locally
```

`usagent expiring-usage` (alias `usagent expiring`) prints likely "use it or lose it" opportunities from `/v1/expiring-usage`. Daemon-backed requests are cached-only; if the daemon is unavailable, the CLI falls back to the same local refresh/read path as `usage --offline`.

```sh
usagent expiring --within 24h --minimum-remaining-percent 10
usagent expiring-usage --providers chatgpt,claude-code --tiers high,extra-high --tags chat,codex
usagent expiring-usage --include-low-confidence --json
```

`usagent mcp` starts a stdio MCP server for coding agents. It exposes tools for current usage (`usage`) and likely expiring quota/tokens to burn (`tokens_to_burn`). Tool calls prefer the running usagent daemon and fall back to the same local refresh/read path as the CLI when the daemon is unavailable.

```sh
usagent mcp --config ~/.config/usagent/config.yaml
```

Example MCP client entry:

```json
{"mcpServers":{"usagent":{"command":"usagent","args":["mcp","--config","~/.config/usagent/config.yaml"]}}}
```

## Endpoints

- `GET /healthz`
- `GET /readyz`
- `GET /v1/usage`
- `GET /v1/usage/analysis`
- `GET /v1/expiring-usage`
- `GET /v1/recommendations/provider`
- `GET /v1/resets`
- `GET /v1/availability`
- `GET /v1/providers`
- `GET /v1/chatgpt/reset-credits`
- `POST /v1/chatgpt/reset-credits/consume`

`/v1/config/raw` is intentionally not exposed. The usage/provider endpoints read the current cached snapshot; provider APIs are called only by the refresh coordinator. `GET /v1/usage/analysis` is cached-only: it derives per-item percent remaining, reset/window timing, inferred pace, pressure, projected exhaustion, confidence, and caveats from the current `/v1/usage` snapshot without refreshing providers. `GET /v1/expiring-usage` is also cached-only: it derives likely "use it or lose it" quota opportunities from the current `/v1/usage` snapshot and supports `within`/`withinMs`, `minimumRemainingPercent`, `providers`, `tiers`/`tier`, `tags`/`tag`, and `includeLowConfidence` query filters. Tier filters match provider or model tiers; tag filters use any matching provider/model tag. `GET /v1/recommendations/provider` supports the same cached-only `providers`, `tiers`/`tier`, `tags`/`tag`, `minimumRemainingPercent`, and `unit` filters. Expiring-usage responses separate raw reset-bound unused quota from actionable waste: `rawEstimatedWastedAmount`/`rawEstimatedWastedPercent` show the simple amount likely to reset unused, while `actionableWasteAmount`/`actionableWastePercent` are adjusted by history and overlapping parent windows. The legacy `estimatedWastedAmount` fields remain as raw estimated unused quota for compatibility, but opportunity scoring and CLI wording use actionable waste when available. For example, a 5h ChatGPT bucket may show 75% raw expiring unused, but only 18.75% actionable waste when a fresh weekly/monthly parent has enough future 5h resets to satisfy forecast demand. `GET /v1/resets` returns a reset calendar sorted by reset time, and `GET /v1/availability` returns conservative provider-level availability with blockers, constraints, and next likely improvement times. See [`docs/RATE_LIMITING.md`](docs/RATE_LIMITING.md) for provider polling minimums and retry-header handling, and [`docs/CHATGPT_RESET_CREDITS.md`](docs/CHATGPT_RESET_CREDITS.md) for ChatGPT/Codex reset banking details.

## Config

See `config.example.yaml`. It is real YAML. Paths beginning with `~` are expanded at runtime, and `%STATE%` expands to `$XDG_STATE_HOME` or `~/.local/state`.

## Secrets

Secrets must be provided by runtime environment variables or secret files. Do not put tokens in config, Docker images, Nix store paths, or checked-in files.

Claude Code/Fable usage is fetched from the Claude Code OAuth usage endpoint using the local Claude Code credentials file path from config. The access token is read from `claudeAiOauth.accessToken` at refresh time and is never returned in HTTP responses. When the endpoint includes `extra_usage`, usagent exposes enabled Extra Credits as a monthly currency quota item, converting the API's cent values to dollars/euros/etc.

ChatGPT Pro/Codex subscription usage is fetched from ChatGPT's private `/backend-api/wham/usage` endpoint when `providers.chatgpt.enabled=true`. By default usagent reads the Codex CLI OAuth login from `~/.codex/auth.json`. The configured `authPath` also accepts Pi's `~/.pi/agent/auth.json` format and reads its `openai-codex` OAuth entry, so Pi users can point usagent at the credentials Pi automatically refreshes. Alternatively set `CHATGPT_ACCESS_TOKEN` and optionally `CHATGPT_ACCOUNT_ID`. Reset banking is represented in normal stats as `chatgpt-rate-limit-reset-credits`; detailed banked reset-credit records are available from `GET /v1/chatgpt/reset-credits`. See [`docs/CHATGPT_RESET_CREDITS.md`](docs/CHATGPT_RESET_CREDITS.md) for source references, response fields, service-to-service examples, and redemption safety notes.

Redeeming a reset credit is intentionally gated. It is disabled unless `providers.chatgpt.allowResetConsume=true`, and callers must use JSON, the confirmation header, and a confirmation body:

```sh
curl -fsS -X POST http://127.0.0.1:8788/v1/chatgpt/reset-credits/consume \
  -H 'content-type: application/json' \
  -H 'x-usagent-action: consume-chatgpt-reset-credit' \
  -d '{"creditId":"RateLimitResetCredit_...","confirm":"consume-chatgpt-reset-credit"}'
```

This endpoint calls OpenAI's `/backend-api/wham/rate-limit-reset-credits/consume` endpoint and spends a real banked reset credit.

OpenAI API spend is separate and optional. It is fetched from the Admin Costs API when `providers.openai.enabled=true`. This does **not** track ChatGPT Plus/Pro subscription quota. Provide an admin key in `OPENAI_ADMIN_KEY` (or the configured `apiKeyEnv`) and define USD budgets:

```yaml
providers:
  openai:
    enabled: true
    apiKeyEnv: "OPENAI_ADMIN_KEY"
    budgets:
      - id: "weekly-usd"
        unit: "usd"
        limit: 50
        window: { id: "week", label: "W", kind: "weekly" }
      - id: "monthly-usd"
        unit: "usd"
        limit: 200
        window: { id: "month", label: "M", kind: "monthly" }
```

z.ai usage is fetched from `GET https://api.z.ai/api/monitor/usage/quota/limit` when `providers.zAi.enabled=true`. Provide `ZAI_API_KEY` (fallback `GLM_API_KEY`) or override auth/header settings for compatible endpoints:

```yaml
providers:
  zAi:
    enabled: true
    tokenEnv: "ZAI_API_KEY"
    tokenEnvFallbacks: ["GLM_API_KEY"]
    excludeLimitTypes: ["TIME_LIMIT"] # hides web-search/TIME_LIMIT by default
```

Custom HTTP JSON providers can map local/proxy quota APIs into usagent quota items. Mapping strings are JSON dot paths by default; use `literal:<value>` for constants. Token values are read from env vars only:

```yaml
providers:
  custom:
    - id: "my-provider"
      label: "Mine"
      enabled: true
      endpoints:
        - id: "quota"
          url: "https://example.com/quota"
          auth: { type: "bearer", tokenEnv: "MINE_TOKEN" }
          itemsPath: "items"
          item:
            id: "id"
            label: "literal:Mine quota"
            window: { id: "window.id", label: "window.label", kind: "window.kind", resetAt: "resetAt", resetAtFormat: "unixMs" }
            unit: "unit"
            limit: "limit"
            used: "used"
            remaining: "remaining"
            percentUsed: "percentUsed"
            visible: true
usageView:
  providers: ["claude-code", "chatgpt", "z-ai", "my-provider"]
```

## Docker

```sh
docker build -t usagent .
docker run --rm -p 8787:8787 \
  -v "$PWD/config.example.yaml:/etc/usagent/config.yaml:ro" \
  -v "$HOME/.claude/.credentials.json:/home/usagent/.claude/.credentials.json:ro" \
  -v usagent-state:/var/lib/usagent \
  usagent
```

## Nix flake

```sh
nix build .#usagent
nix run .# -- --config config.example.yaml
nix build .#oci
```

The flake exposes:

- `packages.default` / `packages.usagent`
- `packages.oci`
- `apps.default` / `apps.usagent`
- `nixosModules.usagent`
- `homeManagerModules.usagent`

## NixOS service

```nix
{
  inputs.usagent.url = "path:/path/to/usagent";

  outputs = { self, nixpkgs, usagent, ... }: {
    nixosConfigurations.host = nixpkgs.lib.nixosSystem {
      modules = [
        usagent.nixosModules.usagent
        {
          services.usagent = {
            enable = true;
            host = "127.0.0.1";
            port = 8787;
            config.providers.claudeOAuth = {
              enabled = true;
              credentialsPath = "/run/credentials/usagent/claude-credentials.json";
            };
          };
        }
      ];
    };
  };
}
```

The generated YAML lives in the Nix store and must not contain plaintext secrets. Point it at runtime credential files instead.

## Home Manager user service

```nix
{
  imports = [ inputs.usagent.homeManagerModules.usagent ];

  services.usagent = {
    enable = true;
    config.providers.claudeOAuth.enabled = true;
    # Defaults to ~/.claude/.credentials.json and XDG state for snapshots.
  };
}
```

## Target bar UI

```text
Usage: Claude S:100% W:50% F:24% [2h14m] · ChatGPT S:75% W:40% · z.ai S:90% W:97%
```

ChatGPT, OpenAI API costs, z.ai, and configured custom providers are real pull providers when enabled; disabled providers can still appear as metadata-only entries via `usageView.providers`.
