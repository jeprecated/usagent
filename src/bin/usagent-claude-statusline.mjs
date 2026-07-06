#!/usr/bin/env node
const ingestUrl = process.env.USAGENT_INGEST_URL ?? "http://127.0.0.1:8787/v1/ingest/claude-code";
const token = process.env.USAGENT_INGEST_TOKEN;
const passthroughCommand = process.env.USAGENT_CLAUDE_STATUSLINE_COMMAND;

async function readStdin(limit = 1024 * 1024) {
  let size = 0;
  const chunks = [];
  for await (const chunk of process.stdin) {
    size += chunk.length;
    if (size > limit) throw new Error("statusline_payload_too_large");
    chunks.push(chunk);
  }
  return Buffer.concat(chunks).toString("utf8");
}

async function postPayload(payload) {
  const headers = { "content-type": "application/json" };
  if (token) headers.authorization = `Bearer ${token}`;
  const response = await fetch(ingestUrl, { method: "POST", headers, body: payload });
  if (!response.ok) throw new Error(`ingest_failed_${response.status}`);
}

const payload = await readStdin();
try {
  if (payload.trim() !== "") await postPayload(payload);
} catch {
  // Statusline ingestion must never break Claude Code. Intentionally silent.
}

if (passthroughCommand) {
  const { spawnSync } = await import("node:child_process");
  const result = spawnSync(passthroughCommand, { shell: true, input: payload, encoding: "utf8" });
  if (result.stdout) process.stdout.write(result.stdout);
  process.exit(result.status ?? 0);
}
