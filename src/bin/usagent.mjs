#!/usr/bin/env node
import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { existsSync } from "node:fs";

const configPath = process.env.USAGENT_CONFIG ?? "config.example.yaml";
const port = Number(process.env.USAGENT_PORT ?? 8787);
const host = process.env.USAGENT_HOST ?? "127.0.0.1";

const startedAt = Date.now();
let lastClaudeIngest;
let configCache;
let configCacheLoadedAt = 0;

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

async function loadJson(path) {
  try {
    return JSON.parse(await readFile(path, "utf8"));
  } catch {
    return undefined;
  }
}

async function loadConfig() {
  const now = Date.now();
  if (configCache && now - configCacheLoadedAt < 2000) return configCache;
  configCache = (await loadJson(configPath)) ?? {};
  configCacheLoadedAt = now;
  return configCache;
}

function providerLabel(provider) {
  if (provider === "claude-code") return "Claude";
  if (provider === "openai") return "OpenAI";
  if (provider === "z-ai") return "z.ai";
  return provider;
}

function providerState(provider, items) {
  const providerItems = items.filter((item) => item.provider === provider);
  if (providerItems.length === 0) return "stale";
  if (providerItems.some((item) => item.state === "error" || item.severity === "error")) return "error";
  if (providerItems.some((item) => item.state === "stale")) return "stale";
  return "fresh";
}

async function legacyQuotaItems(config) {
  const path = config?.legacySources?.piSessionMonitorOverviewFile;
  if (!path) return [];
  const overview = await loadJson(path);
  return Array.isArray(overview?.quotaItems) ? overview.quotaItems : [];
}

async function usageOverview() {
  const generatedAt = Date.now();
  const config = await loadConfig();
  const quotaItems = await legacyQuotaItems(config);
  const providerIds = config?.usageView?.providers ?? ["claude-code", "openai", "z-ai"];
  const providers = providerIds.map((id) => ({
    id,
    label: providerLabel(id),
    state: providerState(id, quotaItems),
    source: id === "claude-code" ? "push" : "pull",
    lastUpdatedAt: quotaItems.filter((item) => item.provider === id).map((item) => item.refresh?.lastUpdatedAt).filter(Number.isFinite).sort((a, b) => b - a)[0],
  }));

  if (lastClaudeIngest && !quotaItems.some((item) => item.provider === "claude-code")) {
    providers.unshift({ id: "claude-code", label: "Claude", state: "fresh", source: "push", lastUpdatedAt: lastClaudeIngest.receivedAt });
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
    if (req.method === "GET" && url.pathname === "/v1/usage") return json(res, 200, await usageOverview());
    if (req.method === "GET" && url.pathname === "/v1/providers") return json(res, 200, { providers: (await usageOverview()).providers });
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
