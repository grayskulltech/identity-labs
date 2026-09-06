// Relay for ClaudeForFoundationModels `.proxied` auth mode.
//
// The Swift package sends standard Messages API requests to `baseURL`. This relay
// authorizes the caller, enforces a model allowlist, attaches the workspace API key,
// and streams the upstream response back unchanged. No dependencies beyond Node 22.

import http from "node:http";
import { timingSafeEqual } from "node:crypto";
import { Readable, Transform } from "node:stream";
import { pipeline } from "node:stream/promises";

const FORWARDED_REQUEST_HEADERS = ["content-type", "accept", "anthropic-version", "anthropic-beta"];
const FORWARDED_RESPONSE_HEADERS = ["content-type", "request-id", "cache-control", "retry-after"];
const DEFAULT_ANTHROPIC_VERSION = "2023-06-01";

export function loadConfigFromEnv(env = process.env) {
  const apiKey = env.ANTHROPIC_API_KEY;
  if (!apiKey) throw new Error("ANTHROPIC_API_KEY is required");
  const appTokens = (env.RELAY_APP_TOKENS ?? "").split(",").map((t) => t.trim()).filter(Boolean);
  if (appTokens.length === 0) throw new Error("RELAY_APP_TOKENS is required (comma-separated)");
  return {
    apiKey,
    appTokens,
    allowedModels: (env.RELAY_ALLOWED_MODELS ?? "claude-opus-5,claude-sonnet-5")
      .split(",").map((m) => m.trim()).filter(Boolean),
    upstream: env.RELAY_UPSTREAM ?? "https://api.anthropic.com",
    maxBodyBytes: Number(env.RELAY_MAX_BODY_BYTES ?? 5 * 1024 * 1024),
    port: Number(env.PORT ?? 8787),
    log: (record) => process.stdout.write(JSON.stringify(record) + "\n"),
  };
}

function constantTimeMatch(candidate, accepted) {
  const c = Buffer.from(candidate);
  return accepted.some((token) => {
    const t = Buffer.from(token);
    return c.length === t.length && timingSafeEqual(c, t);
  });
}

function sendJSON(res, status, body, extraHeaders = {}) {
  res.writeHead(status, { "content-type": "application/json", ...extraHeaders });
  res.end(JSON.stringify(body));
}

function apiError(res, status, type, message) {
  sendJSON(res, status, { type: "error", error: { type, message } });
}

async function readBody(req, maxBytes) {
  const chunks = [];
  let size = 0;
  for await (const chunk of req) {
    size += chunk.length;
    if (size > maxBytes) {
      const err = new Error("body too large");
      err.code = "BODY_TOO_LARGE";
      throw err;
    }
    chunks.push(chunk);
  }
  return Buffer.concat(chunks);
}

// Watches an SSE stream for the final usage report without altering the bytes.
function usageTap(onUsage) {
  let tail = "";
  return new Transform({
    transform(chunk, _enc, cb) {
      tail = (tail + chunk.toString("utf8")).slice(-4096);
      const match = tail.match(/"output_tokens":\s*(\d+)/g);
      if (match) onUsage({ output_tokens: Number(match.at(-1).match(/\d+/)[0]) });
      cb(null, chunk);
    },
  });
}

export function createRelay(config) {
  const { apiKey, appTokens, allowedModels, upstream, maxBodyBytes, log = () => {} } = config;
  const fetchImpl = config.fetch ?? globalThis.fetch;

  return http.createServer(async (req, res) => {
    const startedAt = Date.now();

    if (req.method === "GET" && req.url === "/healthz") {
      return sendJSON(res, 200, { ok: true });
    }
    if (req.method !== "POST" || req.url !== "/v1/messages") {
      return apiError(res, 404, "not_found_error", "Only POST /v1/messages is relayed");
    }

    const auth = req.headers.authorization ?? "";
    const presented = auth.startsWith("Bearer ") ? auth.slice(7) : "";
    if (!presented || !constantTimeMatch(presented, appTokens)) {
      return apiError(res, 401, "authentication_error", "Invalid app token");
    }

    let raw;
    try {
      raw = await readBody(req, maxBodyBytes);
    } catch (err) {
      if (err.code === "BODY_TOO_LARGE") return apiError(res, 413, "request_too_large", "Request body exceeds limit");
      return apiError(res, 400, "invalid_request_error", "Could not read request body");
    }

    let body;
    try {
      body = JSON.parse(raw.toString("utf8"));
    } catch {
      return apiError(res, 400, "invalid_request_error", "Body must be JSON");
    }
    if (typeof body.model !== "string" || !allowedModels.includes(body.model)) {
      return apiError(res, 400, "invalid_request_error", `Model not permitted by relay policy: ${body.model ?? "(none)"}`);
    }

    const headers = { "x-api-key": apiKey, "anthropic-version": DEFAULT_ANTHROPIC_VERSION };
    for (const name of FORWARDED_REQUEST_HEADERS) {
      if (req.headers[name] !== undefined) headers[name] = req.headers[name];
    }

    let upstreamRes;
    try {
      upstreamRes = await fetchImpl(`${upstream}/v1/messages`, { method: "POST", headers, body: raw });
    } catch (err) {
      log({ event: "upstream_unreachable", error: String(err?.message ?? err) });
      return apiError(res, 502, "api_error", "Upstream unreachable");
    }

    const responseHeaders = {};
    for (const name of FORWARDED_RESPONSE_HEADERS) {
      const value = upstreamRes.headers.get(name);
      if (value !== null) responseHeaders[name] = value;
    }
    res.writeHead(upstreamRes.status, responseHeaders);

    const record = {
      event: "relay",
      request_id: upstreamRes.headers.get("request-id"),
      model: body.model,
      stream: body.stream === true,
      status: upstreamRes.status,
    };

    if (!upstreamRes.body) {
      res.end();
      log({ ...record, duration_ms: Date.now() - startedAt });
      return;
    }

    if (body.stream === true) {
      try {
        await pipeline(
          Readable.fromWeb(upstreamRes.body),
          usageTap((usage) => Object.assign(record, { usage })),
          res,
        );
      } catch (err) {
        record.error = String(err?.message ?? err);
        res.destroy();
      }
      log({ ...record, duration_ms: Date.now() - startedAt });
      return;
    }

    const text = await upstreamRes.text();
    try {
      const parsed = JSON.parse(text);
      if (parsed.usage) record.usage = parsed.usage;
      if (parsed.stop_reason) record.stop_reason = parsed.stop_reason;
    } catch {
      // Non-JSON upstream body: pass through without usage.
    }
    res.end(text);
    log({ ...record, duration_ms: Date.now() - startedAt });
  });
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const config = loadConfigFromEnv();
  createRelay(config).listen(config.port, () => {
    config.log({ event: "listening", port: config.port, allowed_models: config.allowedModels, upstream: config.upstream });
  });
}
