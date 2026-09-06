# ADR-003: Treating the phone as a proper Apple identity

Status: accepted, amended by [ADR-004](adr-004-managed-device-attestation.md), which makes attested device posture a requirement rather than the upgrade path described below.

## Decision

A family member's identity in CastleOps is the person, authenticated with a passkey, on a device that proves it is a genuine iPhone running the genuine CastleOps app, with Face ID as the step-up for anything that changes state. Each of those three facts comes from an Apple primitive and is verified on the LAN.

| Fact the gateway needs | Apple primitive | Where it is verified | Tier |
|---|---|---|---|
| This is Gary, Stephanie, or Jaxon | Passkey (WebAuthn), synced by iCloud Keychain, unlocked by Face ID | Authentik on the LAN | 0 on the phone, 1 at the IdP. iCloud Keychain sync is end-to-end encrypted, so it is opaque Tier 2 transit. |
| This is a real iPhone running our signed app | App Attest key in the Secure Enclave, attested by Apple to our team ID and bundle ID | Gateway, offline, against Apple's App Attest root | 1. The attestation object is verified locally. Apple's receipt and risk-metric service is not used. |
| This request came from that device, now | App Attest assertion over the request, signed by the attested key, counter must advance | Gateway, per request | 1 |
| A human is present and consenting to a write | A second Secure Enclave key whose use requires current biometry | Gateway checks which key signed | 1 |
| This device is still trusted | Device record in the gateway, admin-approved at enrollment, revocable in one step | Gateway, per request | 1 |

Agents remain Non-Human Identities in Authentik, reached by token exchange, exactly as in ADR-001. Apple has no agent identity primitive and none is needed there.

## What we rejected

**Sign in with Apple as the upstream IdP.** It puts Apple's identity servers in the authentication path, so a login fails when the internet is down and Apple learns every sign-in to the app. Passkeys give the same Face ID experience against Authentik directly, and the LAN keeps working alone.

**Plain DPoP with an app-generated key.** It binds the token to a key, but nothing proves the key lives in a Secure Enclave on a real device in our app. App Attest proves all three and still gives sender-constrained requests. The DPoP shape from the design review is kept; the key underneath it becomes the attested key.

**MDM as the first move.** Superseded. ADR-004 makes attested posture a requirement and adopts an unsupervised, self-hosted MDM with a minimal payload set and no Apple Business Manager, which avoids the Grayskull entanglement this paragraph originally objected to.

**DeviceCheck.** Requires Apple's server on every query. Excluded.

## Enrollment ceremony

Enrollment happens once per device and produces a device record the gateway consults on every request.

1. The person signs in to Authentik from the app and registers a passkey. Face ID unlocks it. The passkey syncs through iCloud Keychain to their other Apple devices, encrypted end to end.
2. The app generates two Secure Enclave keys. The session key is an App Attest key. The presence key is a P-256 key with an access control of `biometryCurrentSet`, so it cannot sign without Face ID succeeding right then.
3. The app asks the gateway for a one-time nonce and calls `attestKey`. Apple returns an attestation object chaining to the App Attest root and binding the key to our team and bundle ID.
4. The gateway verifies the chain offline, checks the nonce, the app identifier, and the environment, and stores the public key with a counter of zero, alongside the presence key's public key and the subject from the Authentik token.
5. The gateway holds the device as pending and notifies the admin. Gary sees the person, the device model, and the enrollment time, and approves with Face ID on his own phone. Stephanie can approve a device for Jaxon under the same secondary-approver rule as break-glass.
6. The device record becomes active. Until then every request from it is denied.

## Per-request binding

Every call to the gateway carries the Authentik access token and an App Attest assertion. The assertion is a signature by the attested key over a client-data hash the gateway can reconstruct from the request itself.

```
clientData = sha256( method | url | sha256(body) | sha256(accessToken) | serverNonce )
```

The gateway checks, in order: the device record is active; the signature verifies against the stored key; the counter is greater than the last one seen; the nonce is fresh; the token subject matches the device's enrolled subject; then Cedar decides. A stolen token without the device is useless. A cloned assertion replays into a counter check and fails. A lost phone is handled by marking its record revoked, which kills every request from it on the next call with no token revocation dance.

Reads sign with the session key so Siri and background intents work without Face ID. Writes and elevation approvals sign with the presence key, so the Secure Enclave itself refuses to produce the signature unless the person is looking at the phone. That is the step-up, and the gateway can tell which key signed.

## The household as a group

Apple Family Sharing has no API a self-hosted IdP can read, so the family structure is mirrored in Authentik groups by hand: `adults`, `minors`, `approvers`. Jaxon's child Apple Account and Screen Time remain Apple's concern. The gateway's Cedar policies reference the Authentik groups, and the time-of-day condition for `minors` from ADR-001 lives there. Two rules keep the mirror honest: a new device cannot be enrolled to a subject that is not in a group, and the admin approval screen shows the group the device will inherit.

## Recovery

- **Lost or replaced phone.** Revoke the device record. The passkey still exists in iCloud Keychain, so the new phone signs in with Face ID and runs the enrollment ceremony again. Gary approves it.
- **iCloud Keychain off.** The passkey is device-bound and dies with the phone. Recovery is a new passkey registered through an admin-initiated enrollment link on the LAN, approved the same way.
- **Gary's phone is the one lost.** Stephanie is a member of `approvers` for device enrollment as well as break-glass, and the LAN-only mTLS admin path from ADR-001 still exists for the case where no approver phone is available.
- **App Attest key rotation.** Re-run the ceremony. Keys do not need scheduled rotation; the counter and the device record carry the security.

