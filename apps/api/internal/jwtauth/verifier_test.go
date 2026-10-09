package jwtauth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testIssuer   = "http://localhost:3000"
	testAudience = "viteordre-api"
)

// ---- real Better Auth output ------------------------------------------------------------

// TestRealBetterAuthTokens verifies tokens and key sets that a real Better Auth instance produced
// for every signing algorithm it supports. See testdata/betterauth/README.md.
func TestRealBetterAuthTokens(t *testing.T) {
	for _, alg := range []string{"EdDSA", "ES256", "ES512", "RS256", "PS256"} {
		t.Run(alg, func(t *testing.T) {
			dir := filepath.Join("testdata", "betterauth", alg)
			token := strings.TrimSpace(readFile(t, filepath.Join(dir, "token.txt")))
			var want struct{ UserID, Email string }
			if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, "expect.json"))), &want); err != nil {
				t.Fatal(err)
			}
			jwksBody := readFile(t, filepath.Join(dir, "jwks.json"))

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(jwksBody))
			}))
			defer srv.Close()

			iat := payloadOf(t, token)["iat"].(float64)
			clock := time.Unix(int64(iat)+60, 0) // one minute after issue
			v := newTestVerifier(t, srv.URL, func(c *Config) { c.Now = func() time.Time { return clock } })

			c, err := v.Verify(context.Background(), token)
			if err != nil {
				t.Fatal(err)
			}
			if c.Subject != want.UserID || c.Email != want.Email || c.Name != "Owner One" || c.EmailVerified {
				t.Fatalf("claims = %+v, want user %s %s", c, want.UserID, want.Email)
			}
			if !c.ExpiresAt.After(clock) {
				t.Fatalf("expiry %v should be after %v", c.ExpiresAt, clock)
			}

			// The same token is rejected once it has expired, and when audience or issuer differ.
			late := newTestVerifier(t, srv.URL, func(c *Config) { c.Now = func() time.Time { return clock.Add(time.Hour) } })
			if _, err := late.Verify(context.Background(), token); !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("expired real token: %v", err)
			}
			other := newTestVerifier(t, srv.URL, func(c *Config) {
				c.Now = func() time.Time { return clock }
				c.Audience = "someone-else"
			})
			if _, err := other.Verify(context.Background(), token); !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("wrong audience: %v", err)
			}
		})
	}
}

// ---- synthetic tokens -------------------------------------------------------------------

type signer struct {
	kid  string
	alg  string
	sign func(signed []byte) []byte
	jwk  map[string]any
}

func ed25519Signer(kid string) signer {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	return signer{kid: kid, alg: "EdDSA",
		sign: func(m []byte) []byte { return ed25519.Sign(priv, m) },
		jwk:  map[string]any{"kty": "OKP", "crv": "Ed25519", "kid": kid, "alg": "EdDSA", "x": enc(pub)}}
}

func ecSigner(kid, alg string, curve elliptic.Curve, h crypto.Hash, crv string) signer {
	priv, _ := ecdsa.GenerateKey(curve, rand.Reader)
	size := (curve.Params().BitSize + 7) / 8
	return signer{kid: kid, alg: alg,
		sign: func(m []byte) []byte {
			hh := h.New()
			hh.Write(m)
			r, s, _ := ecdsa.Sign(rand.Reader, priv, hh.Sum(nil))
			out := make([]byte, 2*size)
			r.FillBytes(out[:size])
			s.FillBytes(out[size:])
			return out
		},
		jwk: map[string]any{"kty": "EC", "crv": crv, "kid": kid, "alg": alg,
			"x": enc(priv.X.FillBytes(make([]byte, size))), "y": enc(priv.Y.FillBytes(make([]byte, size)))}}
}

