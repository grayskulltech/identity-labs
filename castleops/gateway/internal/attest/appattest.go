// Package attest is the App Attest DeviceBinding (ADR-003). It is not yet
// implemented. The DPoP binding in authn is the working path today; this file
// fixes the interface and the verification steps so the swap is mechanical.
package attest

import (
	"context"

	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/contract"
)

// AppAttestBinding verifies App Attest assertions over the request.
//
// Verification steps, per ADR-003, in order:
//  1. Parse the assertion CBOR: signature, authenticatorData.
//  2. clientDataHash = sha256(method | url | sha256(body) | sha256(token) | nonce).
//  3. nonce = sha256(authenticatorData || clientDataHash); verify the signature
//     over nonce with the enrolled attested public key.
//  4. Parse the counter from authenticatorData; require counter > stored;
//     store the new counter (MemoryDevices.AdvanceCounter).
//  5. Require rpIdHash == sha256(teamID.bundleID).
//  6. Presence: the assertion is by the session key. A write carries a second
//     signature by the presence key over clientDataHash; verify that
//     separately and set Presence=true only when it validates.
//
// Use a maintained App Attest verification library for step 1 and the
// enrollment-time attestation chain walk rather than writing a CBOR parser here.
type AppAttestBinding struct {
	TeamID   string
	BundleID string
}

func (a *AppAttestBinding) Name() string        { return "app-attest-binding" }
func (a *AppAttestBinding) Tier() contract.Tier { return contract.TierLAN }

// Verify fails closed until implemented. Unavailable on the decision path is
// a deny, so wiring this adapter in before it is finished denies every request.
func (a *AppAttestBinding) Verify(_ context.Context, _ contract.BindingInput) (contract.BindingResult, error) {
	return contract.BindingResult{}, contract.Unavailable(contract.StageAttestation, "App Attest verification not implemented; see ADR-003")
}
