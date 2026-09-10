# CastleOps Design Review: Apple Intelligence + Duo Agentic Identity + MCP Gateway

Status: review of the v0 concept spec. Verdict, validation matrix, devil's advocate, and a revised reference architecture.

## 1. Verdict

The shape is right. The details are wrong in ways that matter.

**Right:** every tool call goes through an identity-aware gateway that enforces per-action authorization, with agents modeled as first-class identities tied to an accountable human. That is exactly what Duo Agentic Identity shipped at RSAC 2026 (discovery, NHI lifecycle, least-privilege authz at the gateway on every tool call). Cisco AI Defense inspects MCP requests and responses at runtime. The break-glass pattern replaces standing admin rights with just-in-time elevation. None of this is vapor.

**Wrong, and blocking:**

| # | Problem | Severity |
|---|---------|----------|
| 1 | Phones are modeled as Non-Human Identities. A human tapping Siri is a human. This throws away MFA, device trust, and the human-sponsor model Duo actually enforces. | Blocking |
| 2 | The generic `ExecuteMCPToolIntent(toolName, toolArguments: JSON)` intent defeats App Intents. Parameters are statically extracted at build time; Siri cannot reliably fill a free-form JSON string. It also turns the phone into an arbitrary RPC proxy. | Blocking |
| 3 | The Swift client does not speak MCP. Streamable HTTP requires an `initialize` handshake, `Mcp-Session-Id`, `MCP-Protocol-Version`, and `Accept: application/json, text/event-stream`. A bare POST to `/rpc` is not MCP. The gateway URL is also a fictional Duo-hosted host with markdown link syntax embedded in the string literal, so `URL(string:)` returns nil. | Blocking |
| 4 | AI Defense is placed on the wrong hop. A typed JSON-RPC call from a phone is the lowest-risk payload in the system. Prompt injection enters where Gemma reads untrusted tool output (calendar titles, SSIDs, guest names, device hostnames) and where agents talk to agents. | High |
| 5 | Least privilege stops at the front door. The gateway checks Jaxon, then forwards to an orchestrator that runs with full domain rights and lets a local LLM decide what to do. That is a confused deputy. | High |
| 6 | Bearer tokens with no sender constraint. A token lifted off a jailbroken or backed-up phone is fully replayable. | High |
| 7 | Break-glass has a single approver. Gary's phone is simultaneously the root of trust and the only elevation path. Lost phone, dead battery, or Gary asleep means Stephanie cannot do anything above baseline. | Medium |
| 8 | "Local, privacy-first" contradicts routing every tool call through two Cisco cloud services. Tool names and arguments (guest names, calendar contents) transit Duo and AI Defense. | Medium |
| 9 | Success reporting is wrong. Any 2xx returns "Successfully executed" even when the JSON-RPC body carries `isError: true`. Any non-2xx is reported as "access denied", including 5xx and network faults. | Medium |
| 10 | Licensing and IP separation. Duo Agentic Identity and AI Defense are enterprise SKUs. Building a Grayskull or home-lab product on Cisco employee entitlements crosses the line you have drawn. | Must resolve before build |

## 2. Component validation matrix

| Component in spec | Exists as described? | Notes |
|---|---|---|
| Apple Intelligence as NLU for third-party actions | Partially | Siri routes to statically declared App Intents and App Shortcuts phrases. Freeform natural language for a domain Apple has not defined a schema for (home networking) does not work through Siri. The on-device Foundation Models framework with its `Tool` protocol does freeform tool calling, but only inside your app, not from Siri. |
| Swift App Intent wrapper | Yes | Correct mechanism, wrong design. One typed intent per action, not a generic passthrough. |
| OAuth 2.1 + PKCE on iOS | Yes | `ASWebAuthenticationSession`, S256, Keychain. Add DPoP (RFC 9449) with a Secure Enclave key. |
| Duo Agentic Identity MCP gateway | Yes | Intercepts every tool call, evaluates fine-grained policy, permits or blocks. Integrates with Cisco Secure Access, open-source MCP gateways, and AWS Bedrock AgentCore Gateway. It is not a Duo-hosted public RPC endpoint you POST to from a phone. |
| NHI lifecycle in Duo Directory | Yes | Each agent gets a human owner, group membership, auth at access, full logging. Intended for agents, not for phones with humans behind them. |
| Cisco AI Defense scanning MCP tool calls | Yes | Runtime guardrails on prompts, responses, agents, and MCP traffic. Detects tool misuse, memory poisoning, privilege escalation, intent hijacking. The Duo gateway integration was announced as "coming soon" at launch, so verify current availability before depending on an inline chain. |
| Break-glass with single-use transaction-bound JWT | Not a product feature | You build this. Duo Verified Push gives you the approval primitive with number matching. |
| Per-scope least privilege (`tools:all`, `network:guest_provision`) | Partially | Scope strings are too coarse. "Guest provision, max 4 hours, guest VLAN only" is a policy condition, not a scope. Duo's authz engine evaluates conditions on the call, so put constraints there. |
| CastleOps orchestrator (LangGraph + Ollama/Gemma) | Your build | The multi-agent part of a "multi-agent ecosystem" is entirely absent from the identity design. See section 5. |

