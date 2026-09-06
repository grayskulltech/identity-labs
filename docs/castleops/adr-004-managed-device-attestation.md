# ADR-004: Attested device posture is a requirement

Status: accepted. Amends ADR-003, where Managed Device Attestation was the upgrade path. It is now day one.

## Decision

Every family device that can act on the Kingsbrook domain is enrolled in a self-hosted MDM on the LAN and carries a device certificate that the LAN certificate authority issues only after Apple attests the device's identity, hardware, and OS version. The gateway treats that attested posture as a policy input on every request. A device that is unenrolled, out of date, or whose attestation has lapsed is denied, not warned.

Enrollment is unsupervised, on personal Apple Accounts, with a minimal payload set. No Apple Business Manager, no Managed Apple Accounts, no Grayskull business identity on a family phone.

## What Apple attests, and what it does not

Managed Device Attestation (iOS 16 and later) lets an MDM deliver an ACME payload with attestation enabled. The device asks Apple's attestation service to vouch for it, and Apple returns a certificate chain that binds a Secure Enclave key to attested properties.

| Attested by Apple's hardware root | Self-reported by the device, not attested |
|---|---|
| Serial number, UDID, model identifier | Anything in a `DeviceInformation` query |
| OS version at the time of attestation | Declarative Device Management status reports, including OS version between attestations |
| Secure Enclave residency of the key, SEP firmware version | Jailbreak state. There is no attested jailbreak signal. A compromised OS is expected to fail attestation, and that is the strongest available signal. |
| Supervision state and enrollment type | Installed apps, restrictions in effect |

Apple rate-limits attestation, so the attested OS version is a snapshot renewed on a cadence, and the self-reported DDM status fills the gaps. Policy uses both, as below.

## Components on the LAN

| Role | Choice | Notes |
|---|---|---|
| MDM server | NanoMDM with NanoDEP omitted, or Fleet | Both are open source and self-hostable. Fleet adds Declarative Device Management and a UI. Pick Fleet if you want the UI, NanoMDM if you want the smallest surface. |
| ACME CA with device-attest-01 | step-ca | Verifies the Apple attestation chain and issues the device certificate. Bound to the LAN. Trusts only Apple's Enterprise Attestation root. |
| MDM push | Apple Push Notification service | Mandatory for any Apple MDM. The push carries a wake token and nothing else. Tier 2. |
| Push certificate | Apple Push Certificates Portal | Issued to a dedicated household Apple Account, not a Grayskull account. The vendor CSR signing step needs a paid Apple Developer membership or a signing service; see devil's advocate. |
| Posture store | Gateway device registry, extended | Adds attested serial, model, OS version, attestation time, DDM-reported OS version, enrollment status, last check-in. |

## Data flows, by tier

| Flow | Tier | What crosses |
|---|---|---|
| Device to LAN MDM: check-in, commands, DDM status | 1 | Everything. Stays home. |
| APNs to device: MDM wake | 2 | Opaque wake token. Apple knows the device is managed by some server. It does not learn which or what was commanded. |
| Device to Apple attestation service | 2 | Apple attests its own hardware to itself. The request carries the nonce and client data hash from step-ca. Apple learns that this device is being attested, which it already knows how to do for its own services. |
| step-ca issues device certificate | 1 | Stays home. |
| Gateway reads posture | 1 | Stays home. |

No tool argument, calendar entry, or family name is in any of these flows.

## The three-leg identity, updated

ADR-003 gave the gateway three facts: person, device, presence. Attested posture strengthens the device leg and adds a fourth fact.

| Fact | Primitive | Verified by |
|---|---|---|
| This is the person | Passkey in Authentik | Authentik |
| This is a genuine iPhone running our signed app | App Attest key | Gateway, offline |
| This device is enrolled, attested, and on an acceptable OS | MDM-issued device certificate from step-ca, presented over mTLS to the overlay and gateway, plus the posture record | Gateway, on every request |
| A human is present for this write | Biometry-gated presence key | Gateway checks which key signed |

App Attest and the MDM certificate are not redundant. App Attest proves the app. The MDM certificate proves the device and its posture. Both keys live in the Secure Enclave and neither leaves the phone. The ACME payload sets `AllowAllAppsAccess` so the CastleOps app can present the device certificate for mTLS.

## Policy inputs

Cedar conditions available on every request, evaluated locally:

