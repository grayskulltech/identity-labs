# In-app integration with ClaudeForFoundationModels

This is the developer path. Your app talks to Claude through Apple's
`LanguageModelSession` API, the same one used for the on-device model, so a
single code path can serve either.

Primary reference:
<https://platform.claude.com/docs/en/cli-sdks-libraries/libraries/apple-foundation-models>

## Requirements

- iOS 27, macOS 27, visionOS 27, or watchOS 27
- Xcode 27
- The `ClaudeForFoundationModels` package, `0.1.0` or later
- A Claude Console workspace with billing enabled

## Package surface

The package is deliberately small. It is a Foundation Models provider, not a
general Messages API client.

| Type | Role |
|---|---|
| `ClaudeLanguageModel` | Conforms to `LanguageModel`. Pass it to `LanguageModelSession`. |
| `ClaudeModel` | Model identifier plus declared capabilities. Constants such as `.opus5` and `.sonnet5` mirror API IDs. |
| `AuthMode` | `.appAttest(clientID:)`, `.proxied(headers:)`, `.apiKey(_)` |
| `ClaudeServerTool` | `.webSearch(...)`, `.webFetch(...)`, `.codeExecution` |
| `ClaudeError` | Provider errors with no `LanguageModelError` equivalent, such as `.missingCredential` |

Features the Foundation Models protocol cannot express are unavailable through
the package: cache breakpoint control, stop sequences, batches, Files API, token
counting, and beta headers. Prompt caching is applied automatically.

## Authentication modes

| Mode | Ships a secret | Needs a back end | Use |
|---|---|---|---|
| `.appAttest(clientID:)` | No | No | Production, direct to Anthropic. Device proves it is a genuine build; Anthropic issues one-hour tokens scoped to your workspace. Physical device only. |
| `.proxied(headers:)` with `baseURL` | No | Yes | Production when you need a model allowlist, request logging, or per-user authorization enforced server-side. The `relay/` directory implements this. |
| `.apiKey(_)` | Yes | No | Simulator and local development only. Extractable from any shipped binary. |

App Attest setup:

1. Xcode target, **Signing & Capabilities**, add **App Attest**.
2. Claude Console, workspace settings, **App integrations**, **Create app integration**.
   Enter the Apple Developer Team ID and bundle IDs.
3. Copy the `clid_...` client ID into the app configuration.
4. Revoke the integration from the same screen to cut off a compromised build.

App Attest tokens identify the app, not the user. Per-user authorization stays in
your code, or in the relay.

## The Swift package in this lab

`swift/` contains `IdentityAssistant`, a library target that demonstrates:

| File | Shows |
|---|---|
| `ClaudeConfiguration.swift` | Building `ClaudeLanguageModel` from a typed configuration with all three auth modes, model pinned to Claude Opus 5 |
| `ModelRouter.swift` | Choosing on-device versus Claude per request based on data classification, with rate-limit fallback |
| `TriageVerdict.swift` | A `@Generable` structured output for sign-in risk triage |
| `SignInEventsTool.swift` | A client-side `Tool` the model can call to fetch recent sign-in events |
| `IdentityAssistant.swift` | The facade: one `triage(_:)` call that wires the pieces together |

Open `swift/Package.swift` in Xcode 27 on a macOS 27 host. The package has not
been compiled in this repository's CI because no OS 27 toolchain is available
there; treat the first build against a new beta as a verification step.

## Routing policy

The router is the guardrail. It runs before any network call:

```text
prompt + attachments
  -> classify(): contains identity PII? (email, UPN, IP, device ID, session token)
  -> policy.allowsCloudForPII?
       no  -> SystemLanguageModel (on-device)
       yes -> ClaudeLanguageModel
  -> on LanguageModelError.rateLimited from Claude -> retry once on-device
```

Classification is regex-based in the lab and intentionally conservative. Replace
`PIIClassifier` with your DLP engine's verdict when integrating.

## Model and effort

The lab pins `ClaudeModel.opus5`. The package sends the API default effort, which
is `high`, unless `fixedEffort:` is set. For interactive triage that is the right
default. For batch-like summarization inside the app, `fixedEffort: .medium`
lowers cost without changing the code path.

## Error handling

Match in this order:

1. `ClaudeError.missingCredential` — configuration problem, surface to the operator.
2. `LanguageModelError.rateLimited` — fall back to on-device for the turn.
3. `LanguageModelError.contextSizeExceeded` — trim transcript or summarize.
4. `LanguageModelError.guardrailViolation` and `.refusal` — show the user a plain
   message; do not retry with the same content.
5. Anything else — transport error, retry with backoff.

## Relay

`relay/server.mjs` is a zero-dependency Node 22 relay for `.proxied`. It:

- accepts only `POST /v1/messages`
- requires a bearer app token, compared in constant time
- rejects any model not on the allowlist
- strips inbound credential headers and attaches `x-api-key` from the environment
- forwards `anthropic-version` and `anthropic-beta` so structured outputs keep working
- streams the upstream body back unchanged
- logs request ID, model, status, and token usage, never prompt content

Run its tests with `node --test relay/`.
