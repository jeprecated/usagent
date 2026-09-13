# usagent

Standalone Go usage/quota microservice for agent providers.

`usagent` refreshes provider usage for Claude Code/Fable, ChatGPT Pro/Codex, optional OpenAI API costs and Anthropic API credit estimates, and z.ai through a cache coordinator, normalizes the current snapshot to schema version 2, and exposes it over HTTP for Noctalia or any other client.

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

`usagent` or `usagent usage` prints remaining quota for all providers in the configured usage view. With the default `client.mode: prefer-daemon`, it first calls the selected daemon's `/v1/usage` endpoint; if the daemon is unavailable, it may load the same config/state and refresh due providers locally. Use `usagent serve` to run the daemon. See [Central daemon operation](#central-daemon-operation) for fail-closed and local-only policies.

```sh
usagent
usagent usage --config ~/.config/usagent/config.yaml
usagent usage --json
usagent usage --offline  # skip daemon lookup and refresh/read locally
```

The table keeps provider errors short and shows each distinct error once per provider. Authentication failures show `authentication required; sign in again`; cached quotas retain their stale age. Use `usagent usage --json` for full error details.

`usagent expiring-usage` (alias `usagent expiring`) prints likely "use it or lose it" opportunities from `/v1/expiring-usage`. Daemon-backed requests are cached-only. Under the default `prefer-daemon` policy, an unavailable daemon may fall back to the same local refresh/read path as `usage --offline`; configured client policy governs this behavior. `require-daemon` fails closed, while `local-only` bypasses daemon access.

```sh
usagent expiring --within 24h --minimum-remaining-percent 10
usagent expiring-usage --providers chatgpt,claude-code --tiers high,extra-high --tags chat,codex
usagent expiring-usage --include-low-confidence --json
```

`usagent mcp` starts a stdio MCP server for coding agents. It exposes tools for current usage (`usage`) and likely expiring quota/tokens to burn (`tokens_to_burn`). Under the default `prefer-daemon` policy, tool calls prefer the running daemon and may fall back to the same local refresh/read path as the CLI when it is unavailable; configured client policy governs fallback. `require-daemon` fails closed, while `local-only` bypasses daemon access.

```sh
usagent mcp --config ~/.config/usagent/config.yaml
```

Example MCP client entry:

```json
{"mcpServers":{"usagent":{"command":"usagent","args":["mcp","--config","~/.config/usagent/config.yaml"]}}}
```

## Central daemon operation

A supported one-poller/many-reader deployment runs one daemon on a private host and makes every CLI and MCP reader require it. For example, Lattice can listen on its Tailnet interface while its own clients use the local listener:

```yaml
# Lattice: replace the illustrative address with its actual Tailnet IP.
server:
  host: "100.64.0.10"
  port: 8788
client:
  url: "http://100.64.0.10:8788"
  mode: "require-daemon"
```

`client.mode` controls client commands only; it does not disable `usagent serve`. Thus Lattice can run `usagent serve` and use `usagent usage` or `usagent mcp` against that same daemon with one configuration. Other private hosts, such as Overton, can require Lattice through Tailnet/MagicDNS:

```yaml
# Overton-style private reader
client:
  url: "http://lattice:8788"
  mode: "require-daemon"
providers:
  claudeOAuth: { enabled: false }
  chatgpt: { enabled: false }
  openai: { enabled: false }
  zAi: { enabled: false }
  custom: []
```

Disabling every provider on a pure client is defense in depth against accidentally running `serve`; `require-daemon` already prevents client commands from polling locally. Host-specific Nix composition, firewall rules, activation, and live deployment are deliberately deferred to a separate deployment change.

### Client modes and destination selection

- `prefer-daemon` (the default) tries the selected daemon first and retains the legacy alternate-user-config and local refresh fallback behavior.
- `require-daemon` fails closed: daemon connection, endpoint, or response errors never fall back to another daemon, local state/history, credentials, refresh locks, or provider APIs.
- `local-only` skips daemon lookup and uses the local refresh/read path.