func rsaSigner(kid, alg string, pss bool, h crypto.Hash) signer {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	return signer{kid: kid, alg: alg,
		sign: func(m []byte) []byte {
			hh := h.New()
			hh.Write(m)
			var sig []byte
			if pss {
				sig, _ = rsa.SignPSS(rand.Reader, priv, h, hh.Sum(nil), &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
			} else {
				sig, _ = rsa.SignPKCS1v15(rand.Reader, priv, h, hh.Sum(nil))
			}
			return sig
		},
		jwk: map[string]any{"kty": "RSA", "kid": kid, "alg": alg,
			"n": enc(priv.N.Bytes()), "e": enc(big.NewInt(int64(priv.E)).Bytes())}}
}

func enc(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func jsonB64(v any) string {
	b, _ := json.Marshal(v)
	return enc(b)
}

// mint builds a signed token. mutate may change the claims before signing.
func (s signer) mint(now time.Time, mutate func(map[string]any)) string {
	claims := map[string]any{
		"sub": "user-1", "iss": testIssuer, "aud": testAudience,
		"iat": now.Unix(), "exp": now.Add(10 * time.Minute).Unix(),
		"email": "a@example.com", "name": "A", "email_verified": true,
	}
	if mutate != nil {
		mutate(claims)
	}
	signed := jsonB64(map[string]any{"alg": s.alg, "kid": s.kid}) + "." + jsonB64(claims)
	return signed + "." + enc(s.sign([]byte(signed)))
}

// jwksServer serves a mutable key set and counts requests.
type jwksServer struct {
	*httptest.Server
	mu    sync.Mutex
	keys  []map[string]any
	fail  bool
	count atomic.Int32
}

func newJWKSServer(t *testing.T, signers ...signer) *jwksServer {
	t.Helper()
	s := &jwksServer{}
	s.set(signers...)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.count.Add(1)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.fail {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": s.keys})
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *jwksServer) set(signers ...signer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = nil
	for _, sg := range signers {
		s.keys = append(s.keys, sg.jwk)
	}
}

func (s *jwksServer) setFail(f bool) { s.mu.Lock(); s.fail = f; s.mu.Unlock() }

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestVerifier(t *testing.T, jwksURL string, opts ...func(*Config)) *Verifier {
	t.Helper()
	cfg := Config{JWKSURL: jwksURL, Issuer: testIssuer, Audience: testAudience}
	for _, o := range opts {
		o(&cfg)
	}
	v, err := NewVerifier(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVerifyAllAlgorithms(t *testing.T) {
	signers := []signer{
		ed25519Signer("ed"),
		ecSigner("es256", "ES256", elliptic.P256(), crypto.SHA256, "P-256"),
		ecSigner("es384", "ES384", elliptic.P384(), crypto.SHA384, "P-384"),
		ecSigner("es512", "ES512", elliptic.P521(), crypto.SHA512, "P-521"),
		rsaSigner("rs256", "RS256", false, crypto.SHA256),
		rsaSigner("rs512", "RS512", false, crypto.SHA512),
		rsaSigner("ps256", "PS256", true, crypto.SHA256),
		rsaSigner("ps384", "PS384", true, crypto.SHA384),
	}
	srv := newJWKSServer(t, signers...)
	v := newTestVerifier(t, srv.URL)
	for _, s := range signers {
		t.Run(s.alg+"/"+s.kid, func(t *testing.T) {
			c, err := v.Verify(context.Background(), s.mint(time.Now(), nil))
			if err != nil {
				t.Fatal(err)
			}
			if c.Subject != "user-1" || c.Email != "a@example.com" || !c.EmailVerified || c.KeyID != s.kid {
				t.Fatalf("claims = %+v", c)
			}
		})
	}
	if n := srv.count.Load(); n != 1 {
		t.Fatalf("key set fetched %d times, want 1", n)
	}
}

func TestRejectedClaims(t *testing.T) {
	s := ed25519Signer("k1")
	srv := newJWKSServer(t, s)
	now := time.Now()
	v := newTestVerifier(t, srv.URL)

	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"expired", func(c map[string]any) { c["exp"] = now.Add(-time.Minute).Unix() }},
		{"missing exp", func(c map[string]any) { delete(c, "exp") }},
		{"exp is a string", func(c map[string]any) { c["exp"] = "9999999999" }},
		{"not yet valid", func(c map[string]any) { c["nbf"] = now.Add(time.Hour).Unix() }},
		{"wrong issuer", func(c map[string]any) { c["iss"] = "https://evil.example" }},
		{"missing issuer", func(c map[string]any) { delete(c, "iss") }},
		{"wrong audience", func(c map[string]any) { c["aud"] = "other-api" }},
		{"audience list without ours", func(c map[string]any) { c["aud"] = []string{"a", "b"} }},
		{"missing audience", func(c map[string]any) { delete(c, "aud") }},
		{"missing subject", func(c map[string]any) { delete(c, "sub") }},
		{"empty subject", func(c map[string]any) { c["sub"] = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := v.Verify(context.Background(), s.mint(now, tt.mutate))
			if !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("got %v, want ErrInvalidToken", err)
			}
		})
	}

	t.Run("audience list with ours", func(t *testing.T) {
		if _, err := v.Verify(context.Background(), s.mint(now, func(c map[string]any) { c["aud"] = []string{"x", testAudience} })); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("leeway", func(t *testing.T) {
		// expired 10 seconds ago, still inside the default 30 second leeway
		if _, err := v.Verify(context.Background(), s.mint(now, func(c map[string]any) { c["exp"] = now.Add(-10 * time.Second).Unix() })); err != nil {
			t.Fatal(err)
		}
		if _, err := v.Verify(context.Background(), s.mint(now, func(c map[string]any) { c["exp"] = now.Add(-time.Minute).Unix() })); err == nil {
			t.Fatal("a minute past exp must be rejected")
		}
	})
}

func TestRejectedTokens(t *testing.T) {
	s := ed25519Signer("k1")
	other := ed25519Signer("k1") // same kid, different key
	srv := newJWKSServer(t, s)
	v := newTestVerifier(t, srv.URL)
	now := time.Now()
	good := s.mint(now, nil)
	parts := strings.Split(good, ".")

	// A token signed with HMAC using the public key bytes as the secret, the classic algorithm
	// confusion attack.
	pubBytes, _ := base64.RawURLEncoding.DecodeString(s.jwk["x"].(string))
	hsHead := jsonB64(map[string]any{"alg": "HS256", "kid": "k1"})
	hsBody := jsonB64(map[string]any{"sub": "u", "iss": testIssuer, "aud": testAudience, "exp": now.Add(time.Hour).Unix()})
	mac := hmac.New(sha256.New, pubBytes)
	mac.Write([]byte(hsHead + "." + hsBody))
	hs256 := hsHead + "." + hsBody + "." + enc(mac.Sum(nil))

	noneHead := jsonB64(map[string]any{"alg": "none", "kid": "k1"})
	noKid := jsonB64(map[string]any{"alg": "EdDSA"}) + "." + parts[1] + "." + parts[2]
	tamperedBody := parts[0] + "." + jsonB64(map[string]any{"sub": "admin", "iss": testIssuer, "aud": testAudience, "exp": now.Add(time.Hour).Unix()}) + "." + parts[2]

	tests := map[string]string{
		"empty":               "",
		"one part":            "abc",
		"two parts":           parts[0] + "." + parts[1],
		"four parts":          good + ".x",
		"not base64":          "!!!." + parts[1] + "." + parts[2],
		"header not json":     enc([]byte("nope")) + "." + parts[1] + "." + parts[2],
		"alg none":            noneHead + "." + parts[1] + ".",
		"alg HS256 confusion": hs256,
		"no kid":              noKid,
		"unknown kid":         signedWithKid(other, "nope", now),
		"wrong key":           other.mint(now, nil),
		"tampered payload":    tamperedBody,
		"tampered signature":  parts[0] + "." + parts[1] + "." + flip(parts[2]),
		"too large":           strings.Repeat("a", maxTokenBytes+1),
	}
	for name, tok := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := v.Verify(context.Background(), tok); !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("got %v, want ErrInvalidToken", err)
			}
		})
	}
}

