# usagent

Standalone Go usage/quota microservice for agent providers.

`usagent` refreshes provider usage for Claude Code/Fable, OpenAI, and z.ai through a cache coordinator, normalizes the current snapshot to schema version 2, and exposes it over HTTP for Noctalia or any other client.

## Commands

```sh
devenv shell
check                 # go test ./...
start                 # go run ./cmd/usagent --config config.example.yaml
```

Without devenv:

```sh
go test ./...
go run ./cmd/usagent --config config.example.yaml --host 127.0.0.1 --port 8787
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

OpenAI and z.ai are currently stable metadata placeholders with no quota items until their provider fetchers are implemented.
