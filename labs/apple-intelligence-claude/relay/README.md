# Claude relay for `.proxied` auth

Zero-dependency Node 22 relay that sits between an app using
`ClaudeForFoundationModels` and the Claude API. It exists so the app ships no
API key and so model choice is enforced server-side.

## Run

```sh
export ANTHROPIC_API_KEY=sk-ant-...
export RELAY_APP_TOKENS="$(openssl rand -hex 32)"
export RELAY_ALLOWED_MODELS="claude-opus-5"
node server.mjs
```

| Variable | Default | Purpose |
|---|---|---|
| `ANTHROPIC_API_KEY` | required | Workspace key attached to every upstream request |
| `RELAY_APP_TOKENS` | required | Comma-separated bearer tokens the app presents. List two during rotation. |
| `RELAY_ALLOWED_MODELS` | `claude-opus-5,claude-sonnet-5` | Requests naming any other model are rejected with 400 |
| `RELAY_UPSTREAM` | `https://api.anthropic.com` | Override for testing |
| `RELAY_MAX_BODY_BYTES` | 5 MiB | Request body cap |
| `PORT` | `8787` | Listen port |

Point the Swift side at it:

```swift
ClaudeConfiguration(auth: .proxied(
    baseURL: URL(string: "https://relay.example.com")!,
    appToken: "<value from RELAY_APP_TOKENS>"
))
```

The package appends `/v1/messages` to `baseURL`. Terminate TLS in front of the
relay; it listens on plain HTTP.

## Behavior

- Only `POST /v1/messages` is relayed. `GET /healthz` answers without auth.
- App token comparison is constant-time.
- Inbound `x-api-key` and `authorization` are never forwarded upstream.
- `anthropic-version` and `anthropic-beta` are forwarded so guided generation and
  other package features keep working.
- Streaming responses are piped through byte-for-byte.
- Each request logs one JSON line: request ID, model, status, duration, usage,
  stop reason. Prompt and response content are never logged.

## Test

```sh
node --test
```

## MCP gateway stub

`mcp-gateway-stub.mjs` is a separate test double: a Streamable HTTP MCP server with
one read-only tool and one destructive tool, bearer auth, and in-memory sessions.
Use it to exercise the Swift `MCPBridge` client before pointing the app at a real
gateway.

```sh
MCP_STUB_TOKEN=dev node mcp-gateway-stub.mjs
```

Endpoint: `http://127.0.0.1:8788/mcp`. See `docs/05-mcp-gateway-bridge.md`.

## Hardening for production

- Replace the shared bearer token with a per-user token minted by your IdP so the
  relay can attribute usage. The `Authorization` header is already the channel.
- Add per-token rate limiting in front of the relay. Claude API 429s are passed
  through with `retry-after`, and the Swift router falls back on-device for that turn.
- Run it behind your egress controls so the only outbound destination is Anthropic.