func signedWithKid(s signer, kid string, now time.Time) string {
	s.kid = kid
	return s.mint(now, nil)
}

func flip(sig string) string {
	b := []byte(sig)
	if b[0] == 'A' {
		b[0] = 'B'
	} else {
		b[0] = 'A'
	}
	return string(b)
}

func TestKeyAndAlgorithmMismatch(t *testing.T) {
	ed := ed25519Signer("ed")
	rs := rsaSigner("rs", "RS256", false, crypto.SHA256)
	p256 := ecSigner("p256", "ES256", elliptic.P256(), crypto.SHA256, "P-256")
	p384 := ecSigner("p384", "ES384", elliptic.P384(), crypto.SHA384, "P-384")
	srv := newJWKSServer(t, ed, rs, p256, p384)
	v := newTestVerifier(t, srv.URL)
	now := time.Now()

	// Header claims an algorithm that does not fit the key the kid points at.
	cases := map[string]signer{
		"EdDSA header on an RSA key":     {kid: "rs", alg: "EdDSA", sign: ed.sign},
		"RS256 header on an Ed25519 key": {kid: "ed", alg: "RS256", sign: rs.sign},
		"ES256 header on a P-384 key":    {kid: "p384", alg: "ES256", sign: p384.sign},
		"ES384 header on a P-256 key":    {kid: "p256", alg: "ES384", sign: p256.sign},
		"PS256 header on an RS256 JWK":   {kid: "rs", alg: "PS256", sign: rs.sign},
		"ES256 header on an Ed25519 key": {kid: "ed", alg: "ES256", sign: p256.sign},
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := v.Verify(context.Background(), s.mint(now, nil)); !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("got %v, want ErrInvalidToken", err)
			}
		})
	}
}