## Swift skeleton

Skeleton, not a drop-in. Verify API names against the current SDK.

```swift
import DeviceCheck
import CryptoKit
import LocalAuthentication
import Security

actor DeviceIdentity {
    static let shared = DeviceIdentity()
    private let attest = DCAppAttestService.shared
    private var sessionKeyID: String? = Keychain.string("attest.keyID")

    // Enrollment, once per device
    func enroll(nonce: Data) async throws -> (attestation: Data, presencePublicKey: Data) {
        guard attest.isSupported else { throw IdentityError.attestUnsupported }
        let keyID = try await attest.generateKey()
        let attestation = try await attest.attestKey(keyID, clientDataHash: Data(SHA256.hash(data: nonce)))
        Keychain.set("attest.keyID", keyID)
        sessionKeyID = keyID
        let presence = try PresenceKey.createIfNeeded()
        return (attestation, presence.publicKeyRepresentation)
    }

    // Per request: session key for reads, presence key for writes and approvals
    func sign(method: String, url: URL, body: Data, accessToken: String, nonce: Data, presence: Bool) async throws -> String {
        var material = Data()
        material.append(method.data(using: .utf8)!)
        material.append(url.absoluteString.data(using: .utf8)!)
        material.append(Data(SHA256.hash(data: body)))
        material.append(Data(SHA256.hash(data: Data(accessToken.utf8))))
        material.append(nonce)
        let clientDataHash = Data(SHA256.hash(data: material))

        if presence {
            return try PresenceKey.sign(clientDataHash, reason: "Confirm this CastleOps change").base64EncodedString()
        }
        guard let keyID = sessionKeyID else { throw IdentityError.notEnrolled }
        return try await attest.generateAssertion(keyID, clientDataHash: clientDataHash).base64EncodedString()
    }
}

enum PresenceKey {
    static let tag = "com.grayskull.castleops.presence".data(using: .utf8)!

    static func createIfNeeded() throws -> SecureEnclave.P256.Signing.PrivateKey {
        if let existing = try load() { return existing }
        let access = SecAccessControlCreateWithFlags(
            nil, kSecAttrAccessibleWhenUnlockedThisDeviceOnly,
            [.privateKeyUsage, .biometryCurrentSet], nil)!
        let key = try SecureEnclave.P256.Signing.PrivateKey(accessControl: access)
        Keychain.setData("presence.key", key.dataRepresentation)
        return key
    }

    static func load() throws -> SecureEnclave.P256.Signing.PrivateKey? {
        guard let data = Keychain.data("presence.key") else { return nil }
        return try SecureEnclave.P256.Signing.PrivateKey(dataRepresentation: data)
    }

    static func sign(_ digest: Data, reason: String) throws -> Data {
        let context = LAContext()
        context.localizedReason = reason
        guard let key = try load() else { throw IdentityError.notEnrolled }
        // Secure Enclave enforces biometry here. No Face ID, no signature.
        return try key.signature(for: digest).rawRepresentation
    }
}

enum IdentityError: Error { case attestUnsupported, notEnrolled }
```

Gateway side, in prose: parse the attestation CBOR, walk the x5c chain to Apple's App Attest root, confirm the nonce in the credCert extension, confirm the RP ID hash equals `sha256(teamID.bundleID)`, confirm the counter is zero and the environment is production, store the credential public key. On each assertion, parse, verify the signature over `sha256(authenticatorData || clientDataHash)`, check the counter advanced, update it.

## Consequences

- The app is distributed through TestFlight so App Attest runs in its production environment. Development builds use the sandbox environment and the gateway keeps a separate allowlist for them.
- The gateway gains a device registry: subject, device name, attested public key, presence public key, counter, status, approver, timestamps. It is the revocation point.
- Authentik is configured as passkey-only for the family. No passwords exist to phish.
- The admin approval screen from break-glass gains a second use, device enrollment, so the approver experience is one pattern.
- Apple Watch, Mac, and iPad follow the same ceremony where App Attest is available on that platform. Where it is not, that device gets read-only access with the passkey alone and no presence key, and it says so in the device record.

## Devil's advocate

- **App Attest does not prove the OS is patched or the device is not jailbroken.** Correct. It proves genuine hardware, genuine app, and key residency. If attested posture becomes a requirement, the upgrade path is Managed Device Attestation through an MDM under Grayskull's Apple Business Manager, with the entanglement cost named above.
- **iCloud Keychain is Apple's cloud.** It is end-to-end encrypted and Apple documents that it cannot read synced passkeys. That is opaque Tier 2 by the ADR-001 definition. A family member may turn it off and accept the recovery path instead.
- **Face ID on every write is friction.** It is the same gesture as Apple Pay, and it is what makes "what you see is what you sign" true at the hardware level rather than in a UI.
- **Two keys is more to get wrong than one.** The presence key is what separates a background Siri read from a human-present write. One key would force a choice between Face ID on reads or no Face ID on writes. Two is the minimum.
- **The gateway now owns a device registry and a CBOR parser.** Yes. Use a maintained App Attest verification library rather than writing the parser, and treat the registry as the security-critical table it is: backed up with the IdP, restore-drilled with it.
