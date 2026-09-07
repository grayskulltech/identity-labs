import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { createMCPStub, PROTOCOL_VERSION } from "./mcp-gateway-stub.mjs";

let server;
let url;
const TOKEN = "stub-token";

before(async () => {
  server = createMCPStub({ bearerToken: TOKEN });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  url = `http://127.0.0.1:${server.address().port}/mcp`;
});

after(async () => {
  await new Promise((r) => server.close(r));
});

const rpc = (body, extraHeaders = {}) =>
  fetch(url, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      accept: "application/json, text/event-stream",
      "mcp-protocol-version": PROTOCOL_VERSION,
      authorization: `Bearer ${TOKEN}`,
      ...extraHeaders,
    },
    body: JSON.stringify({ jsonrpc: "2.0", ...body }),
  });

async function initialize() {
  const res = await rpc({ id: 1, method: "initialize", params: { protocolVersion: PROTOCOL_VERSION, capabilities: {}, clientInfo: { name: "t", version: "0" } } });
  assert.equal(res.status, 200);
  const session = res.headers.get("mcp-session-id");
  assert.ok(session, "initialize must assign a session id");
  const body = await res.json();
  assert.equal(body.result.protocolVersion, PROTOCOL_VERSION);
  return session;
}

test("rejects missing bearer token", async () => {
  const res = await rpc({ id: 1, method: "initialize", params: {} }, { authorization: "" });
  assert.equal(res.status, 401);
});

test("rejects requests without a known session after initialize", async () => {
  const res = await rpc({ id: 2, method: "tools/list", params: {} });
  assert.equal(res.status, 404);
});

test("initialized notification returns 202 and tools/list works", async () => {
  const session = await initialize();
  const note = await rpc({ method: "notifications/initialized" }, { "mcp-session-id": session });
  assert.equal(note.status, 202);

  const list = await rpc({ id: 3, method: "tools/list", params: {} }, { "mcp-session-id": session });
  assert.equal(list.status, 200);
  const body = await list.json();
  const names = body.result.tools.map((t) => t.name);
  assert.deepEqual(names, ["lookup_signins", "revoke_sessions"]);
  assert.equal(body.result.tools[0].annotations.readOnlyHint, true);
  assert.equal(body.result.tools[1].annotations.destructiveHint, true);
  assert.equal(body.result.tools[0].handler, undefined, "handlers must not leak to clients");
});

test("tools/call runs a tool and reports unknown tools as JSON-RPC errors", async () => {
  const session = await initialize();
  const call = await rpc(
    { id: 4, method: "tools/call", params: { name: "lookup_signins", arguments: { subject: "a@example.com", limit: 2 } } },
    { "mcp-session-id": session },
  );
  const body = await call.json();
  assert.equal(body.result.isError, false);
  assert.equal(body.result.structuredContent.events.length, 2);
  assert.equal(body.result.structuredContent.events[0].subject, "a@example.com");

  const unknown = await rpc({ id: 5, method: "tools/call", params: { name: "nope", arguments: {} } }, { "mcp-session-id": session });
  const err = await unknown.json();
  assert.equal(err.error.code, -32602);
});

test("DELETE ends the session", async () => {
  const session = await initialize();
  const del = await fetch(url, { method: "DELETE", headers: { authorization: `Bearer ${TOKEN}`, "mcp-session-id": session } });
  assert.equal(del.status, 204);
  const after = await rpc({ id: 6, method: "tools/list", params: {} }, { "mcp-session-id": session });
  assert.equal(after.status, 404);
});