// ---- key set behaviour ------------------------------------------------------------------

func TestKeyRotationRefetchesOnUnknownKid(t *testing.T) {
	old, fresh := ed25519Signer("old"), ed25519Signer("new")
	srv := newJWKSServer(t, old)
	clk := &clock{t: time.Now()}
	v := newTestVerifier(t, srv.URL, func(c *Config) { c.Now = clk.Now })

	if _, err := v.Verify(context.Background(), old.mint(clk.Now(), nil)); err != nil {
		t.Fatal(err)
	}
	// The web app rotates its key. A token with the new kid arrives after the rate-limit window.
	srv.set(old, fresh)
	clk.Advance(11 * time.Second)
	if _, err := v.Verify(context.Background(), fresh.mint(clk.Now(), nil)); err != nil {
		t.Fatalf("token signed by the rotated-in key: %v", err)
	}
	if n := srv.count.Load(); n != 2 {
		t.Fatalf("fetches = %d, want 2", n)
	}
}

func TestUnknownKidFloodIsRateLimited(t *testing.T) {
	known := ed25519Signer("known")
	srv := newJWKSServer(t, known)
	clk := &clock{t: time.Now()}
	v := newTestVerifier(t, srv.URL, func(c *Config) { c.Now = clk.Now })

	if _, err := v.Verify(context.Background(), known.mint(clk.Now(), nil)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		_, err := v.Verify(context.Background(), signedWithKid(known, fmt.Sprintf("fake-%d", i), clk.Now()))
		if !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("got %v", err)
		}
	}
	if n := srv.count.Load(); n > 2 {
		t.Fatalf("a flood of unknown key ids caused %d key set fetches, want at most 2", n)
	}
}