## 3. What this gets you, end to end

If rebuilt per section 5, the outcome is:

- **Every family member is a real principal.** No shared admin password, no "Stephanie uses Gary's phone." Each action is attributable to a human, a device, and an intent.
- **Blast radius is bounded by policy, not trust.** Jaxon cannot reach a firewall tool because the gateway never lets the call exist, regardless of what the phone or the LLM asks for.
- **No standing admin.** Gary's own phone runs at baseline and elevates only per transaction. That is the same pattern you sell to Duo Care Signature customers.
- **Voice never leaves the device.** Siri's speech and intent resolution are on-device. What leaves is a typed, minimal tool call.
- **Full audit trail.** Duo logs the who, what device, which agent acted on whose behalf, and the policy decision. Ship it to Splunk.
- **A reference architecture with sale value.** This is a demonstrable "agentic identity for a small domain" lab. The commercial version belongs to Grayskull, with an open-source substitution for the Cisco pieces. The Duo/AI Defense version is a Cisco customer lab and stays on that side of the line.

## 4. Devil's advocate

**Is Siri the right front end at all?** Siri is a fixed-phrase router for third-party apps. "Hey Siri, let Marcus on the WiFi until dinner" only works if you shipped an App Shortcut phrase close to it and Siri resolves "until dinner" into a parameter type you declared. For anything freeform, the honest path is your own app UI with the Foundation Models framework doing tool calling into your typed intents. Widgets and Shortcuts get you most of the family value with none of the ambiguity.

**Is the whole Duo + AI Defense chain overkill for three users?** For the family, yes. Stephanie and Jaxon's real needs (guest WiFi, calendar) are a Shortcuts-driven PWA against the UniFi and CalDAV APIs. The chain is justified only if the deliverable is the reference architecture. Decide which it is and write that down, because the two have different owners (Grayskull vs. Cisco lab), different licensing, and different substitutions.

**Where is the multi-agent part?** The spec is a single-hop RPC with an LLM at the end. Agent identity matters at the hops you have not drawn: orchestrator to calendar agent, orchestrator to network agent, agent reading tool output back into model context. Right now the design authenticates the human and then hands off to a root-equivalent process whose behavior is decided by Gemma. That is the exact "shadow agent with ungoverned actions" problem Duo Agentic Identity was built to stop, and the design reproduces it behind the gateway.

**Privacy claim.** Two cloud services see every tool call. If "local, privacy-first" is a requirement, either keep arguments opaque (pass entity IDs, resolve names locally) or drop the claim. Do not keep both.

**Availability.** Duo outage, ISP outage, or Cisco cloud maintenance means nobody can control the local network from a phone. Define fail-closed at the gateway and give Gary a LAN-only mTLS admin path that does not depend on the internet.

**Push fatigue.** Jaxon can request break-glass from Siri as often as he likes. That is a social-engineering channel against the admin. Rate-limit requests per principal, require Verified Push with number matching, and show the exact tool and arguments on the approval screen (what you see is what you sign).

**Token theft.** Background Siri execution needs the Keychain item readable after first unlock, which widens the window. Sender-constrained tokens (DPoP with a Secure Enclave key) make a stolen token useless off-device.

