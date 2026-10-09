package jwtauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
)

// maxJWKSBytes bounds how much of a key set response is read.
const maxJWKSBytes = 1 << 20

// jwk is one key of a JSON Web Key Set. Only the members needed to verify signatures are read.
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// key is a parsed public key.
type key struct {
	kid string
	alg string // the JWK's own alg member, empty when absent
	kty string
	crv string // OKP and EC keys
	pub any    // ed25519.PublicKey, *ecdsa.PublicKey or *rsa.PublicKey
}

func b64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

// parseKey converts a JWK into a key. Keys of a type this package does not verify, for example
// Ed448 or encryption keys, return ok=false and are skipped rather than failing the whole set.
func parseKey(j jwk) (k *key, ok bool, err error) {
	if j.Use != "" && j.Use != "sig" {
		return nil, false, nil
	}
	k = &key{kid: j.Kid, alg: j.Alg, kty: j.Kty, crv: j.Crv}
	switch j.Kty {
	case "OKP":
		if j.Crv != "Ed25519" {
			return nil, false, nil
		}
		x, err := b64(j.X)
		if err != nil || len(x) != ed25519.PublicKeySize {
			return nil, false, fmt.Errorf("key %q: invalid Ed25519 public key", j.Kid)
		}
		k.pub = ed25519.PublicKey(x)
	case "EC":
		var curve elliptic.Curve
		switch j.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, false, nil
		}
		xb, errX := b64(j.X)
		yb, errY := b64(j.Y)
		if errX != nil || errY != nil {
			return nil, false, fmt.Errorf("key %q: invalid EC coordinates", j.Kid)
		}
		x, y := new(big.Int).SetBytes(xb), new(big.Int).SetBytes(yb)
		// IsOnCurve is the check for a point sent by an untrusted party. The deprecation note
		// on it concerns custom curves, which are not used here.
		if !curve.IsOnCurve(x, y) {
			return nil, false, fmt.Errorf("key %q: point is not on curve %s", j.Kid, j.Crv)
		}
		k.pub = &ecdsa.PublicKey{Curve: curve, X: x, Y: y}
	case "RSA":
		nb, errN := b64(j.N)
		eb, errE := b64(j.E)
		if errN != nil || errE != nil {
			return nil, false, fmt.Errorf("key %q: invalid RSA parameters", j.Kid)
		}
		n, e := new(big.Int).SetBytes(nb), new(big.Int).SetBytes(eb)
		if n.BitLen() < 2048 {
			return nil, false, fmt.Errorf("key %q: RSA modulus is shorter than 2048 bits", j.Kid)
		}
		if !e.IsInt64() || e.Int64() < 3 || e.Int64() > 1<<31-1 || e.Bit(0) == 0 {
			return nil, false, fmt.Errorf("key %q: invalid RSA exponent", j.Kid)
		}
		k.pub = &rsa.PublicKey{N: n, E: int(e.Int64())}
	default:
		return nil, false, nil
	}
	return k, true, nil
}

// parseJWKS reads a key set into a map by key id. Keys without an id are ignored, since a token
// is matched to its key by id. Individual malformed keys are skipped, a set that holds no usable
// key at all is an error.
func parseJWKS(data []byte) (map[string]*key, error) {
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("parse key set: %w", err)
	}
	out := make(map[string]*key, len(set.Keys))
	var firstErr error
	for _, j := range set.Keys {
		if j.Kid == "" {
			continue
		}
		k, ok, err := parseKey(j)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if ok {
			out[j.Kid] = k
		}
	}
	if len(out) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, fmt.Errorf("key set holds no usable signing key")
	}
	return out, nil
}

// fetchKeys downloads and parses the key set.
func fetchKeys(ctx context.Context, client *http.Client, url string) (map[string]*key, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch key set: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("fetch key set: %s answered %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSBytes))
	if err != nil {
		return nil, fmt.Errorf("read key set: %w", err)
	}
	return parseJWKS(data)
}