func TestKeySetOutage(t *testing.T) {
	s := ed25519Signer("k1")
	srv := newJWKSServer(t, s)
	clk := &clock{t: time.Now()}
	v := newTestVerifier(t, srv.URL, func(c *Config) { c.Now = clk.Now })

	// Outage before anything is cached: 503 material, not a rejection of the token.
	srv.setFail(true)
	if _, err := v.Verify(context.Background(), s.mint(clk.Now(), nil)); !errors.Is(err, ErrKeysUnavailable) || errors.Is(err, ErrInvalidToken) {
		t.Fatalf("got %v, want ErrKeysUnavailable", err)
	}

	// Recovery after the rate-limit window.
	srv.setFail(false)
	clk.Advance(11 * time.Second)
	if _, err := v.Verify(context.Background(), s.mint(clk.Now(), nil)); err != nil {
		t.Fatal(err)
	}

	// Outage after the cache went stale: the cached key keeps working.
	srv.setFail(true)
	clk.Advance(2 * time.Hour)
	if _, err := v.Verify(context.Background(), s.mint(clk.Now(), nil)); err != nil {
		t.Fatalf("stale keys should still verify during an outage: %v", err)
	}
}

func TestStaleCacheIsRefreshed(t *testing.T) {
	s := ed25519Signer("k1")
	srv := newJWKSServer(t, s)
	clk := &clock{t: time.Now()}
	v := newTestVerifier(t, srv.URL, func(c *Config) { c.Now = clk.Now })

	if _, err := v.Verify(context.Background(), s.mint(clk.Now(), nil)); err != nil {
		t.Fatal(err)
	}
	// The key is revoked at the source, once the cache is older than the TTL it must stop working.
	srv.set(ed25519Signer("other"))
	clk.Advance(2 * time.Hour)
	if _, err := v.Verify(context.Background(), s.mint(clk.Now(), nil)); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("revoked key: got %v, want ErrInvalidToken", err)
	}
}

func TestParseJWKSSkipsUnsupportedKeys(t *testing.T) {
	good := ed25519Signer("good")
	set, _ := json.Marshal(map[string]any{"keys": []any{
		map[string]any{"kty": "OKP", "crv": "Ed448", "kid": "ed448", "x": "AAAA"},
		map[string]any{"kty": "oct", "kid": "sym", "k": "AAAA"},
		map[string]any{"kty": "EC", "crv": "P-256", "kid": "enc", "use": "enc", "x": "AA", "y": "AA"},
		map[string]any{"kty": "OKP", "crv": "Ed25519", "x": good.jwk["x"]}, // no kid
		good.jwk,
	}})
	keys, err := parseJWKS(set)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys["good"] == nil {
		t.Fatalf("keys = %v, want only the usable one", keys)
	}

	for name, body := range map[string]string{
		"empty set":       `{"keys":[]}`,
		"not json":        `<html>`,
		"point off curve": `{"keys":[{"kty":"EC","crv":"P-256","kid":"x","x":"AQ","y":"AQ"}]}`,
		"short rsa":       `{"keys":[{"kty":"RSA","kid":"x","n":"AQAB","e":"AQAB"}]}`,
	} {
		if _, err := parseJWKS([]byte(body)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestConcurrentVerifyAndRotation(t *testing.T) {
	a, b := ed25519Signer("a"), ed25519Signer("b")
	srv := newJWKSServer(t, a)
	v := newTestVerifier(t, srv.URL, func(c *Config) { c.MinRefreshInterval = time.Millisecond })

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s := a
				if i%2 == 1 {
					s = b
				}
				// Tokens from b succeed only after the rotation below, either outcome is fine,
				// the race detector is what this test is for.
				_, _ = v.Verify(context.Background(), s.mint(time.Now(), nil))
				if i == 0 && j == 10 {
					srv.set(a, b)
				}
			}
		}(i)
	}
	wg.Wait()
	time.Sleep(5 * time.Millisecond)
	if _, err := v.Verify(context.Background(), b.mint(time.Now(), nil)); err != nil {
		t.Fatalf("after rotation: %v", err)
	}
}

func TestNewVerifierRequiresSettings(t *testing.T) {
	for name, cfg := range map[string]Config{
		"no url":      {Issuer: "i", Audience: "a"},
		"no issuer":   {JWKSURL: "u", Audience: "a"},
		"no audience": {JWKSURL: "u", Issuer: "i"},
	} {
		if _, err := NewVerifier(cfg); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// ---- helpers ----------------------------------------------------------------------------

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func payloadOf(t *testing.T, token string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[1])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