**Device trust is missing.** Nothing in the spec checks that the iPhone is managed, unjailbroken, or current. Duo Device Health or MDM attestation belongs in the policy.

## 5. Revised reference architecture

```
[iPhone: human user]
  Siri / App Shortcut / in-app Foundation Models
      -> typed AppIntent (ProvisionGuestWiFi, CreateCalendarEvent, RequestElevation, ...)
      -> Duo SSO (OIDC, Passwordless/Passport, Device Health) -> access token, DPoP-bound (Secure Enclave key)
      -> MCP Streamable HTTP client (initialize, session, protocol version)
      -> reachable only via ZTNA (Cisco Secure Access or equivalent), never a public listener

[MCP Gateway (Duo Agentic Identity)]
  1. Validate DPoP-bound user token, device posture
  2. Evaluate policy: principal x tool x argument constraints (duration <= 4h, vlan == guest)
  3. Token exchange (RFC 8693): subject = user, actor = castleops-orchestrator NHI
  4. Forward to orchestrator with the exchanged token

[CastleOps Orchestrator (LangGraph)]
  - Runs as NHI castleops-orchestrator, no domain rights of its own
  - Dispatches to worker agents, each its own NHI: castleops-network, castleops-calendar, castleops-iot
  - Every worker call goes back through the gateway with a second token exchange (chain: user -> orchestrator -> worker)
  - AI Defense inspects: tool outputs before they enter model context, inter-agent messages, and final actions

[Break-glass]
  - RequestElevation intent -> gateway holds the call, emits approval with tool + args hash
  - Approvers: Gary primary, Stephanie secondary for a defined subset, both via Verified Push with number matching
  - On approval: single-use JWT, audience = tool, claim = sha256(args), ttl <= 60s, jti tracked for replay
  - Rate limit: N requests per principal per hour, all denials logged
  - Fail-closed. LAN-only mTLS admin path for Gary as the out-of-band fallback.
```

### Identity model

| Principal | Type | How authenticated | What it can do |
|---|---|---|---|
| gary, stephanie, jaxon | Human | Duo SSO + Passkey/Passport + Device Health | Baseline scopes per group. No standing admin, including Gary. |
| castleops-orchestrator | NHI, owner = gary | Client credentials + token exchange | Dispatch only. Cannot call domain tools directly. |
| castleops-network | NHI, owner = gary | Token exchange from orchestrator | UniFi guest provisioning, bounded by policy conditions. Firewall changes require elevation claim. |
| castleops-calendar | NHI, owner = gary | Token exchange from orchestrator | CalDAV read/write. |
| castleops-iot | NHI, owner = gary | Token exchange from orchestrator | Home Assistant, allowlisted entities. |

### Policy, not scope strings

Scopes name a capability. Conditions bound it. Examples for the Duo authz engine:

- `network.guest.provision` allowed for group `family` when `args.duration_minutes <= 240` and `args.vlan == "guest"`.
- `network.guest.provision` allowed for group `minors` when additionally `time.hour in 07..21`.
- `network.firewall.*` denied for everyone unless `token.elevation.tool == requested_tool` and `token.elevation.args_hash == sha256(args)`.
- `calendar.write` allowed for group `adults` only.

## 6. Corrected Swift skeleton

Typed intent, real MCP transport, DPoP-bound token, correct result handling. Skeleton, not a drop-in.

