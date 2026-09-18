# CastleOps

Local-first, identity-governed tool execution for the Kingsbrook domain, driven from Apple devices. Design record in `../docs/castleops/`: the design review and ADR-001 through ADR-004.

[ADR-005](../docs/castleops/adr-005-documentation-knowledge-service.md) is a related but separate personal service (a Duo/Cisco/Identity Intelligence documentation search, self-hosted on fortress — reachable at `castleops.grayskulltech.com`) — it deliberately sits outside the `contract/v1` gateway described below, for the reasons its own "Relationship to the v1 interface contract" section states.

## Layout

| Path | What it is | Owner |
|---|---|---|
| `contract/v1/` | The interface contract: seven adapter interfaces, JSON schemas for every boundary value, the tool registry format, and the v1 tools. MIT. This is the IP line between the Grayskull and Cisco tracks. | Grayskull, published |
| `gateway/` | The reference gateway in Go. Embedded Cedar policy decision point, ES256 token verification, DPoP device binding, MCP Streamable HTTP transport, audit sink, and a simulation world that doubles as the test suite. | Grayskull core |

## Build order status

From ADR-001, ADR-002, and ADR-004, in order. Each step ends with something provable from a terminal.

| Step | Claim | Status |
|---|---|---|
| 1 | Interface contract v1 published | Done. `contract/v1` |
| 2 | Gateway with embedded Cedar, typed tools, human principals, sender-constrained requests. Deny, allow, and audit from the CLI. | Done for the in-memory world. `castleops-gw simulate` runs 20 scenarios. Authentik and a persistent device registry are the next adapters. |
| 3 | Self-hosted MDM and step-ca, posture record at the gateway, Cedar denies stale or below-minimum posture | Policy and posture record are done and tested with synthetic records. MDM and step-ca adapters not started. |
| 4 | Overlay-only listener | `serve` refuses non-private bind addresses. Overlay adapter not started. |
| 5 | iOS app: passkey, App Attest enrollment, presence key, MCP client | Not started. DPoP binding stands in for App Attest; the App Attest adapter is stubbed and fails closed. |
| 6 | Foundation Models guided generation into the intent enum | Not started |
| 7 | Chat session with read tools, then write tools behind the confirmation card | Not started |
| 8 | Orchestrator and worker NHIs with token exchange | Token actor chains are parsed and audited. Exchange endpoint not started. |
| 9 | Guardrail scanner on tool output, LAN model as session provider | Pattern scanner placeholder is wired and tested. Local classifier not started. |
| 10 | Break-glass with two approvers, opaque push, args-hash binding, rate limits | Args-hash binding, single-use, and self-approval forbid are enforced and tested. Approval channel not started. |
| 11 | Cisco adapter set in the Cisco lab against a tagged release | Not started; by design, last |

## Run it

```
cd castleops/gateway
go test ./...
go run ./cmd/castleops-gw validate
go run ./cmd/castleops-gw simulate
go run ./cmd/castleops-gw simulate -json | head
```

`simulate` builds three people, three enrolled phones with session and presence keys, the v1 tools, and the embedded policy set, then runs every scenario and prints the decision, the deny stage, the determining policies, and the guardrail verdict. The same scenarios are the test suite.

`serve` starts the MCP endpoint. It requires mTLS with the device CA and refuses to bind a non-private address; both protections can be overridden by flags that log loudly on every start.

## What a request goes through

```
transport   the peer certificate names an enrolled device
token       ES256 access token from the LAN IdP: issuer, audience, expiry, subject, groups, cnf, act chain
device      record is active and enrolled to this subject
attestation proof signed now by the session key or the presence key of this device; jti not replayed
schema      the tool exists and the arguments validate exactly; defaults applied; args hashed
policy      Cedar decides on principal, action, tool, and derived facts; any forbid wins; any error denies
presence    a write not signed by the presence key stops here (a Cedar forbid, attributed as its own stage)
execution   the worker NHI for the tool runs it; oversize results are truncated and flagged
guardrail   tool output is scanned; blocked content is replaced with a fixed notice
audit       exactly one record per call, allowed or denied, with the argument hash and never the arguments
```

## Rules the code enforces that are easy to lose

- A `forbid` always wins. The four posture and presence forbids plus the self-approval forbid are required at startup; a policy file without them does not load.
- Any Cedar evaluation error is a deny, because a forbid that errored is a forbid that did not fire.
- Every attribute a policy references is always present in context. Derived facts (ages, version comparisons, hour of day) are computed in Go so policy never parses a date or a version string.
- An elevation token is consumed the moment it succeeds. Replaying it is a deny regardless of its expiry.
- Audit records carry the argument hash, never the arguments. Guest names and rule ids are Tier 3.
- An adapter declares its tier and the gateway refuses to load one above the deployment's allowance.