Client destinations are selected in this order: explicit `--daemon-url`, explicitly supplied legacy `--host`/`--port`, `client.url`, then an HTTP origin derived from `server.host`/`server.port`. The legacy host and port flags remain compatible; supplying either selects legacy derivation wholesale, with any missing half taken from `server`. `--daemon-url` is an HTTP(S) origin override and conflicts with `--host`, `--port`, `--offline`, and `local-only`. It still requires a loadable config because that config supplies the invocation policy. In `prefer-daemon`, an explicit `--daemon-url` suppresses the alternate user-config daemon attempt, but a failure may still fall back locally. `--offline` is rejected by `require-daemon`; otherwise it selects intentional local operation where policy permits.

Treat the intended config file's presence as part of a fail-closed deployment contract and pass `--config` (or set `USAGENT_CONFIG`) explicitly. If neither is supplied and no XDG user config exists, the current fallback is the CWD-relative path `config.example.yaml`; a different working directory may therefore fail to load or load an unintended example file rather than the deployment configuration. MCP process startup intentionally tolerates a config-load failure for backward compatibility, so startup alone does not prove that the intended config loaded. Every MCP tool call reloads the config and returns a tool error if loading fails.

### Private-network security and credentials

This increment relies on a Tailnet or equivalent private network as its security boundary. `server.readAuth.mode` remains `none`-only: plain HTTP must remain private, HTTPS may be terminated by an external private proxy, and `usagent` must not be exposed to the public internet. No bearer authentication or built-in TLS listener is provided.

Keep `providers.chatgpt.allowResetConsume: false` on a broadly reachable central listener. Enabling it exposes a mutating endpoint that spends real reset credits and needs a later security design even though request confirmation is required.

The daemon rereads configured access-token files or environment sources when providers refresh, so externally refreshed access tokens can be picked up. For Claude Code credentials, a usage `401` triggers one refresh-token exchange, an atomic credential-file update, and one retry; the file and its directory must be writable. Other OAuth grants remain owned by Codex, Pi, or their configured credential source.

Upgrade the central daemon before require-daemon clients. During version skew, a client that requests an endpoint the older daemon lacks fails closed under `require-daemon`; it does not compensate by polling providers locally.

Finally, `usagent` observes account-scoped usage. It does not reserve quota, coordinate callers, or prevent simultaneous model requests from consuming the same account limits.

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
- `GET /v1/chatgpt/reset-once`
- `POST /v1/chatgpt/reset-once/arm`
- `POST /v1/chatgpt/reset-once/cancel`

`/v1/config/raw` is intentionally not exposed. The usage/provider endpoints read the current cached snapshot; provider APIs are called only by the refresh coordinator. `GET /v1/usage/analysis` is cached-only: it derives per-item percent remaining, reset/window timing, inferred pace, pressure, projected exhaustion, confidence, and caveats from the current `/v1/usage` snapshot without refreshing providers. `GET /v1/expiring-usage` is also cached-only: it derives likely "use it or lose it" quota opportunities from the current `/v1/usage` snapshot and supports `within`/`withinMs`, `minimumRemainingPercent`, `providers`, `tiers`/`tier`, `tags`/`tag`, and `includeLowConfidence` query filters. Tier filters match provider or model tiers; tag filters use any matching provider/model tag. `GET /v1/recommendations/provider` supports the same cached-only `providers`, `tiers`/`tier`, `tags`/`tag`, `minimumRemainingPercent`, and `unit` filters. Expiring-usage responses separate raw reset-bound unused quota from actionable waste: `rawEstimatedWastedAmount`/`rawEstimatedWastedPercent` show the simple amount likely to reset unused, while `actionableWasteAmount`/`actionableWastePercent` are adjusted by history and overlapping parent windows. The legacy `estimatedWastedAmount` fields remain as raw estimated unused quota for compatibility, but opportunity scoring and CLI wording use actionable waste when available. For example, a 5h ChatGPT bucket may show 75% raw expiring unused, but only 18.75% actionable waste when a fresh weekly/monthly parent has enough future 5h resets to satisfy forecast demand. `GET /v1/resets` returns a reset calendar sorted by reset time, and `GET /v1/availability` returns conservative provider-level availability with blockers, constraints, and next likely improvement times. See [`docs/RATE_LIMITING.md`](docs/RATE_LIMITING.md) for provider polling minimums and retry-header handling, and [`docs/CHATGPT_RESET_CREDITS.md`](docs/CHATGPT_RESET_CREDITS.md) for ChatGPT/Codex reset banking details.