```swift
import AppIntents
import CryptoKit
import Foundation

struct ProvisionGuestWiFiIntent: AppIntent {
    static var title: LocalizedStringResource = "Add a WiFi Guest"
    static var description = IntentDescription("Creates a time-limited guest WiFi voucher via CastleOps.")

    @Parameter(title: "Guest Name") var guestName: String
    @Parameter(title: "Duration", default: .twoHours) var duration: GuestDuration

    static var parameterSummary: some ParameterSummary {
        Summary("Add \(\.$guestName) to guest WiFi for \(\.$duration)")
    }

    func perform() async throws -> some IntentResult & ProvidesDialog {
        let result = try await MCPClient.shared.call(
            tool: "network.guest.provision",
            arguments: ["guest_name": guestName, "duration_minutes": duration.minutes, "vlan": "guest"]
        )
        switch result {
        case .ok(let text):
            return .result(dialog: IntentDialog(stringLiteral: text))
        case .toolError(let message):
            throw MCPError.toolFailed(message)
        }
    }
}

enum GuestDuration: String, AppEnum {
    case oneHour, twoHours, fourHours
    static var typeDisplayRepresentation = TypeDisplayRepresentation(name: "Duration")
    static var caseDisplayRepresentations: [GuestDuration: DisplayRepresentation] = [
        .oneHour: "1 hour", .twoHours: "2 hours", .fourHours: "4 hours"
    ]
    var minutes: Int { switch self { case .oneHour: 60; case .twoHours: 120; case .fourHours: 240 } }
}

struct CastleOpsShortcuts: AppShortcutsProvider {
    static var appShortcuts: [AppShortcut] {
        AppShortcut(
            intent: ProvisionGuestWiFiIntent(),
            phrases: ["Add a WiFi guest in \(.applicationName)", "Let someone on the WiFi with \(.applicationName)"],
            shortTitle: "WiFi Guest",
            systemImageName: "wifi"
        )
    }
}

enum MCPCallResult { case ok(String), toolError(String) }

enum MCPError: Error, CustomLocalizedStringResourceConvertible {
    case notAuthenticated, forbidden(String), transport(Int), protocolError(String), toolFailed(String)
    var localizedStringResource: LocalizedStringResource {
        switch self {
        case .notAuthenticated: "Sign in to CastleOps first."
        case .forbidden(let why): "CastleOps policy denied this action: \(why)"
        case .transport(let code): "CastleOps gateway returned HTTP \(code)."
        case .protocolError(let why): "CastleOps protocol error: \(why)"
        case .toolFailed(let why): "The action failed: \(why)"
        }
    }
}

actor MCPClient {
    static let shared = MCPClient()
    private let endpoint = URL(string: "https://castleops-gw.kingsbrook.internal/mcp")!  // reachable only via ZTNA
    private var sessionID: String?
    private let protocolVersion = "2025-06-18"

    func call(tool: String, arguments: [String: Any]) async throws -> MCPCallResult {
        if sessionID == nil { try await initialize() }
        let body: [String: Any] = [
            "jsonrpc": "2.0", "id": UUID().uuidString, "method": "tools/call",
            "params": ["name": tool, "arguments": arguments]
        ]
        let (data, http) = try await post(body)
        switch http.statusCode {
        case 200: break
        case 401: sessionID = nil; throw MCPError.notAuthenticated
        case 403: throw MCPError.forbidden(String(data: data, encoding: .utf8) ?? "policy")
        default: throw MCPError.transport(http.statusCode)
        }
        // Streamable HTTP may answer with SSE; take the first JSON-RPC message.
        guard let json = try firstJSONRPCMessage(from: data, contentType: http.value(forHTTPHeaderField: "Content-Type")) else {
            throw MCPError.protocolError("empty response")
        }
        if let err = json["error"] as? [String: Any] {
            throw MCPError.protocolError(err["message"] as? String ?? "unknown")
        }
        let result = json["result"] as? [String: Any] ?? [:]
        let text = ((result["content"] as? [[String: Any]]) ?? [])
            .compactMap { $0["text"] as? String }.joined(separator: "\n")
        return (result["isError"] as? Bool ?? false) ? .toolError(text) : .ok(text)
    }

    private func initialize() async throws {
        let body: [String: Any] = [
            "jsonrpc": "2.0", "id": UUID().uuidString, "method": "initialize",
            "params": ["protocolVersion": protocolVersion,
                       "capabilities": [:],
                       "clientInfo": ["name": "CastleOps iOS", "version": "0.1"]]
        ]
        let (_, http) = try await post(body)
        guard http.statusCode == 200, let sid = http.value(forHTTPHeaderField: "Mcp-Session-Id") else {
            throw MCPError.protocolError("initialize failed (\(http.statusCode))")
        }
        sessionID = sid
        let notif: [String: Any] = ["jsonrpc": "2.0", "method": "notifications/initialized"]
        _ = try await post(notif)
    }

    private func post(_ body: [String: Any]) async throws -> (Data, HTTPURLResponse) {
        let token = try await OAuthManager.shared.accessToken()
        var req = URLRequest(url: endpoint)
        req.httpMethod = "POST"
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("application/json, text/event-stream", forHTTPHeaderField: "Accept")
        req.setValue(protocolVersion, forHTTPHeaderField: "MCP-Protocol-Version")
        if let sid = sessionID { req.setValue(sid, forHTTPHeaderField: "Mcp-Session-Id") }
        req.setValue("DPoP \(token)", forHTTPHeaderField: "Authorization")
        req.setValue(try await OAuthManager.shared.dpopProof(method: "POST", url: endpoint, accessToken: token),
                     forHTTPHeaderField: "DPoP")
        let (data, resp) = try await URLSession.shared.data(for: req)
        guard let http = resp as? HTTPURLResponse else { throw MCPError.transport(-1) }
        return (data, http)
    }

    private func firstJSONRPCMessage(from data: Data, contentType: String?) throws -> [String: Any]? {
        if contentType?.contains("text/event-stream") == true {
            let lines = String(decoding: data, as: UTF8.self).split(separator: "\n")
            for line in lines where line.hasPrefix("data:") {
                let payload = line.dropFirst(5).trimmingCharacters(in: .whitespaces)
                if let d = payload.data(using: .utf8),
                   let obj = try JSONSerialization.jsonObject(with: d) as? [String: Any] { return obj }
            }
            return nil
        }
        return try JSONSerialization.jsonObject(with: data) as? [String: Any]
    }
}

/// OAuth 2.1 + PKCE via ASWebAuthenticationSession against Duo SSO.
/// Access token stored in Keychain (kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly for background Siri execution).
/// DPoP key is a P-256 key in the Secure Enclave; the proof binds the token to this device.
actor OAuthManager {
    static let shared = OAuthManager()
    func accessToken() async throws -> String { fatalError("implement: Keychain read, refresh on expiry") }
    func dpopProof(method: String, url: URL, accessToken: String) async throws -> String {
        // JWT header {typ: dpop+jwt, alg: ES256, jwk}, claims {htm, htu, iat, jti, ath: sha256(accessToken)}
        fatalError("implement with SecureEnclave.P256.Signing.PrivateKey")
    }
}
```

