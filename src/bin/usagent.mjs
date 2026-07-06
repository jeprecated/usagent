#!/usr/bin/env node
import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { existsSync } from "node:fs";

const configPath = process.env.USAGENT_CONFIG ?? "config.example.yaml";
const port = Number(process.env.USAGENT_PORT ?? 8787);
const host = process.env.USAGENT_HOST ?? "127.0.0.1";

const startedAt = Date.now();
let lastClaudeIngest;

function json(res, status, body) {
  const payload = JSON.stringify(body, null, 2);
  res.writeHead(status, {
    "content-type": "application/json; charset=utf-8",
    "cache-control": "no-store",
  });
  res.end(payload);
}

function notFound(res) {
  json(res, 404, { error: "not_found" });
}

function usageOverview() {
  const generatedAt = Date.now();
  const quotaItems = [];
  const providers = [
    { id: "claude-code", label: "Claude", state: lastClaudeIngest ? "fresh" : "stale", source: "push", lastUpdatedAt: lastClaudeIngest?.receivedAt },
    { id: "openai", label: "OpenAI", state: "stale", source: "pull" },
    { id: "z-ai", label: "z.ai", state: "stale", source: "pull" },
  ];

  if (lastClaudeIngest) {
    quotaItems.push({
      id: "claude-code-ingest-present",
      provider: "claude-code",
      label: "Claude statusline received",
      window: { id: "statusline", label: "statusline", kind: "custom" },
      state: "fresh",
      severity: "ok",
      visible: true,
      refresh: { lastUpdatedAt: lastClaudeIngest.receivedAt, source: "push" },
    });
  }

  return {
    schemaVersion: 2,
    service: "usagent",
    generatedAt,
    startedAt,
    stale: quotaItems.length === 0,
    providers,
    quotaItems,
  };
}

async function readBody(req, limit = 1024 * 1024) {
  let size = 0;
  const chunks = [];
  for await (const chunk of req) {
    size += chunk.length;
    if (size > limit) throw new Error("request_too_large");
    chunks.push(chunk);
  }
  return Buffer.concat(chunks).toString("utf8");
}

const server = createServer(async (req, res) => {
  try {
    const url = new URL(req.url ?? "/", `http://${req.headers.host ?? "localhost"}`);
    if (req.method === "GET" && url.pathname === "/healthz") return json(res, 200, { ok: true, service: "usagent" });
    if (req.method === "GET" && url.pathname === "/readyz") return json(res, 200, { ready: true, configPath, configExists: existsSync(configPath) });
    if (req.method === "GET" && url.pathname === "/v1/usage") return json(res, 200, usageOverview());
    if (req.method === "GET" && url.pathname === "/v1/providers") return json(res, 200, { providers: usageOverview().providers });
    if (req.method === "GET" && url.pathname === "/v1/config/raw") {
      const text = await readFile(configPath, "utf8").catch(() => "");
      return json(res, text ? 200 : 404, { configPath, text });
    }
    if (req.method === "POST" && url.pathname === "/v1/ingest/claude-code") {
      const body = await readBody(req);
      lastClaudeIngest = { receivedAt: Date.now(), bytes: Buffer.byteLength(body) };
      return json(res, 202, { accepted: true, provider: "claude-code", receivedAt: lastClaudeIngest.receivedAt });
    }
    return notFound(res);
  } catch (error) {
    return json(res, 500, { error: "internal_error", message: error instanceof Error ? error.message : String(error) });
  }
});

server.listen(port, host, () => {
  console.log(JSON.stringify({ level: "info", msg: "usagent listening", host, port, configPath }));
});
