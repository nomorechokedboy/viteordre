package authn

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"encore.app/internal/jwtauth"
)

// The handler is exercised with a token a real Better Auth instance issued, see
// internal/jwtauth/testdata/betterauth/README.md.
func TestAuthHandlerWithRealToken(t *testing.T) {
	dir := filepath.Join("..", "internal", "jwtauth", "testdata", "betterauth", "EdDSA")
	token := strings.TrimSpace(mustRead(t, filepath.Join(dir, "token.txt")))
	jwks := mustRead(t, filepath.Join(dir, "jwks.json"))
	var want struct{ UserID string }
	if err := json.Unmarshal([]byte(mustRead(t, filepath.Join(dir, "expect.json"))), &want); err != nil {
		t.Fatal(err)
	}

	raw, _ := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[1])
	var claims struct{ Iat int64 }
	_ = json.Unmarshal([]byte(strings.Replace(string(raw), `"iat"`, `"Iat"`, 1)), &claims)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(jwks)) }))
	defer srv.Close()
	v, err := jwtauth.NewVerifier(jwtauth.Config{
		JWKSURL: srv.URL, Issuer: "http://localhost:3000", Audience: "viteordre-api",
		Now: func() time.Time { return time.Unix(claims.Iat+60, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{verifier: v}

	uid, data, err := s.AuthHandler(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if string(uid) != want.UserID || data.Email != "owner@example.com" || data.Name != "Owner One" {
		t.Fatalf("uid=%q data=%+v", uid, data)
	}

	for name, bad := range map[string]string{"empty": "", "garbage": "a.b.c", "tampered": token[:len(token)-3] + "AAA"} {
		if uid, data, err := s.AuthHandler(context.Background(), bad); err == nil || uid != "" || data != nil {
			t.Errorf("%s: got uid=%q data=%v err=%v, want an error and no identity", name, uid, data, err)
		}
	}
}

func TestInitServiceDefaults(t *testing.T) {
	t.Setenv("AUTH_ISSUER", "")
	t.Setenv("AUTH_JWKS_URL", "")
	t.Setenv("AUTH_JWT_AUDIENCE", "")
	if s, err := initService(); err != nil || s.verifier == nil {
		t.Fatalf("initService with defaults: %v", err)
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