- `device.enrolled == true` and `device.lastCheckin` within 24 hours
- `device.attestedOSVersion >= policy.minimumOS` and `device.attestedAt` within 7 days
- `device.ddmOSVersion >= policy.minimumOS`, so a device that updated since attestation is not blocked, and a device that reports an old version is
- `device.model` in the household allowlist
- `device.supervised` is recorded, not required. Personal phones are unsupervised by design.

Minimum OS is set by policy, not by hand: current major release, with a grace period of 14 days after a security update ships, enforced by the MDM's software update declaration so the phone updates itself before the grace period ends. The system nags the phone, not the person.

## Enrollment ceremony, revised

The MDM step comes first, because the app's enrollment now requires a posture record to attach to.

1. Person opens the LAN enrollment URL on the phone, signs in to Authentik with a passkey, and installs the MDM profile. iOS asks them to approve management. Unsupervised, personal Apple Account, nothing else changes on the phone.
2. MDM pushes the ACME payload. The device generates a Secure Enclave key, obtains Apple's attestation, and step-ca issues the device certificate after verifying the chain, the nonce, and the attested properties.
3. MDM records the attested properties and DDM begins reporting. The gateway's device registry gets a posture record keyed by UDID.
4. Person opens CastleOps. The app presents the device certificate over mTLS, signs in with the passkey, generates the App Attest and presence keys, and submits the attestation as in ADR-003. The gateway links the app enrollment to the posture record.
5. Admin approval as in ADR-003. The approval screen now shows attested model and OS alongside person and group.
6. Active. Every request from here carries the device certificate, the access token, and an App Attest assertion, and is evaluated against posture.

## What the MDM is allowed to do

Managing a family member's personal phone is a trust decision and the guardrail is a published scope, not good intentions. The MDM configuration is limited to:

- The ACME payload with attestation
- A software update enforcement declaration
- The CastleOps app's managed configuration (gateway address, CA anchors)

It does not install other apps, restrict features, collect location, read installed apps beyond what enrollment requires, or hold an erase-device command in any operator role. The MDM server's role-based access is configured so no account can issue an erase, and the audit log ships to the same sink as the gateway. This scope is written down where the family can read it, and a change to it is a change to this ADR.

A person can remove the profile at any time. That is their right on an unsupervised personal device. The consequence is that the gateway denies the phone until they re-enroll, which is exactly the behavior we want.

## Dual track

The interface contract gains `DevicePosture`. Grayskull adapter: NanoMDM or Fleet with step-ca. Cisco adapter: Meraki Systems Manager as the MDM with Duo Device Health and Duo Trusted Endpoints feeding posture into the Duo policy decision. Verify Meraki's Managed Device Attestation support before relying on it in the demo. The demo story improves again: the same posture contract, backed by open source at home and by Meraki and Duo in the lab.

## Build order

A new step lands before the iOS app: MDM, step-ca with device-attest-01, enrollment of one phone, posture record visible at the gateway, and a Cedar deny on a fabricated stale attestation. Prove it from the terminal before the app exists. The app step then adds mTLS with the device certificate.

## Devil's advocate

- **You are now running an MDM for your family.** Yes, and the published scope above is what makes that acceptable. Anything beyond it is a new ADR and a new conversation at the dinner table.
- **The push certificate needs a vendor-signed CSR, which usually means a paid Apple Developer membership.** Use a dedicated household Apple Account for the push certificate itself. For the vendor signing step, either use a signing service such as the one the MicroMDM community maintains, or accept that the CastleOps app already requires a Developer membership for TestFlight and App Attest, and let that membership sign it. Whichever account signs, it is a household or Grayskull-personal account, never a Cisco one, and the MDM server never carries a Grayskull tenant identifier.
- **Apple rate-limits attestation, so posture is a snapshot.** Policy combines attested version within 7 days with DDM-reported version now. A device that lies in DDM still cannot lie to attestation, and a device that updated since attestation is not punished for it.
- **Attestation renewal needs Apple reachable.** Certificates are issued with a lifetime long enough that a multi-day outage does not lapse them, and renewal starts well before expiry. The action path still needs nothing from Apple.
- **Unsupervised means removable.** Correct, and supervision means Apple Business Manager or a wipe through Apple Configurator, which is the entanglement we are avoiding. Removal equals denial, and the household knows it.
- **There is still no attested jailbreak signal.** There is no such signal anywhere in Apple's platform. A compromised OS failing attestation is the strongest available evidence, and it is now required.
