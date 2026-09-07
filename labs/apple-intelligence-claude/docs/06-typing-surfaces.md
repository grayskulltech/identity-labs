# Asking questions while typing, on device only

No Claude, no network model. Apple's `SystemLanguageModel` answers, and your MCP
gateway supplies the tools. The question is where you type the question.

## The four typed surfaces

| Surface | Reaches you where | Cost to build | Hard limits |
|---|---|---|---|
| **Keyboard extension** | Inside any app, without leaving the text field | High | ~30–60 MB before jetsam; no network without Full Access |
| **Spotlight** (App Intent) | Swipe down from any screen | Low | Leaves the app you were typing in |
| **Action extension** | On text you selected | Medium | Two taps; selection required |
| **In-app composer** | Only in your app | Lowest | You have to switch apps |

Only the keyboard answers without breaking the typing flow. It is also the only one
with a real chance of being killed by the system. Build the Spotlight path first —
it is a dozen lines once the runtime exists — then the keyboard.

## The constraint that actually binds

Not memory. **Context.** Apple's on-device model shipped with a 4,096-token window
and current generations run 8,192. Instructions, every attached tool's schema, the
question, the tool results, and the answer all share it.

A gateway with a dozen tools does not fit. Measure it before you design around it:

```text
contextSize                 4096
  − reserved for output      512
  − reserved for working set 768   (the question + tool results coming back)
  − instructions              ~60
  = available for schemas    2756 tokens
```

One MCP tool with a realistic JSON Schema bills 100–400 tokens. So the honest
budget is **three or four tools per request**, not twelve.

`OnDeviceAssistant` handles this rather than letting the session fail:

1. `tools/list` from the gateway, filtered by your allowlist.
2. Rank the candidates against the question by term overlap (`ToolRelevance`).
3. Admit them greedily while the measured schema cost fits (`ToolBudget`), capped at
   four regardless of budget — a long tool list degrades selection on a small model
   even when it fits.
4. Attach only those, then run the session.

Token cost is measured with the model's own tokenizer via `tokenCount(for:)`, not
estimated. `plan(for:)` returns the selection without running anything, so a debug
screen can show which tools a question would pull in and which were dropped.

### A limitation to design around

Ranking is term overlap, so it finds tools through their **descriptions**, not their
identifiers. `lookup_signins` stems to one token, `signin`, and never matches a
person typing "sign-ins". This is asserted in the tests so it is not mistaken for a
bug. The consequence is a rule for your gateway: **write tool descriptions in the
words a person would actually type.** "Recent sign in events for a user" earns
matches; "SIEM query passthrough" does not.

## Building the keyboard

`keyboard/KeyboardViewController.swift` is a thin `UIInputViewController`: a status
label, an Ask button, and the text handoff. Everything else is in `OnDeviceAssistant`.

The flow is deliberately simple. Type the question in the app's own text field, tap
Ask, and the keyboard replaces what you typed with the answer:

1. `documentContextBeforeInput` back to the last line break is the question.
2. Run it on device with the selected tools.
3. Delete exactly what was captured — and only if it is still there, since the
   person may have kept typing — then insert the answer.

Info.plist, under `NSExtensionAttributes`:

```xml
<key>RequestsOpenAccess</key><true/>
```

### Two things that will bite you

**Memory.** A keyboard extension gets roughly 30–60 MB and jetsam kills it with no
crash log, no signal, no exception — the keyboard just vanishes and iOS falls back to
the system one. Whether `SystemLanguageModel` inference counts against that budget is
not documented, so treat it as the first thing to measure on a real device. The
package is structured so it can go either way: `OnDeviceAssistant` links only
`MCPBridge` and `FoundationModels`, never a cloud vendor's SDK, so nothing is loaded
into that process that does not need to be. If inference does prove too heavy, the
Action extension gets a more generous budget and the same code runs there unchanged.

**Full Access.** Without it a keyboard has no network at all, so your gateway is
unreachable. With it, the keyboard can see everything typed in every app on the
device. That is the real trade, and it is worth stating plainly to whoever approves
this: you are asking people to grant a keyboard the ability to observe their typing
in order to reach your tools. The controller degrades instead of failing — it checks
`hasFullAccess`, says "answering without your tools", and lets the model answer from
what it knows.

On a managed fleet, third-party keyboards are restrictable through the Restrictions
payload. Verify the exact key against Apple's device management reference or the
Apple Device Policy Explorer before you write the profile; the keyboard-related keys
are not the same as the Apple Intelligence keys in `03-mdm-guardrails.md`.

## Wiring it up

```swift
import OnDeviceAssistant
import MCPBridge

await OnDeviceAssistant.shared.configure(.init(
    gateway: MCPGatewayConfiguration(
        endpoint: URL(string: "https://mcp.example.com/mcp")!,
        tokenProvider: KeychainTokenProvider(
            service: "com.example.assistant",
            accessGroup: "group.com.example.assistant"   // shared with the keyboard
        )
    ),
    tools: MCPToolPolicy(allowedTools: ["lookup_signins", "check_cert", "who_owns"])
))
```

The keychain access group matters: the keyboard extension and the containing app are
separate processes, so the token has to live in a shared group for both to read it.

For Spotlight, register `OnDeviceAssistantIntents` from your app's
`AppIntentsPackage`. `AskInlineIntent` then appears in Spotlight and Shortcuts, takes
a typed question, and returns a typed answer.

## Degradation, in order

The assistant is built to lose capability rather than fail:

| Condition | Behavior |
|---|---|
| Gateway unreachable | Model answers from what it knows; no tools |
| No Full Access (keyboard) | Same, and the status label says so |
| Apple Intelligence off or device ineligible | Ask button disabled, reason shown |
| Question needs more context than fits | Says so and suggests narrowing the allowlist |
| Tool not marked read-only | Held for confirmation, as everywhere else in this lab |

Nothing in this path contacts a model vendor. The only network call is to your
gateway, with the person's own bearer token.
