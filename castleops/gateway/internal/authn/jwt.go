// Package authn verifies tokens and per-request device bindings with the
// standard library only. ES256 is the one algorithm accepted; there is no
// algorithm negotiation and no "none".
package authn

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/contract"
)

// JWK is the subset of RFC 7517 needed for P-256 public keys.
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Kid string `json:"kid,omitempty"`
}

// PublicKey converts the JWK to an ECDSA public key.
func (k JWK) PublicKey() (*ecdsa.PublicKey, error) {
	if k.Kty != "EC" || k.Crv != "P-256" {
		return nil, fmt.Errorf("unsupported key type %s/%s", k.Kty, k.Crv)
	}
	xb, err := b64d(k.X)
	if err != nil {
		return nil, err
	}
	yb, err := b64d(k.Y)
	if err != nil {
		return nil, err
	}
	pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(xb), Y: new(big.Int).SetBytes(yb)}
	if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
		return nil, errors.New("point not on curve")
	}
	return pub, nil
}

// Thumbprint is the RFC 7638 JWK thumbprint, base64url of sha256 over the
// canonical members in lexicographic order.
func (k JWK) Thumbprint() string {
	canon := fmt.Sprintf(`{"crv":%q,"kty":%q,"x":%q,"y":%q}`, k.Crv, k.Kty, k.X, k.Y)
	sum := sha256.Sum256([]byte(canon))
	return b64e(sum[:])
}

// JWKFromPublic builds a JWK from an ECDSA P-256 public key.
func JWKFromPublic(pub *ecdsa.PublicKey, kid string) JWK {
	return JWK{
		Kty: "EC", Crv: "P-256", Kid: kid,
		X: b64e(pad32(pub.X.Bytes())),
		Y: b64e(pad32(pub.Y.Bytes())),
	}
}

// KeySet resolves kid to a public key. A static file-backed set is enough
// for the LAN IdP; a JWKS URL fetcher is a drop-in later.
type KeySet interface {
	Key(kid string) (*ecdsa.PublicKey, error)
}

// StaticKeySet is a fixed kid -> key map.
type StaticKeySet map[string]*ecdsa.PublicKey

// Key implements KeySet.
func (s StaticKeySet) Key(kid string) (*ecdsa.PublicKey, error) {
	k, ok := s[kid]
	if !ok {
		return nil, fmt.Errorf("unknown kid %q", kid)
	}
	return k, nil
}

// Verified is a parsed and signature-checked compact JWS.
type Verified struct {
	Header map[string]any
	Claims map[string]any
}

// VerifyES256 checks a compact JWS with ES256, resolving the key through keyFor.
// The header is parsed before keyFor is called so an embedded jwk can be used.
func VerifyES256(token string, keyFor func(header map[string]any) (*ecdsa.PublicKey, error)) (Verified, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Verified{}, errors.New("token is not a compact JWS")
	}
	hb, err := b64d(parts[0])
	if err != nil {
		return Verified{}, fmt.Errorf("header: %w", err)
	}
	var header map[string]any
	if err := json.Unmarshal(hb, &header); err != nil {
		return Verified{}, fmt.Errorf("header: %w", err)
	}
	if alg, _ := header["alg"].(string); alg != "ES256" {
		return Verified{}, fmt.Errorf("alg %q is not ES256", header["alg"])
	}
	pub, err := keyFor(header)
	if err != nil {
		return Verified{}, err
	}
	sig, err := b64d(parts[2])
	if err != nil || len(sig) != 64 {
		return Verified{}, errors.New("signature is not a 64-byte P-256 signature")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, digest[:], r, s) {
		return Verified{}, errors.New("signature verification failed")
	}
	pb, err := b64d(parts[1])
	if err != nil {
		return Verified{}, fmt.Errorf("payload: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(pb, &claims); err != nil {
		return Verified{}, fmt.Errorf("payload: %w", err)
	}
	return Verified{Header: header, Claims: claims}, nil
}

