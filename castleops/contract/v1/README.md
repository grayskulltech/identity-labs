# CastleOps Interface Contract v1

This directory is the IP line described in ADR-001. Everything in it is published under this repository's MIT license so that either track, Grayskull or Cisco, can implement it without touching the other's code.

## What the contract is

- **Seven adapter interfaces** the gateway core depends on and never implements itself: `IdentityProvider`, `PolicyDecisionPoint`, `DevicePosture`, `GuardrailScanner`, `ZeroTrustTransport`, `ApprovalChannel`, `AuditSink`. See `interfaces.md`.
- **JSON Schemas** for every value that crosses an adapter boundary: `schemas/`. Adapters in any language validate against these.
- **The tool registry format**: `schemas/tool.schema.json`, with the two v1 tools in `tools/`. A tool is a typed capability with argument constraints the gateway enforces before policy ever sees the call.

## What the contract is not

It is not an implementation. The reference gateway in `../../gateway` is one consumer. The Cisco lab is another. Neither is the contract.

## Versioning

- The contract is versioned by directory: `v1`, `v2`. A directory is frozen on tag. Additive changes inside a frozen version are allowed only when every existing valid document remains valid and every existing adapter remains conformant.
- Anything else is a new major version. Both tracks treat a major bump as a breaking release.
- Tool definitions are versioned independently by their `version` field and never mutate in place. A changed tool is a new version with a new file.

## Tiers, restated for adapter authors

From ADR-001. Every adapter declares which tier its outbound traffic occupies, and the gateway refuses to load an adapter whose declared tier is higher than the deployment allows.

| Tier | Meaning | Personal deployment allows |
|---|---|---|
| 1 | LAN only | Yes |
| 2 | Opaque references may transit a third party | Yes, and the adapter documents exactly what leaves |
| 3 | Argument values, names, or content leave the LAN | No. A Tier 3 adapter is a Cisco-track adapter. |

## Conformance

An adapter is conformant when it passes the conformance vectors that ship with the reference gateway for its interface. Vectors are JSON documents under `../../gateway/internal/contract/testdata`. A conformant adapter in the Cisco track can be swapped in without a code change to the core.
