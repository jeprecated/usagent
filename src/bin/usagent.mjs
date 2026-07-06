#!/usr/bin/env node
import { createServer } from "node:http";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { dirname } from "node:path";

const configPath = process.env.USAGENT_CONFIG ?? "config.example.yaml";
const port = Number(process.env.USAGENT_PORT ?? 8787);
const host = process.env.USAGENT_HOST ?? "127.0.0.1";

const startedAt = Date.now();
let lastClaudeIngest;
let configCache;
let configCacheLoadedAt = 0;
let claudeOAuthCache = { items: [], nextRefreshAt: 0, staleAt: 0, lastUpdatedAt: 0 };
let stateCacheLoaded = false;

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

async function loadStateCache(config) {
  if (stateCacheLoaded) return;
  stateCacheLoaded = true;
  const statePath = config?.server?.statePath;
  if (!statePath) return;
  const state = await loadJson(statePath);
  const cached = state?.claudeOAuthCache;
  if (Array.isArray(cached?.items) && cached.items.length > 0) {
    claudeOAuthCache = {
      items: cached.items,
      nextRefreshAt: Number(cached.nextRefreshAt) || 0,
      staleAt: Number(cached.staleAt) || 0,
      lastUpdatedAt: Number(cached.lastUpdatedAt) || 0,
    };
  }
}

