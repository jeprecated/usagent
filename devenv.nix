{ pkgs, ... }:

{
  packages = [
    pkgs.nodejs_24
    pkgs.jujutsu
    pkgs.jq
    pkgs.curl
    pkgs.docker-client
  ];

  env.USAGENT_CONFIG = "config.example.yaml";
  env.USAGENT_HOST = "127.0.0.1";
  env.USAGENT_PORT = "8787";

  scripts.start.exec = "node src/bin/usagent.mjs";
  scripts.dev.exec = "node --watch src/bin/usagent.mjs";
  scripts.check.exec = "npm run check";
  scripts.usage.exec = "curl -fsS http://127.0.0.1:8787/v1/usage | jq .";

  enterTest = ''
    npm run check
    port=$(node -e 'const net=require("node:net"); const s=net.createServer(); s.listen(0,"127.0.0.1",()=>{console.log(s.address().port); s.close();});')
    USAGENT_PORT=$port node src/bin/usagent.mjs > /tmp/usagent-test.log 2>&1 &
    pid=$!
    trap 'kill $pid 2>/dev/null || true' EXIT
    for i in $(seq 1 30); do
      if curl -fsS http://127.0.0.1:$port/healthz >/dev/null; then
        curl -fsS http://127.0.0.1:$port/v1/usage | jq -e '.schemaVersion == 2 and .service == "usagent"' >/dev/null
        exit 0
      fi
      sleep 0.2
    done
    cat /tmp/usagent-test.log >&2 || true
    exit 1
  '';
}
