import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import { createRelay } from "./server.mjs";

// Fake Anthropic upstream: records the last request, answers JSON or SSE by model name.
let upstreamSeen = null;
const upstream = http.createServer(async (req, res) => {
  const chunks = [];
  for await (const c of req) chunks.push(c);
  const body = JSON.parse(Buffer.concat(chunks).toString());
  upstreamSeen = { headers: req.headers, body, url: req.url, method: req.method };

  if (body.stream) {
    res.writeHead(200, { "content-type": "text/event-stream", "request-id": "req_stream" });
    res.write('event: message_start\ndata: {"type":"message_start","message":{"usage":{"input_tokens":12,"output_tokens":0}}}\n\n');
    res.write('event: content_block_delta\ndata: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}\n\n');
    res.write('event: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}\n\n');
    res.end("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n");
    return;
  }
  res.writeHead(200, { "content-type": "application/json", "request-id": "req_json" });
  res.end(JSON.stringify({
    id: "msg_1", type: "message", role: "assistant", model: body.model,
    content: [{ type: "text", text: "ok" }], stop_reason: "end_turn",
    usage: { input_tokens: 10, output_tokens: 2 },
  }));
});

const logs = [];
let relay;
let relayURL;

before(async () => {
  await new Promise((r) => upstream.listen(0, "127.0.0.1", r));
  relay = createRelay({
    apiKey: "sk-ant-test",
    appTokens: ["token-a", "token-b"],
    allowedModels: ["claude-opus-5"],
    upstream: `http://127.0.0.1:${upstream.address().port}`,
    maxBodyBytes: 2048,
    log: (r) => logs.push(r),
  });
  await new Promise((r) => relay.listen(0, "127.0.0.1", r));
  relayURL = `http://127.0.0.1:${relay.address().port}`;
});

after(async () => {
  await new Promise((r) => relay.close(r));
  await new Promise((r) => upstream.close(r));
});

const post = (body, headers = {}, path = "/v1/messages") =>
  fetch(`${relayURL}${path}`, {
    method: "POST",
    headers: { "content-type": "application/json", authorization: "Bearer token-b", ...headers },
    body: typeof body === "string" ? body : JSON.stringify(body),
  });

test("health endpoint does not require auth", async () => {
  const res = await fetch(`${relayURL}/healthz`);
  assert.equal(res.status, 200);
});

test("rejects missing and wrong app tokens", async () => {
  const missing = await post({ model: "claude-opus-5" }, { authorization: "" });
  assert.equal(missing.status, 401);
  const wrong = await post({ model: "claude-opus-5" }, { authorization: "Bearer token-x" });
  assert.equal(wrong.status, 401);
});

test("rejects paths other than /v1/messages", async () => {
  const res = await post({ model: "claude-opus-5" }, {}, "/v1/complete");
  assert.equal(res.status, 404);
});

test("rejects models outside the allowlist", async () => {
  const res = await post({ model: "claude-haiku-4-5", messages: [] });
  assert.equal(res.status, 400);
  const body = await res.json();
  assert.equal(body.error.type, "invalid_request_error");
});

test("rejects oversized bodies", async () => {
  const res = await post({ model: "claude-opus-5", pad: "x".repeat(4096) });
  assert.equal(res.status, 413);
});

test("forwards allowed requests with the workspace key and strips inbound credentials", async () => {
  const res = await post(
    { model: "claude-opus-5", max_tokens: 16, messages: [{ role: "user", content: "hi" }] },
    { "x-api-key": "sk-ant-from-client", "anthropic-beta": "structured-outputs-2025-11-13", "x-custom": "dropped" },
  );
  assert.equal(res.status, 200);
  assert.equal(res.headers.get("request-id"), "req_json");
  const body = await res.json();
  assert.equal(body.content[0].text, "ok");

  assert.equal(upstreamSeen.url, "/v1/messages");
  assert.equal(upstreamSeen.headers["x-api-key"], "sk-ant-test");
  assert.equal(upstreamSeen.headers["anthropic-version"], "2023-06-01");
  assert.equal(upstreamSeen.headers["anthropic-beta"], "structured-outputs-2025-11-13");
  assert.equal(upstreamSeen.headers["authorization"], undefined);
  assert.equal(upstreamSeen.headers["x-custom"], undefined);

  const record = logs.findLast((r) => r.event === "relay" && r.request_id === "req_json");
  assert.deepEqual(record.usage, { input_tokens: 10, output_tokens: 2 });
  assert.equal(record.stop_reason, "end_turn");
  assert.equal(JSON.stringify(record).includes("hi"), false, "log must not contain prompt content");
});

test("streams SSE responses through unchanged and records usage", async () => {
  const res = await post({ model: "claude-opus-5", max_tokens: 16, stream: true, messages: [{ role: "user", content: "hi" }] });
  assert.equal(res.status, 200);
  assert.equal(res.headers.get("content-type"), "text/event-stream");
  const text = await res.text();
  assert.match(text, /event: message_start/);
  assert.match(text, /"text":"hi"/);
  assert.match(text, /event: message_stop/);

  const record = logs.findLast((r) => r.event === "relay" && r.request_id === "req_stream");
  assert.equal(record.stream, true);
  assert.deepEqual(record.usage, { output_tokens: 7 });
});
