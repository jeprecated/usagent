{ pkgs, ... }:

{
  packages = [
    pkgs.go
    pkgs.jujutsu
    pkgs.jq
    pkgs.curl
    pkgs.python3
    pkgs.docker-client
  ];

  scripts.start.exec = "go run ./cmd/usagent serve --config config.example.yaml --host 127.0.0.1 --port 8787";
  scripts.dev.exec = "go run ./cmd/usagent serve --config config.example.yaml --host 127.0.0.1 --port 8787";
  scripts.check.exec = "go test ./...";
  scripts.usage.exec = "go run ./cmd/usagent usage --config config.example.yaml --host 127.0.0.1 --port 8787";

  enterTest = ''
    go test ./...
    port=$(python3 - <<'PY'
import socket
s=socket.socket()
s.bind(('127.0.0.1', 0))
print(s.getsockname()[1])
s.close()
PY
)
    XDG_STATE_HOME=$(mktemp -d) go run ./cmd/usagent serve --config config.example.yaml --host 127.0.0.1 --port "$port" > /tmp/usagent-test.log 2>&1 &
    pid=$!
    trap 'kill $pid 2>/dev/null || true' EXIT
    for i in $(seq 1 50); do
      if curl -fsS "http://127.0.0.1:$port/healthz" >/dev/null; then
        curl -fsS "http://127.0.0.1:$port/v1/usage" | jq -e '.schemaVersion == 2 and .service == "usagent"' >/dev/null
        exit 0
      fi
      sleep 0.2
    done
    cat /tmp/usagent-test.log >&2 || true
    exit 1
  '';
}
