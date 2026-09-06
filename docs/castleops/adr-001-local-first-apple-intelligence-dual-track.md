# ADR-001: Local-first, Apple Intelligence inference, dual-track ownership

Status: accepted. Supersedes the open decisions in section 7 of the design review.

## Decisions

1. **Local-first is a hard requirement.** This is a personal system holding personal data. The authoritative policy decision, tool execution, and audit record live on the Kingsbrook LAN. No cloud service is in the critical path of an action.
2. **Inference runs on the phone via Apple Intelligence.** Freeform intent extraction uses the on-device Foundation Models framework with guided generation. Siri App Shortcuts cover fixed phrases. No server-side LLM chooses actions. Extended to multi-turn conversation in [ADR-002](adr-002-conversational-layer.md).
3. **Dual track.** The same design ships as a Grayskull product (open-source adapters) and as a Cisco reference architecture (Duo and AI Defense adapters). The two share one published interface contract and nothing else.

## What local-first means, precisely

| Tier | Data | Where it may go |
|---|---|---|
| 0 | Voice, transcript, intent extraction, model context | The phone only. Foundation Models framework runs exclusively on-device. Siri features that route to Private Cloud Compute are not used for CastleOps actions. |
| 1 | Policy decisions, tool arguments, tool execution, audit log, identity records | Kingsbrook LAN only. Self-hosted identity provider, gateway, policy engine, log store. |
| 2 | Opaque references only | May transit a third party. Examples: an APNs push carrying a transaction ID and nothing else; a WireGuard or Tailscale control plane exchanging keys and endpoints. |
| 3 | Names, calendar contents, SSIDs, device inventory, guest identities, argument values | Never leave the LAN. Never appear in a push body, a cloud log, or a SaaS authz request. |

A component that cannot satisfy its tier is not "configured carefully". It is replaced or moved to the Cisco track.

## Consequences for the architecture

### Policy decision point is local

The gateway embeds its own policy engine (Cedar, or OPA with Rego) and decides every tool call from local state. Duo Agentic Identity's fine-grained authorization is a cloud decision, so in the personal deployment it cannot be the PDP. In the Cisco track the PDP adapter is Duo and the gateway forwards the decision request there. Same tool contract, different PDP, and that swap is the demo.

This also closes the availability finding from the review. ISP down means the phone reaches the gateway over the LAN, the PDP answers locally, and guest WiFi still gets provisioned.

### Identity provider is self-hosted

Personal track: Authentik or Keycloak on the lab host, OIDC with PKCE, passkeys for the family, device-bound refresh tokens, DPoP on every gateway request. How the person, the device, and human presence are bound to Apple primitives is in [ADR-003](adr-003-apple-native-identity-binding.md). Cisco track: Duo SSO with Passwordless and Device Health, same OIDC client configuration on the phone.

Running your own IdP is a critical dependency. Guardrails, not effort: pinned container images, Renovate for version bumps, nightly encrypted backup of the IdP database to a second host, a documented restore drill, and a break-glass local admin account stored offline.

### Inference on the phone changes the backend

Because the phone extracts a typed intent before anything is sent, the orchestrator does not need an LLM to decide actions. LangGraph becomes a deterministic state machine: validate, authorize, execute, audit. Ollama and Gemma are demoted to two optional read-only jobs, summarizing tool output for display and answering questions over local data. Neither can emit a tool call.

This removes the confused-deputy finding at its root. There is no model on the server with agency, so there is nothing for a poisoned calendar title to hijack. Guardrail scanning narrows to one path: tool output that will be rendered into the on-device model's context for a follow-up turn.

### Apple Intelligence integration pattern

Two entry points, one contract:

- **Siri, fixed phrases.** `AppShortcutsProvider` with typed intents (`ProvisionGuestWiFiIntent`, `CreateCalendarEventIntent`, `RequestElevationIntent`). Parameters are `AppEnum` or `AppEntity` types, never free strings where an enum will do.
- **In-app freeform.** A `LanguageModelSession` with guided generation into a `@Generable` enum of intents. The model is forced to produce one of the declared intents with typed fields or an explicit `.unsupported` case. The app then invokes the same typed intent Siri would. The model never sees or emits a tool name string.

```swift
import FoundationModels

@Generable
enum CastleOpsRequest {
    case provisionGuestWiFi(guestName: String, durationMinutes: GuestDurationMinutes)
    case createCalendarEvent(title: String, startISO8601: String, durationMinutes: Int)
    case requestElevation(reason: String)
    case unsupported(reason: String)
}

@Generable
enum GuestDurationMinutes: Int { case sixty = 60, oneTwenty = 120, twoForty = 240 }

func interpret(_ utterance: String) async throws -> CastleOpsRequest {
    let session = LanguageModelSession(instructions: """
        Map the user's request to exactly one CastleOps action. \
        If it does not match a listed action, return unsupported with a one-line reason. \
        Never invent parameters the user did not state.
        """)
    let response = try await session.respond(to: utterance, generating: CastleOpsRequest.self)
    return response.content
}
```

The generated enum is then mapped 1:1 onto typed App Intents, which go through `MCPClient` as in the design review. Same gateway, same policy, whether the request came from Siri or the in-app model.

Device support: Apple Intelligence requires an A17 Pro or later iPhone. Any family device below that gets the Shortcuts and widget UI with no freeform layer. Design the app so freeform is additive, never required.

