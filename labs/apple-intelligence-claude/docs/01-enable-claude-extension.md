# Enable Claude as an Apple Intelligence Extension

Applies to iOS 27, iPadOS 27, and macOS 27. On earlier releases see
`02-shortcuts-ios26.md`.

## Prerequisites

| Requirement | Detail |
|---|---|
| Device | Apple Intelligence capable hardware. iPhone 15 Pro and later, M-series iPad and Mac, or A17 Pro and later. |
| OS | iOS 27, iPadOS 27, or macOS 27 with Apple Intelligence turned on. |
| Claude app | Current App Store build. The Extensions capability is provided by the app, not the OS, so an outdated Claude app will not appear in the provider list. |
| Claude account | Signed in inside the Claude app. Free accounts work. Plan limits apply to routed requests the same way they apply inside the app. |
| Management | Not blocked by `allowExternalIntelligenceIntegrations = false`. On a supervised corporate device the provider list is empty when that key is set. See `03-mdm-guardrails.md`. |

## Enable on iPhone and iPad

1. Install or update the Claude app and sign in.
2. Open **Settings**, then **Apple Intelligence & Siri**.
3. Open the section that lists external AI providers. Across the OS 27 betas Apple has
   labelled this **Extensions** and, in at least one build, **Default AI Service**. It sits
   below the Siri settings and above the per-app Apple Intelligence controls.
4. Select **Claude**. Consent copy explains which requests leave the device and that
   they are handled under Anthropic's terms. Accept it.
5. Choose the features Claude should serve. Each is an independent switch, so you can
   route Writing Tools to Claude while leaving Siri on Apple's model:
   - **Siri**: requests Siri cannot answer with Apple's model are handed to Claude.
   - **Writing Tools**: Rewrite, Proofread, Summarize, and Compose use Claude.
   - **Visual Intelligence**: camera and screenshot queries are sent to Claude.
   - **Use Model** in Shortcuts: the action's model picker gains a Claude entry.
6. If an **Ask before sending** style confirmation switch is present, leave it on for
   the pilot. It gives the user a per-request veto, which matters when the request
   contains screen content or clipboard text.

## Enable on Mac

1. Install the Claude app from the App Store or the Anthropic download and sign in.
2. **System Settings**, then **Apple Intelligence & Siri**.
3. Open the external providers section, select **Claude**, accept the consent sheet.
4. Enable the same per-feature switches. On macOS the Writing Tools switch also covers
   the Writing Tools entry in the right-click menu of every text field.

## Verify

Run each of these and confirm the response is attributed to Claude. Apple shows the
provider name on responses that came from an Extension, and the Claude app records
routed conversations in its history.

| Feature | Test | Expected |
|---|---|---|
| Siri | "Explain the difference between OIDC and SAML in two sentences." | Answer attributed to Claude, visible in Claude app history. |
| Writing Tools | Select a paragraph in Notes, choose Rewrite. | Rewrite appears and is attributed to Claude. |
| Visual Intelligence | Screenshot a login page, ask what the risk signals are. | Claude answers about the image content. |
| Shortcuts | Add a **Use Model** action, open the model picker. | Claude is listed. Run it with a fixed prompt. |

If Claude is not listed in step 3, check in this order: Apple Intelligence is on,
the Claude app is updated, the user is signed in, and the device is not carrying a
restriction profile.

## What routes and what does not

Only requests that Apple's own model declines or that the user explicitly directs
to the provider leave the device. Routine Siri actions such as timers, app launches,
and on-device App Intents never reach Claude. Apple's on-device and Private Cloud
Compute processing is unchanged for those.

## Reverse it

Return to the same Settings screen and either switch off individual features or
select Apple's model as the default. Removing the Claude app also removes the
provider entry. The Claude app retains routed conversation history under the
user's account until deleted there.