// TokenVerifier is the IdentityProvider adapter for a LAN OIDC issuer such as
// Authentik. It validates access tokens and maps claims to a PrincipalAssertion.
type TokenVerifier struct {
	Issuer   string
	Audience string
	Keys     KeySet
	Now      func() time.Time
	Skew     time.Duration
}

func (v *TokenVerifier) Name() string        { return "oidc-token-verifier" }
func (v *TokenVerifier) Tier() contract.Tier { return contract.TierLAN }

// VerifyAccessToken implements contract.IdentityProvider.
func (v *TokenVerifier) VerifyAccessToken(_ context.Context, token string) (contract.PrincipalAssertion, error) {
	now := v.now()
	ver, err := VerifyES256(token, func(h map[string]any) (*ecdsa.PublicKey, error) {
		kid, _ := h["kid"].(string)
		if kid == "" {
			return nil, errors.New("access token has no kid")
		}
		return v.Keys.Key(kid)
	})
	if err != nil {
		return contract.PrincipalAssertion{}, contract.Deny(contract.StageToken, "%v", err)
	}
	c := ver.Claims
	if iss, _ := c["iss"].(string); iss != v.Issuer {
		return contract.PrincipalAssertion{}, contract.Deny(contract.StageToken, "issuer mismatch")
	}
	if !audienceContains(c["aud"], v.Audience) {
		return contract.PrincipalAssertion{}, contract.Deny(contract.StageToken, "audience mismatch")
	}
	exp := unixTime(c["exp"])
	iat := unixTime(c["iat"])
	if exp.IsZero() || !now.Before(exp.Add(v.skew())) {
		return contract.PrincipalAssertion{}, contract.Deny(contract.StageToken, "token expired")
	}
	if !iat.IsZero() && iat.After(now.Add(v.skew())) {
		return contract.PrincipalAssertion{}, contract.Deny(contract.StageToken, "token issued in the future")
	}
	sub, _ := c["sub"].(string)
	if sub == "" {
		return contract.PrincipalAssertion{}, contract.Deny(contract.StageToken, "token has no subject")
	}

	pa := contract.PrincipalAssertion{
		Subject:    sub,
		Groups:     stringSlice(c["groups"]),
		IssuedAt:   iat,
		ExpiresAt:  exp,
		AuthTime:   unixTime(c["auth_time"]),
		ActorChain: actorChain(c["act"]),
	}
	if cnf, ok := c["cnf"].(map[string]any); ok {
		if jkt, _ := cnf["jkt"].(string); jkt != "" {
			pa.Confirmation = &contract.Confirmation{JKT: jkt}
		}
	}
	if el, ok := c["elevation"].(map[string]any); ok {
		pa.Elevation = &contract.Elevation{
			Tool:          str(el["tool"]),
			ArgsHash:      str(el["args_hash"]),
			TransactionID: str(el["transaction_id"]),
			Approver:      str(el["approver"]),
		}
	}
	return pa, nil
}

func (v *TokenVerifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func (v *TokenVerifier) skew() time.Duration {
	if v.Skew == 0 {
		return 30 * time.Second
	}
	return v.Skew
}

// actorChain flattens the RFC 8693 nested act claim, outermost first.
func actorChain(act any) []string {
	out := []string{}
	for act != nil {
		m, ok := act.(map[string]any)
		if !ok {
			break
		}
		if s, _ := m["sub"].(string); s != "" {
			out = append(out, s)
		}
		act = m["act"]
	}
	return out
}

func audienceContains(aud any, want string) bool {
	switch a := aud.(type) {
	case string:
		return a == want
	case []any:
		for _, x := range a {
			if s, _ := x.(string); s == want {
				return true
			}
		}
	}
	return false
}

func unixTime(v any) time.Time {
	f, ok := v.(float64)
	if !ok || f <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(f), 0).UTC()
}

func stringSlice(v any) []string {
	out := []string{}
	if arr, ok := v.([]any); ok {
		for _, x := range arr {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func str(v any) string { s, _ := v.(string); return s }

func b64d(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }
func b64e(b []byte) string          { return base64.RawURLEncoding.EncodeToString(b) }

func pad32(b []byte) []byte {
	if len(b) >= 32 {
		return b
	}
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}