### Guardrails

Personal track: a local classifier on the lab host (Prompt Guard or LLM Guard class) scans tool output before it is returned to the phone for rendering into model context. Cisco track: AI Defense adapter on the same hook. Both implement `GuardrailScanner`.

### Reachability

Personal track: self-hosted WireGuard, or Tailscale with a self-hosted Headscale control plane, so Tier 2 stays opaque. Gateway binds only to the overlay address. Cisco track: Cisco Secure Access ZTNA in front of the same gateway.

### Break-glass

Approval request travels as an APNs push with an opaque transaction ID only (Tier 2). The approver's app fetches the tool, arguments, and requester from the LAN gateway over the overlay and renders them before approving (what you see is what you sign). Approver set is Gary primary, Stephanie secondary for a defined subset, both by passkey re-authentication in the app. The elevated JWT is single-use, audience-bound to the tool, carries the args hash, and expires in 60 seconds. Rate limit per requester. LAN-only mTLS admin path remains the last resort.

### Audit

Local log store (Loki, or the Splunk instance already in the lab) with an append-only retention policy. Every record: human principal, device, intent, arguments hash, PDP decision, executing agent NHI, result. Cisco track adds a Duo audit sink adapter.

## Dual-track ownership

The IP line is the interface contract, published under this repo's MIT license so either side may implement it. Nothing crosses in either direction except that contract.

| Layer | Grayskull (personal deployment and product) | Cisco reference architecture |
|---|---|---|
| Interface contract | Owns and publishes: `IdentityProvider`, `PolicyDecisionPoint`, `GuardrailScanner`, `ZeroTrustTransport`, `ApprovalChannel`, `AuditSink`, plus the tool schema registry | Consumes only |
| Core: gateway, typed tool schemas, token exchange, break-glass, iOS app | Owns | Consumes the published release only, never the source of unreleased work |
| Adapters | Authentik/Keycloak, Cedar/OPA, local classifier, WireGuard/Headscale, APNs opaque push, Loki/Splunk | Duo SSO, Duo Agentic Identity authz, AI Defense, Secure Access, Duo Push, Duo audit |
| Data | Real family principals, real infrastructure | Synthetic principals only. No family names, no Kingsbrook inventory, no personal calendars |
| Where it lives | `grayskulltech/*` repos | A Cisco-side lab, built on Cisco time, for Duo Care customers |
| Licensing | Grayskull commercial or open-core at Grayskull's choice; Cisco SKUs are never a dependency | Cisco SKUs; Grayskull core is a public dependency like any other |

Two rules that keep this clean under pressure:

1. Cisco proprietary SDKs, tenant identifiers, or customer material never appear in a `grayskulltech` repo, including in tests, fixtures, or docs.
2. Unreleased Grayskull code never runs in the Cisco lab. The Cisco track pins a tagged public release.

The personal deployment is the Grayskull track running with real data. That is dogfooding, and it is the only place real data exists.

## Devil's advocate on these decisions

- **The on-device model is small.** Guided generation into a closed enum is what makes it dependable, and it is the only acceptable mode. Free-text generation followed by parsing is not permitted in the action path.
- **Two tracks double the maintenance surface.** Freeze the interface contract at v1 before either adapter set is written, version it semantically, and treat a contract change as a breaking release on both sides.
- **The Cisco demo loses "Duo is the enforcement point" in the personal build.** Correct, and intended. In the Cisco track Duo is the PDP. The demo's message is that the enforcement point is pluggable and the tool contract holds either way, which is a stronger story for a reference architecture than a hard dependency.
- **A self-hosted IdP that dies takes the house down.** Fail-closed at the gateway is still right. The mitigations are the backup, restore drill, and the LAN-only mTLS admin path. Test the restore quarterly with a calendar reminder, not a promise.
- **APNs is still Apple's cloud.** A transaction ID is the only payload. If even that is unacceptable, the fallback is LAN-only approvals with the approver on the overlay, at the cost of off-LAN elevation latency.
- **Tailscale's hosted control plane sees device keys and endpoints.** Headscale removes that. If Headscale is too much to run, accept Tailscale as Tier 2 explicitly and write it down.

## Revised build order

1. Publish the interface contract v1 in this repo: six adapter interfaces and the tool schema registry format.
2. Gateway with embedded Cedar PDP, two typed tools, two human principals against Authentik, DPoP enforced. No phone, no LLM. Prove deny, allow, and audit from the command line.
3. WireGuard or Headscale overlay. Gateway bound to the overlay only.
4. iOS app: OIDC with PKCE and passkeys, Secure Enclave DPoP key, `MCPClient`, one typed App Intent and one App Shortcut phrase. Prove Siri end to end on the LAN and off it.
5. Foundation Models guided generation into the intent enum. Prove `.unsupported` on out-of-scope requests and refusal to invent parameters.
6. Orchestrator NHI and one worker NHI with token exchange. Prove the worker is denied outside its scope even when the orchestrator asks.
7. Local guardrail scanner on the tool-output path. Prove a poisoned calendar title reaches the phone as inert text.
8. Break-glass with two approvers, opaque push, args-hash binding, rate limits, LAN fallback.
9. Only then: the Cisco adapter set, in the Cisco lab, against a tagged release, with synthetic data.