async function saveStateCache(config) {
  const statePath = config?.server?.statePath;
  if (!statePath) return;
  const state = {
    schemaVersion: 1,
    updatedAt: Date.now(),
    claudeOAuthCache,
  };
  await mkdir(dirname(statePath), { recursive: true });
  await writeFile(statePath, JSON.stringify(state, null, 2));
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

function expandHome(path) {
  if (typeof path !== "string") return path;
  if (path === "~") return homedir();
  if (path.startsWith("~/")) return `${homedir()}/${path.slice(2)}`;
  return path;
}

function parseResetAt(value) {
  if (typeof value !== "string") return undefined;
  const ms = Date.parse(value);
  return Number.isFinite(ms) ? ms : undefined;
}

function retryAfterMs(response, fallbackMs) {
  const header = response.headers.get("retry-after");
  if (!header) return fallbackMs;
  const seconds = Number(header);
  if (Number.isFinite(seconds)) return Math.max(1000, seconds * 1000);
  const dateMs = Date.parse(header);
  return Number.isFinite(dateMs) ? Math.max(1000, dateMs - Date.now()) : fallbackMs;
}

function cachedClaudeOAuthItems(now) {
  if (!Array.isArray(claudeOAuthCache.items) || claudeOAuthCache.items.length === 0) return [];
  const stale = now >= claudeOAuthCache.staleAt;
  return claudeOAuthCache.items.map((item) => ({
    ...item,
    state: stale ? "stale" : item.state,
    severity: stale && item.severity === "ok" ? "warning" : item.severity,
    refresh: {
      ...(item.refresh ?? {}),
      nextRefreshAt: claudeOAuthCache.nextRefreshAt,
      staleAt: claudeOAuthCache.staleAt,
    },
    ...(stale ? { error: { provider: "claude-code", code: "oauth-refresh-failed", message: "Using cached Claude OAuth usage after refresh failure", lastOccurredAt: now, recoverable: true } } : {}),
  }));
}

function claudeOAuthItem({ id, label, windowId, windowLabel, windowKind, usedPercent, resetAt, severity, lastUpdatedAt, nextRefreshAt, staleAt }) {
  const used = Math.max(0, Math.min(100, Math.round(Number(usedPercent) || 0)));
  const remaining = Math.max(0, 100 - used);
  return {
    id,
    provider: "claude-code",
    label,
    window: {
      id: windowId,
      label: windowLabel,
      kind: windowKind,
      ...(Number.isFinite(resetAt) ? { resetAt } : {}),
    },
    unit: "percent",
    state: "fresh",
    severity: severity === "critical" || severity === "error" || severity === "warning" ? severity : "ok",
    visible: true,
    refresh: {
      lastUpdatedAt,
      source: "provider",
      nextRefreshAt,
      staleAt,
    },
    ...(Number.isFinite(resetAt) ? { reset: { resetAt, resetWindowId: windowId, source: "provider" } } : {}),
    limit: 100,
    used,
    remaining,
    percentUsed: used,
  };
}

async function claudeOAuthQuotaItems(config, generatedAt) {
  const providerConfig = config?.providers?.claudeOAuth ?? config?.providers?.claudeCodeOAuth;
  if (!providerConfig?.enabled) return [];
  await loadStateCache(config);
  const refreshMs = Number(providerConfig.refreshMs ?? config?.quota?.refreshMs ?? 5 * 60 * 1000);
  const staleMs = Number(providerConfig.staleMs ?? Math.max(refreshMs * 3, 15 * 60 * 1000));
  if (generatedAt < claudeOAuthCache.nextRefreshAt) return cachedClaudeOAuthItems(generatedAt);

  const credentialsPath = expandHome(providerConfig.credentialsPath ?? "~/.claude/.credentials.json");
  const endpointUrl = providerConfig.endpointUrl ?? "https://api.anthropic.com/api/oauth/usage";
  const credentials = await loadJson(credentialsPath);
  const token = credentials?.claudeAiOauth?.accessToken;
  if (!token) return cachedClaudeOAuthItems(generatedAt);

  const response = await fetch(endpointUrl, {
    headers: {
      authorization: `Bearer ${token}`,
      "anthropic-beta": providerConfig.betaHeader ?? "oauth-2025-04-20",
      accept: "application/json",
    },
  });
  if (!response.ok) {
    claudeOAuthCache.nextRefreshAt = generatedAt + retryAfterMs(response, refreshMs);
    return cachedClaudeOAuthItems(generatedAt);
  }

  const payload = await response.json();
  const limits = Array.isArray(payload?.limits) ? payload.limits : [];
  const nextRefreshAt = generatedAt + refreshMs;
  const staleAt = generatedAt + staleMs;
  const items = [];

  const session = limits.find((limit) => limit?.kind === "session") ?? payload?.five_hour;
  if (session) {
    items.push(claudeOAuthItem({
      id: "claude-code-oauth-session",
      label: "Claude 5h",
      windowId: "5h",
      windowLabel: "5h",
      windowKind: "rolling",
      usedPercent: session.percent ?? session.utilization,
      resetAt: parseResetAt(session.resets_at),
      severity: session.severity,
      lastUpdatedAt: generatedAt,
      nextRefreshAt,
      staleAt,
    }));
  }

  const weeklyAll = limits.find((limit) => limit?.kind === "weekly_all") ?? payload?.seven_day;
  if (weeklyAll) {
    items.push(claudeOAuthItem({
      id: "claude-code-oauth-weekly-all",
      label: "Claude weekly",
      windowId: "7d",
      windowLabel: "7d",
      windowKind: "weekly",
      usedPercent: weeklyAll.percent ?? weeklyAll.utilization,
      resetAt: parseResetAt(weeklyAll.resets_at),
      severity: weeklyAll.severity,
      lastUpdatedAt: generatedAt,
      nextRefreshAt,
      staleAt,
    }));
  }

  const fableWeekly = limits.find((limit) =>
    limit?.kind === "weekly_scoped" &&
    String(limit?.scope?.model?.display_name ?? limit?.scope?.model?.id ?? "").toLowerCase().includes("fable")
  );
  if (fableWeekly) {
    items.push(claudeOAuthItem({
      id: "claude-code-oauth-fable-weekly",
      label: "Claude Fable weekly",
      windowId: "fable-weekly",
      windowLabel: "Fable weekly",
      windowKind: "weekly",
      usedPercent: fableWeekly.percent,
      resetAt: parseResetAt(fableWeekly.resets_at),
      severity: fableWeekly.severity,
      lastUpdatedAt: generatedAt,
      nextRefreshAt,
      staleAt,
    }));
  }

  if (items.length > 0) {
    claudeOAuthCache = { items, nextRefreshAt, staleAt, lastUpdatedAt: generatedAt };
    await saveStateCache(config).catch(() => {});
  }
  return cachedClaudeOAuthItems(generatedAt);
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
  const legacyItems = await legacyQuotaItems(config);
  const claudeOAuthItems = await claudeOAuthQuotaItems(config, generatedAt).catch(() => []);
  const quotaItems = claudeOAuthItems.length > 0
    ? [...legacyItems.filter((item) => item.provider !== "claude-code"), ...claudeOAuthItems]
    : legacyItems;
  const providerIds = config?.usageView?.providers ?? ["claude-code", "openai", "z-ai"];
  const providers = providerIds.map((id) => ({
    id,
    label: providerLabel(id),
    state: providerState(id, quotaItems),
    source: id === "claude-code" && claudeOAuthItems.length === 0 ? "push" : "pull",
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
