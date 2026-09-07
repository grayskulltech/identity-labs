# MDM guardrails for Claude in Apple Intelligence

The decision is not "allow Claude or not." It is which data is permitted to leave
which device to which account. This guide gives the controls that exist, the ones
that do not, and a decision matrix.

## Restriction keys

All keys live in the Restrictions payload, `com.apple.applicationaccess`. Every
key below requires a supervised device on iOS and iPadOS, or a device-enrolled Mac.
User-enrolled and unmanaged devices ignore them.

| Key | Effect | Introduced |
|---|---|---|
| `allowExternalIntelligenceIntegrations` | `false` blocks every third-party provider under Apple Intelligence, which in OS 27 includes the Extensions provider list. This is the key that gated the ChatGPT integration. | iOS 18.2 / macOS 15.2 |
| `allowExternalIntelligenceIntegrationsSignIn` | `false` prevents users from signing in to the external provider from within Apple Intelligence settings. Requests then run as an anonymous session where the provider supports one. | iOS 18.2 / macOS 15.2 |
| `allowWritingTools` | `false` disables Writing Tools entirely, regardless of provider. | iOS 18.1 / macOS 15.1 |
| `allowImagePlayground` | `false` disables Image Playground. | iOS 18.1 / macOS 15.1 |
| `allowGenmoji` | `false` disables Genmoji. | iOS 18.1 / macOS 15.1 |
| `allowMailSummary`, `allowNotesTranscription`, `allowSafariSummary`, `allowIPhoneMirroring` | Per-feature Apple Intelligence switches. Not provider-specific. | iOS 18.x |

Two gaps to plan around:

- There is no key that permits one provider and blocks another. The external
  integrations key is all-or-nothing across ChatGPT, Claude, Gemini, and any other
  Extension. To allow only Claude, allow external integrations and remove the other
  provider apps through the app allowlist or a managed app removal.
- Siri's cross-app action capability in OS 27 currently has no dedicated
  restriction key. Blocking it means blocking Siri or the apps it targets.

Verify key names against Apple's reference before deploying:
<https://developer.apple.com/documentation/devicemanagement/restrictions>.

## Profiles in this lab

| File | Posture |
|---|---|
| `mdm/deny-external-intelligence.mobileconfig` | Blocks all external providers and provider sign-in. Default for supervised devices without an approved AI-use policy. |
| `mdm/allow-external-intelligence.mobileconfig` | Explicitly permits external providers and sign-in. Deploy alongside an app allowlist that includes only the Claude app. |

Both are unsigned. Sign them with your MDM's signing identity before distribution.
Adjust `PayloadIdentifier`, `PayloadUUID`, and `PayloadOrganization` for your tenant.
Validate with `plutil -lint` on a Mac or `xmllint --noout` anywhere.

## Data flow

| Path | Account used | Data leaves device | Apple sees content | Retained where |
|---|---|---|---|---|
| Extensions: Siri, Writing Tools, Visual Intelligence, Use Model | The user's Claude account | Yes, to Anthropic | No | Claude app conversation history under that account |
| Foundation Models via `.appAttest` | Your organization's workspace | Yes, to Anthropic | No | Per your workspace's retention setting |
| Foundation Models via `.proxied` | Your organization's workspace, through your relay | Yes, to your relay then Anthropic | No | Relay logs you control, plus workspace retention |
| Apple on-device model or Private Cloud Compute | None | No, or PCC only | Yes, under Apple's PCC guarantees | Not retained |

The Extensions row is the one that matters for identity data. Writing Tools has
access to whatever text is selected, in any app. If an analyst selects a block of
sign-in logs in a mail client and taps Summarize, that data goes to the analyst's
personal Claude account unless the device is supervised and the deny profile is in
place, or Writing Tools is blocked.

## Decision matrix

| Device class | Recommendation | Why |
|---|---|---|
| Supervised, handles regulated identity data | Deny profile. Build Claude access into your own app using the Foundation Models package with the on-device router. | Traffic is under an organizational workspace with contractual terms, model allowlist, and retention you control. |
| Supervised, general knowledge workers | Allow profile plus app allowlist limited to Claude. Enable Writing Tools and Siri, leave Visual Intelligence off for the pilot. | Consumer Claude terms apply. Visual Intelligence sends screenshots, which is the highest-leakage surface. |
| BYOD, user-enrolled | No device control is possible. Enforce at the data layer: managed apps with paste and Share restrictions, and DLP on the corporate identity providers. | Restriction keys do not apply. |
| Developer test devices | Allow profile, no allowlist. | Needed to exercise Extensions end to end. |

## Identity-specific checks before allowing Extensions

1. Confirm whether users will sign in to Claude with a personal or an organizational
   account. The Extensions path uses whatever account the Claude app holds. An
   Enterprise or Team Claude plan with SSO gives you an audit trail; a personal
   account does not.
2. If the Claude organization uses SSO, ensure the IdP's session policy covers the
   mobile app. Routed Siri requests will fail silently if the Claude session has
   expired and the user has not reopened the app.
3. Add the Claude app's bundle identifier to your app inventory with the data
   categories it can receive: selected text, screenshots, dictated audio transcripts.
4. Record the decision and the profile UUIDs in the same register that holds your
   other AI-tool approvals.
