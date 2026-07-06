# usagent

Standalone agent usage/quota microservice.

`usagent` polls or ingests provider usage for Claude Code/Fable, OpenAI, and z.ai, normalizes it to a stable JSON model, and exposes it over HTTP for Noctalia or any other client.

## Target bar UI

The existing Pi session tracker remains separate:

```text
Pi: ● 1 busy · 16 idle · 56 stale · 52 done/1d · AG 3
```

A separate usage widget consumes `usagent`:

```text
Usage: Claude S:100% W:50% F:24% R:134m · OpenAI W:50% M:70% · z.ai S:90% W:97%
```

No z.ai web-search quota in the bar.

## Commands

```sh
devenv shell
check
start
```

## Endpoints

- `GET /healthz`
- `GET /readyz`
- `GET /v1/usage`
- `GET /v1/providers`
- `POST /v1/ingest/claude-code`

## Config

See `config.example.yaml`.

## Secrets

Secrets must be provided by runtime env vars or secret files. Do not put tokens in config, Docker images, Nix store paths, or checked-in files.
