// Package jwtauth verifies the JSON Web Tokens that the web app (Better Auth, jwt plugin) issues
// for calls to this API. Keys come from the web app's JWKS endpoint and are cached, so a request
// needs no network call. Only the standard library is used.
package jwtauth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	_ "crypto/sha256" // register the hashes used below
	_ "crypto/sha512"
	"encoding/asn1"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	// ErrInvalidToken means the token is malformed, expired, signed by an unknown key, or not
	// meant for this API. The caller should answer 401.
	ErrInvalidToken = errors.New("jwtauth: invalid token")
	// ErrKeysUnavailable means the key set could not be fetched and no cached key can decide.
	// The caller should answer 503, retrying with a new token does not help.
	ErrKeysUnavailable = errors.New("jwtauth: signing keys unavailable")
)

// maxTokenBytes bounds the work an attacker can cause with one request.
const maxTokenBytes = 8 << 10

// Config configures a Verifier.
type Config struct {
	// JWKSURL is where the signing keys are published, for Better Auth <base URL>/api/auth/jwks.
	JWKSURL string
	// Issuer is the exact value required in the iss claim.
	Issuer string
	// Audience must appear in the aud claim.
	Audience string

	// Leeway tolerates clock differences when checking exp and nbf. Default 30 seconds.
	Leeway time.Duration
	// CacheTTL is how long the key set is trusted before it is fetched again. Keys the web app
	// rotates away stay usable for at most this long. Default 1 hour.
	CacheTTL time.Duration
	// MinRefreshInterval limits how often the key set is fetched, however many tokens with
	// unknown key ids arrive. Default 10 seconds.
	MinRefreshInterval time.Duration
	// HTTPClient fetches the key set. Default has a 5 second timeout.
	HTTPClient *http.Client
	// Now is the clock. Default time.Now.
	Now func() time.Time
}

// Claims are the verified contents of a token.
type Claims struct {
	Subject       string // the user id
	Email         string
	Name          string
	EmailVerified bool
	IssuedAt      time.Time
	ExpiresAt     time.Time
	KeyID         string
}

// Verifier checks tokens. It is safe for concurrent use.
type Verifier struct {
	cfg Config

	mu          sync.RWMutex
	keys        map[string]*key
	fetchedAt   time.Time // last successful fetch
	attemptedAt time.Time // last fetch, successful or not
	lastErr     error     // error of the last fetch, nil when it succeeded

	refreshMu sync.Mutex
}

// NewVerifier validates cfg and returns a Verifier. It does not contact the key server, the
// first token triggers the first fetch.
func NewVerifier(cfg Config) (*Verifier, error) {
	switch {
	case cfg.JWKSURL == "":
		return nil, errors.New("jwtauth: JWKSURL is required")
	case cfg.Issuer == "":
		return nil, errors.New("jwtauth: Issuer is required")
	case cfg.Audience == "":
		return nil, errors.New("jwtauth: Audience is required")
	}
	if cfg.Leeway == 0 {
		cfg.Leeway = 30 * time.Second
	}
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = time.Hour
	}
	if cfg.MinRefreshInterval == 0 {
		cfg.MinRefreshInterval = 10 * time.Second
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Verifier{cfg: cfg}, nil
}

// algSpec says what a JWS algorithm needs from its key.
type algSpec struct {
	kty  string
	crv  string // empty for RSA
	hash crypto.Hash
	pss  bool // RSA only
}

// algorithms is an allowlist. "none" and the HMAC family are deliberately absent: with HMAC an
// attacker could sign with the public key as the shared secret.
var algorithms = map[string]algSpec{
	"EdDSA": {kty: "OKP", crv: "Ed25519"},
	"ES256": {kty: "EC", crv: "P-256", hash: crypto.SHA256},
	"ES384": {kty: "EC", crv: "P-384", hash: crypto.SHA384},
	"ES512": {kty: "EC", crv: "P-521", hash: crypto.SHA512},
	"RS256": {kty: "RSA", hash: crypto.SHA256},
	"RS384": {kty: "RSA", hash: crypto.SHA384},
	"RS512": {kty: "RSA", hash: crypto.SHA512},
	"PS256": {kty: "RSA", hash: crypto.SHA256, pss: true},
	"PS384": {kty: "RSA", hash: crypto.SHA384, pss: true},
	"PS512": {kty: "RSA", hash: crypto.SHA512, pss: true},
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidToken, fmt.Sprintf(format, a...))
}

// Verify checks the signature and the claims of token, which is the compact serialization
// without any "Bearer " prefix.
func (v *Verifier) Verify(ctx context.Context, token string) (*Claims, error) {
	if token == "" || len(token) > maxTokenBytes {
		return nil, invalid("token is empty or too large")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, invalid("expected three dot-separated parts")
	}

	headerJSON, err := b64(parts[0])
	if err != nil {
		return nil, invalid("header is not base64url")
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, invalid("header is not JSON")
	}
	spec, ok := algorithms[header.Alg]
	if !ok {
		return nil, invalid("algorithm %q is not accepted", header.Alg)
	}
	if header.Kid == "" {
		return nil, invalid("header has no kid")
	}
	sig, err := b64(parts[2])
	if err != nil {
		return nil, invalid("signature is not base64url")
	}

	k, err := v.lookup(ctx, header.Kid)
	if err != nil {
		return nil, err
	}
	if k.kty != spec.kty || k.crv != spec.crv || (k.alg != "" && k.alg != header.Alg) {
		return nil, invalid("key %q cannot verify %s", k.kid, header.Alg)
	}
	if err := verifySignature(k, spec, []byte(parts[0]+"."+parts[1]), sig); err != nil {
		return nil, invalid("signature does not match")
	}

	payload, err := b64(parts[1])
	if err != nil {
		return nil, invalid("payload is not base64url")
	}
	claims, err := v.checkClaims(payload)
	if err != nil {
		return nil, err
	}
	claims.KeyID = k.kid
	return claims, nil
}

