# Fallback for iOS 26: Claude through Shortcuts and Siri

iOS 26 has no third-party provider slot in Apple Intelligence. The supported
bridge is the Claude app's App Intents, which Shortcuts and Siri can invoke.

## What the Claude app exposes

The Claude iOS app ships these App Intents and controls:

| Surface | Name | Invocation |
|---|---|---|
| App Intent | **Ask Claude** | Spotlight, Siri by shortcut name, Share sheet, Shortcuts action |
| Control | **Analyze Photo with Claude** | Control Center, Lock Screen, Action button |
| Widget | Claude widget | Home Screen and Lock Screen |

Ask Claude takes a text prompt and returns the response as text, so it composes
with the rest of the Shortcuts library.

## Shortcut: Ask Claude from Siri

1. Open **Shortcuts**, create a new shortcut, name it something Siri parses cleanly.
   "Ask Claude" works. Siri triggers a shortcut by its name.
2. Add **Dictate Text** as the first action so the shortcut can capture a spoken prompt.
3. Add **Ask Claude** from the Claude app's actions. Set its prompt input to the
   dictated text variable.
4. Add **Show Result** (or **Speak Text** for hands-free use) bound to the Ask Claude output.
5. Optionally assign the shortcut to the Action button under
   **Settings**, **Action Button**, **Shortcut**.

Invoke with "Hey Siri, Ask Claude." Siri runs the shortcut, dictates the prompt,
and returns Claude's answer. This is a Shortcuts round trip, not Apple Intelligence
routing, so Siri's own context such as on-screen content is not passed along
unless you add actions that capture it.

## Shortcut: Triage a screenshot

1. New shortcut, receive **Images** from the Share sheet.
2. Add **Ask Claude** with a fixed prompt such as
   "List the phishing and credential-harvesting indicators in this screenshot."
   and attach the shared image as input.
3. Add **Show Result**.

Share any screenshot to this shortcut for a first-pass review without leaving the
current app.

## Guardrails on iOS 26

- Shortcuts run under the user's Claude account. Whatever the user's plan allows,
  the shortcut allows.
- Managed devices: App Intents from the Claude app are blocked when the Claude app
  itself is blocked, either by an app allowlist or by restricting unmanaged apps.
  There is no restriction key that blocks only the intents.
- Data leaving the device is whatever the shortcut passes to the intent. Review
  shortcuts before deploying them via managed Shortcuts distribution.

## Migration to iOS 27

Shortcuts built this way continue to work on iOS 27. Once the device is on iOS 27,
prefer the **Use Model** action with Claude selected as the provider: it carries
variables between steps natively and does not require the Dictate Text wrapper.
