# usagent

Standalone Go usage/quota microservice for agent providers.

`usagent` refreshes provider usage for Claude Code/Fable, OpenAI, and z.ai through a cache coordinator, normalizes the current snapshot to schema version 2, and exposes it over HTTP for Noctalia or any other client.

## Commands

```sh
devenv shell
check                 # go test ./...
start                 # go run ./cmd/usagent serve --config config.example.yaml
usage                 # go run ./cmd/usagent usage --config config.example.yaml
```

Without devenv:

```sh
go test ./...
go run ./cmd/usagent serve --config config.example.yaml --host 127.0.0.1 --port 8787
go run ./cmd/usagent usage --config config.example.yaml
```

## CLI usage summary

`usagent` or `usagent usage` prints remaining quota for all providers in the configured usage view. It first calls the running daemon's `/v1/usage` endpoint; if the daemon is unavailable, it loads the same config/state and refreshes due providers locally. Use `usagent serve` to run the daemon.

```sh
usagent
usagent usage --config ~/.config/usagent/config.yaml
usagent usage --json
usagent usage --offline  # skip daemon lookup and refresh/read locally
```

## Endpoints

- `GET /healthz`
- `GET /readyz`
- `GET /v1/usage`
- `GET /v1/providers`

`/v1/config/raw` is intentionally not exposed. The usage/provider endpoints read the current cached snapshot; provider APIs are called only by the refresh coordinator.

## Config

See `config.example.yaml`. It is real YAML. Paths beginning with `~` are expanded at runtime, and `%STATE%` expands to `$XDG_STATE_HOME` or `~/.local/state`.

## Secrets

Secrets must be provided by runtime environment variables or secret files. Do not put tokens in config, Docker images, Nix store paths, or checked-in files.

Claude Code/Fable usage is fetched from the Claude Code OAuth usage endpoint using the local Claude Code credentials file path from config. The access token is read from `claudeAiOauth.accessToken` at refresh time and is never returned in HTTP responses.

OpenAI usage is fetched from the Admin Costs API when `providers.openai.enabled=true`. Provide an admin key in `OPENAI_ADMIN_KEY` (or the configured `apiKeyEnv`) and define USD budgets:

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
  providers: ["claude-code", "openai", "z-ai", "my-provider"]
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
Usage: Claude S:100% W:50% F:24% [2h14m] · OpenAI W:50% M:70% · z.ai S:90% W:97%
```

OpenAI, z.ai, and configured custom providers are real pull providers when enabled; disabled providers can still appear as metadata-only entries via `usageView.providers`.