func verifySignature(k *key, spec algSpec, signed, sig []byte) error {
	if spec.kty == "OKP" {
		pub, ok := k.pub.(ed25519.PublicKey)
		if !ok || !ed25519.Verify(pub, signed, sig) {
			return errors.New("bad signature")
		}
		return nil
	}

	h := spec.hash.New()
	h.Write(signed)
	digest := h.Sum(nil)

	switch pub := k.pub.(type) {
	case *ecdsa.PublicKey:
		// JWS carries ECDSA signatures as the fixed-width concatenation r||s.
		size := (pub.Curve.Params().BitSize + 7) / 8
		if len(sig) != 2*size {
			return errors.New("bad signature length")
		}
		der, err := asn1.Marshal(struct{ R, S *big.Int }{
			new(big.Int).SetBytes(sig[:size]), new(big.Int).SetBytes(sig[size:]),
		})
		if err != nil || !ecdsa.VerifyASN1(pub, digest, der) {
			return errors.New("bad signature")
		}
		return nil
	case *rsa.PublicKey:
		if spec.pss {
			return rsa.VerifyPSS(pub, spec.hash, digest, sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		}
		return rsa.VerifyPKCS1v15(pub, spec.hash, digest, sig)
	}
	return errors.New("unsupported key")
}

// checkClaims validates the registered claims and extracts the ones the API uses.
func (v *Verifier) checkClaims(payload []byte) (*Claims, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, invalid("payload is not a JSON object")
	}

	now := v.cfg.Now()
	exp, ok := numericDate(raw["exp"])
	if !ok {
		return nil, invalid("exp is missing")
	}
	if !now.Before(exp.Add(v.cfg.Leeway)) {
		return nil, invalid("token expired")
	}
	if nbf, ok := numericDate(raw["nbf"]); ok && now.Add(v.cfg.Leeway).Before(nbf) {
		return nil, invalid("token is not valid yet")
	}
	if iss, _ := raw["iss"].(string); iss != v.cfg.Issuer {
		return nil, invalid("issuer %q is not accepted", iss)
	}
	if !hasAudience(raw["aud"], v.cfg.Audience) {
		return nil, invalid("audience does not include %q", v.cfg.Audience)
	}
	sub, _ := raw["sub"].(string)
	if sub == "" {
		return nil, invalid("sub is missing")
	}

	c := &Claims{Subject: sub, ExpiresAt: exp}
	c.Email, _ = raw["email"].(string)
	c.Name, _ = raw["name"].(string)
	c.EmailVerified, _ = raw["email_verified"].(bool)
	if iat, ok := numericDate(raw["iat"]); ok {
		c.IssuedAt = iat
	}
	return c, nil
}

func numericDate(v any) (time.Time, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return time.Time{}, false
	}
	f, err := n.Float64()
	if err != nil || f < 0 || f > 1<<40 {
		return time.Time{}, false
	}
	sec := int64(f)
	return time.Unix(sec, int64((f-float64(sec))*1e9)), true
}

func hasAudience(aud any, want string) bool {
	switch a := aud.(type) {
	case string:
		return a == want
	case []any:
		for _, x := range a {
			if s, ok := x.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

// lookup returns the key with the given id, fetching the key set when the cache is empty, older
// than CacheTTL, or does not know the id. Fetches are rate limited, so a flood of tokens with
// made-up key ids costs at most one request per MinRefreshInterval.
func (v *Verifier) lookup(ctx context.Context, kid string) (*key, error) {
	k, fresh := v.cached(kid)
	if k != nil && fresh {
		return k, nil
	}

	refreshErr := v.refresh(ctx)
	if k, _ = v.cached(kid); k != nil {
		return k, nil // found, possibly in a set that is stale because the refresh failed
	}
	if refreshErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeysUnavailable, refreshErr)
	}
	return nil, invalid("no signing key with id %q", kid)
}

func (v *Verifier) cached(kid string) (k *key, fresh bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.keys[kid], v.keys != nil && v.cfg.Now().Sub(v.fetchedAt) < v.cfg.CacheTTL
}

// refresh fetches the key set unless one was fetched within MinRefreshInterval. In that case it
// returns the outcome of that earlier fetch.
func (v *Verifier) refresh(ctx context.Context) error {
	v.refreshMu.Lock()
	defer v.refreshMu.Unlock()

	v.mu.RLock()
	recent := !v.attemptedAt.IsZero() && v.cfg.Now().Sub(v.attemptedAt) < v.cfg.MinRefreshInterval
	lastErr := v.lastErr
	v.mu.RUnlock()
	if recent {
		return lastErr
	}

	keys, err := fetchKeys(ctx, v.cfg.HTTPClient, v.cfg.JWKSURL)

	v.mu.Lock()
	defer v.mu.Unlock()
	v.attemptedAt = v.cfg.Now()
	v.lastErr = err
	if err == nil {
		v.keys = keys
		v.fetchedAt = v.attemptedAt
	}
	return err
}