## 7. Build order

1. Ownership, local-first scope, and inference placement are decided in [ADR-001](adr-001-local-first-apple-intelligence-dual-track.md). Its revised build order supersedes the steps below.
2. Stand up the gateway with two typed tools and two human principals. No LLM yet. Prove policy denial and audit.
3. Add DPoP and device posture. Prove a copied token fails off-device.
4. Add the orchestrator NHI and one worker NHI with token exchange. Prove the worker cannot call outside its scope even when the orchestrator asks.
5. Add the first typed App Intent and an App Shortcut phrase. Prove Siri end to end.
6. Add AI Defense (or the open substitute) on tool output ingestion. Prove a poisoned calendar title does not cause an action.
7. Add break-glass with two approvers, args-hash binding, rate limits, and the LAN fallback.
8. Only then add freeform NL via Foundation Models inside the app.

## 8. Sources

- [Introducing Duo Agentic Identity](https://duo.com/blog/introducing-duo-agentic-identity)
- [Duo Brings Identity and Authorization Across AI Agent Gateways](https://duo.com/blog/duo-brings-identity-and-authorization-across-ai-agent-gateways)
- [Cisco Reimagines Security for the Agentic Workforce](https://newsroom.cisco.com/c/r/newsroom/en/us/a/y2026/m03/cisco-reimagines-security-for-the-agentic-workforce.html)
- [Cisco AI Defense data sheet](https://www.cisco.com/c/en/us/products/collateral/security/ai-defense/ai-defense-ds.html)
- [Securing AI Agents with Cisco AI Defense](https://blogs.cisco.com/ai/securing-ai-agents-with-cisco-ai-defense)
- [WWDC26: Explore advanced App Intents features for Siri and Apple Intelligence](https://developer.apple.com/videos/play/wwdc2026/343/)
- [WWDC26: Build intelligent Siri experiences with App Schemas](https://developer.apple.com/videos/play/wwdc2026/240/)
- [App Intents Are Apple's New API to Your App](https://blakecrosley.com/blog/app-intents-are-apples-new-api-to-your-app)
