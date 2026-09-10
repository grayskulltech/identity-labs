# Adapter interfaces, v1

Language-neutral definitions. Reference Go types live in `../../gateway/internal/contract`. Every input and output is a JSON document valid against the schema named in the same row.

Conventions: every call receives a `RequestContext` carrying `request_id`, `principal_assertion`, and `now`. Every error is typed as `deny`, `unavailable`, or `invalid`, and the gateway treats `unavailable` on a decision-path adapter as `deny`. Fail closed is the contract, not a configuration.

## IdentityProvider

Authenticates the person and exchanges tokens for agents. Tier 1 in the personal deployment (Authentik on the LAN). Cisco track: Duo SSO.

| Method | Input | Output | Notes |
|---|---|---|---|
| `VerifyAccessToken(token)` | opaque string | `PrincipalAssertion` (`principal-assertion.schema.json`) | Validates signature, issuer, audience, expiry. Returns subject, groups, auth time, and the `cnf` binding if present. |
| `ExchangeForAgent(assertion, actor_id, scope)` | assertion + agent NHI id + requested scope | new `PrincipalAssertion` with `act` chain appended | RFC 8693. The subject is preserved. The actor chain grows by exactly one. |
| `Groups(subject)` | subject id | list of group ids | Used by the policy request builder. Cached at most 60 seconds. |

## PolicyDecisionPoint

Decides a single tool call. Tier 1 in the personal deployment (embedded Cedar). Cisco track: Duo Agentic Identity authorization.

| Method | Input | Output | Notes |
|---|---|---|---|
| `Decide(request)` | `AuthzRequest` (`authz-request.schema.json`) | `AuthzDecision` (`authz-decision.schema.json`) | Must be deterministic for identical input. Must return the policy identifiers that determined the outcome. A `forbid` always wins over any `permit`. |
| `Validate()` | none | list of policy errors | Called at startup. Any error is fatal. |

## DevicePosture

Answers what the gateway may rely on about a device right now. Tier 1 (self-hosted MDM plus the device registry). Cisco track: Meraki Systems Manager and Duo Device Health.

| Method | Input | Output | Notes |
|---|---|---|---|
| `Lookup(device_id)` | UDID or device certificate fingerprint | `PostureRecord` (`posture-record.schema.json`) | Includes attested and reported OS versions with their timestamps, enrollment status, last check-in, model, and supervision state. |
| `Revoke(device_id, reason)` | device id, reason | none | Immediate. The next request from the device is denied. |

## GuardrailScanner

Scans tool results before they return to the phone and enter model context. Tier 1 (local classifier). Cisco track: AI Defense.

| Method | Input | Output | Notes |
|---|---|---|---|
| `ScanResult(tool_id, content)` | tool id, UTF-8 text | verdict `clean`, `suspicious`, or `blocked`, with findings | `blocked` replaces the content with a fixed notice. `suspicious` passes the content with a flag the app renders. Never returns rewritten content; the gateway wraps the original as a data block. |

## ZeroTrustTransport

Terminates the overlay and presents client identity to the gateway. Tier 2 when a hosted control plane exchanges keys (Tailscale). Tier 1 with WireGuard or Headscale. Cisco track: Secure Access.

| Method | Input | Output | Notes |
|---|---|---|---|
| `PeerIdentity(conn)` | connection handle | overlay peer id plus client certificate chain if mTLS | The gateway binds this to the device record. |
| `Listen(addr)` | overlay-only address | listener | Must refuse to bind a non-overlay address unless an explicit unsafe flag is set, and must log that flag on every start. |

## ApprovalChannel

Delivers a break-glass or enrollment approval request to an approver and returns the signed decision. Tier 2 (APNs carries a transaction id only). Cisco track: Duo Verified Push.

| Method | Input | Output | Notes |
|---|---|---|---|
| `RequestApproval(request)` | `ApprovalRequest` (`approval-request.schema.json`) | transaction id | The channel transports only the transaction id. Details are fetched by the approver over the overlay. |
| `AwaitDecision(txn_id, timeout)` | transaction id, duration | `approved` or `denied` plus approver assertion and signature over the request hash | The signature must be by the approver's presence key. |

## AuditSink

Append-only record of every decision. Tier 1 (Loki or Splunk on the LAN). Cisco track: Duo audit.

| Method | Input | Output | Notes |
|---|---|---|---|
| `Write(record)` | `AuditRecord` (`audit-record.schema.json`) | none | Must not block the decision path beyond a bounded local buffer. Loss is reported at startup and in a health endpoint, never silently. |
