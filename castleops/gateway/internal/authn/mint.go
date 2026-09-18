package authn

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"time"
)

// The mint helpers exist for the simulator and tests. They stand in for the
// LAN IdP (access tokens) and the phone (DPoP proofs). Production never mints
// here; Authentik issues tokens and the Secure Enclave signs proofs.

// MintES256 signs header and claims as a compact JWS with the given key.
func MintES256(priv *ecdsa.PrivateKey, header, claims map[string]any) (string, error) {
	if _, ok := header["alg"]; !ok {
		header["alg"] = "ES256"
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := b64e(hb) + "." + b64e(cb)
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest[:])
	if err != nil {
		return "", err
	}
	sig := append(pad32(r.Bytes()), pad32(s.Bytes())...)
	return signingInput + "." + b64e(sig), nil
}

// MintAccessToken produces an access token as the LAN IdP would.
func MintAccessToken(priv *ecdsa.PrivateKey, kid, issuer, audience, subject string, groups []string, now time.Time, ttl time.Duration, extra map[string]any) (string, error) {
	claims := map[string]any{
		"iss":       issuer,
		"aud":       audience,
		"sub":       subject,
		"groups":    groups,
		"iat":       now.Unix(),
		"exp":       now.Add(ttl).Unix(),
		"auth_time": now.Add(-time.Minute).Unix(),
		"jti":       randomID(),
	}
	for k, v := range extra {
		claims[k] = v
	}
	return MintES256(priv, map[string]any{"typ": "at+jwt", "kid": kid}, claims)
}

// MintDPoPProof produces a proof as the phone would, signed by priv.
func MintDPoPProof(priv *ecdsa.PrivateKey, method, url, accessToken string, now time.Time) (string, error) {
	sum := sha256.Sum256([]byte(accessToken))
	jwk := JWKFromPublic(&priv.PublicKey, "")
	header := map[string]any{
		"typ": "dpop+jwt",
		"jwk": map[string]any{"kty": jwk.Kty, "crv": jwk.Crv, "x": jwk.X, "y": jwk.Y},
	}
	claims := map[string]any{
		"htm": method,
		"htu": url,
		"iat": now.Unix(),
		"jti": randomID(),
		"ath": b64e(sum[:]),
	}
	return MintES256(priv, header, claims)
}

func randomID() string {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 96))
	return fmt.Sprintf("%x", n)
}
