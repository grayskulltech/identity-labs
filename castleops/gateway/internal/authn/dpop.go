package authn

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/contract"
)

// DPoPBinding is the DeviceBinding implementation for RFC 9449 proofs. It is
// the development and non-Apple path; App Attest (ADR-003) is the Apple path.
// A proof is accepted only if it is signed by one of the two keys enrolled for
// the device: the session key the token is bound to, or the presence key.
type DPoPBinding struct {
	MaxAge time.Duration
	seen   sync.Map // jti -> expiry unix
}

func (d *DPoPBinding) Name() string        { return "dpop-binding" }
func (d *DPoPBinding) Tier() contract.Tier { return contract.TierLAN }

// Verify implements contract.DeviceBinding.
func (d *DPoPBinding) Verify(_ context.Context, in contract.BindingInput) (contract.BindingResult, error) {
	if in.Proof == "" {
		return contract.BindingResult{}, contract.Deny(contract.StageAttestation, "missing DPoP proof")
	}
	var jwk JWK
	ver, err := VerifyES256(in.Proof, func(h map[string]any) (*ecdsa.PublicKey, error) {
		if typ, _ := h["typ"].(string); typ != "dpop+jwt" {
			return nil, errors.New("proof typ is not dpop+jwt")
		}
		raw, ok := h["jwk"]
		if !ok {
			return nil, errors.New("proof has no jwk")
		}
		b, _ := json.Marshal(raw)
		if err := json.Unmarshal(b, &jwk); err != nil {
			return nil, err
		}
		return jwk.PublicKey()
	})
	if err != nil {
		return contract.BindingResult{}, contract.Deny(contract.StageAttestation, "proof: %v", err)
	}
	c := ver.Claims
	if htm, _ := c["htm"].(string); htm != in.Method {
		return contract.BindingResult{}, contract.Deny(contract.StageAttestation, "proof method mismatch")
	}
	if htu, _ := c["htu"].(string); htu != in.URL {
		return contract.BindingResult{}, contract.Deny(contract.StageAttestation, "proof URL mismatch")
	}
	iat := unixTime(c["iat"])
	if iat.IsZero() || in.Now.Sub(iat) > d.maxAge() || iat.Sub(in.Now) > 30*time.Second {
		return contract.BindingResult{}, contract.Deny(contract.StageAttestation, "proof is stale")
	}
	sum := sha256.Sum256([]byte(in.AccessToken))
	if ath, _ := c["ath"].(string); ath != b64e(sum[:]) {
		return contract.BindingResult{}, contract.Deny(contract.StageAttestation, "proof is not bound to this token")
	}
	jti, _ := c["jti"].(string)
	if jti == "" {
		return contract.BindingResult{}, contract.Deny(contract.StageAttestation, "proof has no jti")
	}
	if !d.remember(jti, in.Now) {
		return contract.BindingResult{}, contract.Deny(contract.StageAttestation, "proof replayed")
	}

	jkt := jwk.Thumbprint()
	switch jkt {
	case in.Device.Keys.PresenceKeyJKT:
		return contract.BindingResult{KeyThumbprint: jkt, Presence: true}, nil
	case in.Device.Keys.AttestKeyID:
		// For the DPoP path the enrolled session key's thumbprint is stored in
		// attest_key_id; the App Attest path stores Apple's key id there instead.
		return contract.BindingResult{KeyThumbprint: jkt, Presence: false}, nil
	}
	return contract.BindingResult{}, contract.Deny(contract.StageAttestation, "proof key is not enrolled for this device")
}

func (d *DPoPBinding) maxAge() time.Duration {
	if d.MaxAge == 0 {
		return 5 * time.Minute
	}
	return d.MaxAge
}

// remember records a jti and reports whether it was new. Entries expire after
// twice the proof max age; the sweep is opportunistic.
func (d *DPoPBinding) remember(jti string, now time.Time) bool {
	exp := now.Add(2 * d.maxAge()).Unix()
	if _, loaded := d.seen.LoadOrStore(jti, exp); loaded {
		return false
	}
	d.seen.Range(func(k, v any) bool {
		if e, ok := v.(int64); ok && e < now.Unix() {
			d.seen.Delete(k)
		}
		return true
	})
	return true
}
