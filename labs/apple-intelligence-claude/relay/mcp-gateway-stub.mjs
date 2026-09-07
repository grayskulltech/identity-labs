// Minimal MCP gateway stub (Streamable HTTP, protocol 2025-06-18) for exercising
// the Swift MCPBridge client without touching a real gateway. Two tools: one
// read-only, one destructive, so the tool policy paths can be tested end to end.
//
// This is a test double, not a gateway. It answers JSON only (no SSE), keeps
// sessions in memory, and validates a single bearer token.

import http from "node:http";
import { randomUUID, timingSafeEqual } from "node:crypto";

export const PROTOCOL_VERSION = "2025-06-18";

export const defaultTools = [
  {
    name: "lookup_signins",
    title: "Recent sign-ins",
    description: "Returns the most recent sign-in events for a user, newest first.",
    inputSchema: {
      type: "object",
      properties: {
        subject: { type: "string", description: "User principal name or email" },
        limit: { type: "integer", description: "Maximum events to return" },
      },
      required: ["subject"],
    },
    annotations: { readOnlyHint: true, idempotentHint: true },
    handler: ({ subject, limit = 5 }) => {
      const now = Date.now();
      const rows = [
        { minutesAgo: 2, country: "RU", app: "Legacy IMAP", mfa: "none", outcome: "success" },
        { minutesAgo: 9, country: "US", app: "Outlook", mfa: "failure", outcome: "failure" },
        { minutesAgo: 11, country: "US", app: "Outlook", mfa: "failure", outcome: "failure" },
        { minutesAgo: 240, country: "US", app: "Teams", mfa: "success", outcome: "success" },
      ].slice(0, limit).map((r) => ({
        timestamp: new Date(now - r.minutesAgo * 60_000).toISOString(),
        subject, sourceCountry: r.country, clientApp: r.app, mfaResult: r.mfa, outcome: r.outcome,
      }));
      return { content: [{ type: "text", text: JSON.stringify(rows) }], structuredContent: { events: rows } };
    },
  },
  {
    name: "revoke_sessions",
    title: "Revoke sessions",
    description: "Revokes every active session for a user, forcing re-authentication.",
    inputSchema: {
      type: "object",
      properties: { subject: { type: "string", description: "User principal name or email" } },
      required: ["subject"],
    },
    annotations: { readOnlyHint: false, destructiveHint: true, idempotentHint: true },
    handler: ({ subject }) => ({ content: [{ type: "text", text: `Revoked all sessions for ${subject}.` }] }),
  },
];

function tokenMatches(presented, expected) {
  const a = Buffer.from(presented ?? "");
  const b = Buffer.from(expected);
  return a.length === b.length && timingSafeEqual(a, b);
}

function rpcResult(id, result) {
  return JSON.stringify({ jsonrpc: "2.0", id, result });
}

function rpcError(id, code, message) {
  return JSON.stringify({ jsonrpc: "2.0", id: id ?? null, error: { code, message } });
}

export function createMCPStub({ bearerToken, tools = defaultTools, path = "/mcp", log = () => {} }) {
  const sessions = new Set();
  const publicTools = tools.map(({ handler, ...rest }) => rest);

  return http.createServer(async (req, res) => {
    if (req.url !== path) {
      res.writeHead(404, { "content-type": "application/json" });
      return res.end(rpcError(null, -32601, "Unknown endpoint"));
    }

    const auth = req.headers.authorization ?? "";
    const presented = auth.startsWith("Bearer ") ? auth.slice(7) : "";
    if (!tokenMatches(presented, bearerToken)) {
      res.writeHead(401, { "content-type": "application/json" });
      return res.end(rpcError(null, -32001, "Unauthorized"));
    }

    const sessionID = req.headers["mcp-session-id"];

    if (req.method === "DELETE") {
      sessions.delete(sessionID);
      res.writeHead(204);
      return res.end();
    }

    if (req.method !== "POST") {
      res.writeHead(405);
      return res.end();
    }

    const accept = req.headers.accept ?? "";
    if (!accept.includes("application/json")) {
      res.writeHead(406, { "content-type": "application/json" });
      return res.end(rpcError(null, -32600, "Accept must include application/json"));
    }

    const chunks = [];
    for await (const c of req) chunks.push(c);
    let message;
    try {
      message = JSON.parse(Buffer.concat(chunks).toString("utf8"));
    } catch {
      res.writeHead(400, { "content-type": "application/json" });
      return res.end(rpcError(null, -32700, "Parse error"));
    }

    const { id, method, params = {} } = message;
    log({ method, id, session: sessionID ?? null });

    if (method === "initialize") {
      const newSession = randomUUID();
      sessions.add(newSession);
      res.writeHead(200, { "content-type": "application/json", "mcp-session-id": newSession });
      return res.end(rpcResult(id, {
        protocolVersion: PROTOCOL_VERSION,
        capabilities: { tools: { listChanged: false } },
        serverInfo: { name: "mcp-gateway-stub", version: "0.1.0" },
      }));
    }

    if (!sessions.has(sessionID)) {
      res.writeHead(404, { "content-type": "application/json" });
      return res.end(rpcError(id, -32000, "Session not found"));
    }

    if (id === undefined) {
      // Notification. The only one a client sends in this flow is notifications/initialized.
      res.writeHead(202);
      return res.end();
    }

    if (method === "tools/list") {
      res.writeHead(200, { "content-type": "application/json" });
      return res.end(rpcResult(id, { tools: publicTools }));
    }

    if (method === "tools/call") {
      const tool = tools.find((t) => t.name === params.name);
      if (!tool) {
        res.writeHead(200, { "content-type": "application/json" });
        return res.end(rpcError(id, -32602, `Unknown tool: ${params.name}`));
      }
      try {
        const result = await tool.handler(params.arguments ?? {});
        res.writeHead(200, { "content-type": "application/json" });
        return res.end(rpcResult(id, { isError: false, ...result }));
      } catch (err) {
        res.writeHead(200, { "content-type": "application/json" });
        return res.end(rpcResult(id, { isError: true, content: [{ type: "text", text: String(err?.message ?? err) }] }));
      }
    }

    res.writeHead(200, { "content-type": "application/json" });
    return res.end(rpcError(id, -32601, `Method not found: ${method}`));
  });
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const bearerToken = process.env.MCP_STUB_TOKEN;
  if (!bearerToken) {
    process.stderr.write("MCP_STUB_TOKEN is required\n");
    process.exit(1);
  }
  const port = Number(process.env.PORT ?? 8788);
  createMCPStub({ bearerToken, log: (r) => process.stdout.write(JSON.stringify(r) + "\n") })
    .listen(port, "127.0.0.1", () => process.stdout.write(JSON.stringify({ event: "listening", port, path: "/mcp" }) + "\n"));
}