## Config

See `config.example.yaml`. It is real YAML. Paths beginning with `~` are expanded at runtime, and `%STATE%` expands to `$XDG_STATE_HOME` or `~/.local/state`.

## Secrets

Secrets must be provided by runtime environment variables or secret files. Do not put tokens in config, Docker images, Nix store paths, or checked-in files.

Claude Code/Fable usage is fetched from the Claude Code OAuth usage endpoint using the local Claude Code credentials file path from config. The access token is read from `claudeAiOauth.accessToken` at refresh time and is never returned in HTTP responses. On `401`, usagent uses `claudeAiOauth.refreshToken`, coordinates with Claude Code's refresh locks, atomically persists rotated tokens, and retries once. Read-only credential mounts can still report cached usage but cannot auto-refresh. When the endpoint includes `extra_usage`, usagent exposes enabled Extra Credits as a monthly currency quota item, converting the API's cent values to dollars/euros/etc.

ChatGPT Pro/Codex subscription usage is fetched from ChatGPT's private `/backend-api/wham/usage` endpoint when `providers.chatgpt.enabled=true`. By default usagent reads the Codex CLI OAuth login from `~/.codex/auth.json`. The configured `authPath` also accepts Pi's `~/.pi/agent/auth.json` format and reads its `openai-codex` OAuth entry, so Pi users can point usagent at the credentials Pi automatically refreshes. Alternatively set `CHATGPT_ACCESS_TOKEN` and optionally `CHATGPT_ACCOUNT_ID`. Reset banking is represented in normal stats as `chatgpt-rate-limit-reset-credits`; detailed banked reset-credit records are available from `GET /v1/chatgpt/reset-credits`. See [`docs/CHATGPT_RESET_CREDITS.md`](docs/CHATGPT_RESET_CREDITS.md) for source references, response fields, service-to-service examples, and redemption safety notes.

Redeeming a reset credit is intentionally gated. It is disabled unless `providers.chatgpt.allowResetConsume=true`, and callers must use JSON, the confirmation header, and a confirmation body. Because enabling it exposes a mutating endpoint that spends a real credit, keep it false on a broadly reachable central listener until a later security design exists:

```sh
curl -fsS -X POST http://127.0.0.1:8788/v1/chatgpt/reset-credits/consume \
  -H 'content-type: application/json' \
  -H 'x-usagent-action: consume-chatgpt-reset-credit' \
  -d '{"creditId":"RateLimitResetCredit_...","confirm":"consume-chatgpt-reset-credit"}'
```

This endpoint calls OpenAI's `/backend-api/wham/rate-limit-reset-credits/consume` endpoint and spends a real banked reset credit.

To spend one credit automatically when the account-wide weekly quota hits zero, arm a one-shot toggle instead of enabling `allowResetConsume`:

```sh
usagent reset-once arm
```

The running loopback-only daemon consumes the earliest-expiring eligible credit on the next fresh exhausted weekly refresh, then disarms. See [`docs/CHATGPT_RESET_CREDITS.md`](docs/CHATGPT_RESET_CREDITS.md).

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

Anthropic Console/API prepaid credits are separate from Claude subscription extra usage. Enable `providers.anthropic` only for a real Anthropic organization account with Admin API access; personal/individual Console organizations are not supported. It uses a configurable balance/cost checkpoint and a runtime `ANTHROPIC_ADMIN_KEY` (or `apiKeyFile`) to show spending since the checkpoint and **estimated remaining USD**, automatically adding itself to the usage view. No balance is assumed or hard-coded. See [`docs/ANTHROPIC_CREDITS.md`](docs/ANTHROPIC_CREDITS.md) for key setup, capturing the daily-UTC spending baseline without double-counting, and reconciling purchases or credit adjustments.

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

ChatGPT, OpenAI API costs, Anthropic API credit estimates, z.ai, and configured custom providers are real pull providers when enabled; disabled providers can still appear as metadata-only entries via `usageView.providers`.
