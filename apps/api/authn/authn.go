// Package authn authenticates API requests. The web app (Better Auth) signs the user in and
// issues a short-lived JWT; this package verifies it and tells Encore who the caller is.
//
// Configuration, all optional in development:
//
//	AUTH_ISSUER        base URL of the web app, must equal BETTER_AUTH_URL there. Default http://localhost:3000
//	AUTH_JWKS_URL      where the signing keys are published. Default <AUTH_ISSUER>/api/auth/jwks
//	AUTH_JWT_AUDIENCE  audience the tokens are issued for, must equal AUTH_JWT_AUDIENCE there. Default viteordre-api
//
// In production set AUTH_ISSUER explicitly. A wrong value fails closed: every token is rejected.
package authn

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"

	"encore.dev/beta/auth"
	"encore.dev/beta/errs"

	"encore.app/internal/jwtauth"
)

// AuthData is what handlers can read with auth.Data() after the caller is authenticated.
// The user id itself is the auth.UID returned by auth.UserID().
type AuthData struct {
	Email         string
	Name          string
	EmailVerified bool
}

//encore:service
type Service struct {
	verifier *jwtauth.Verifier
}

func initService() (*Service, error) {
	issuer := envOr("AUTH_ISSUER", "http://localhost:3000")
	v, err := jwtauth.NewVerifier(jwtauth.Config{
		JWKSURL:  envOr("AUTH_JWKS_URL", strings.TrimRight(issuer, "/")+"/api/auth/jwks"),
		Issuer:   issuer,
		Audience: envOr("AUTH_JWT_AUDIENCE", "viteordre-api"),
	})
	if err != nil {
		return nil, err
	}
	return &Service{verifier: v}, nil
}

// AuthHandler is called by Encore for every endpoint declared with `auth`, with the value of the
// "Authorization: Bearer <token>" header.
//
//encore:authhandler
func (s *Service) AuthHandler(ctx context.Context, token string) (auth.UID, *AuthData, error) {
	claims, err := s.verifier.Verify(ctx, token)
	switch {
	case err == nil:
		return auth.UID(claims.Subject), &AuthData{
			Email: claims.Email, Name: claims.Name, EmailVerified: claims.EmailVerified,
		}, nil
	case errors.Is(err, jwtauth.ErrKeysUnavailable):
		slog.Error("authn: signing keys unavailable", "error", err)
		return "", nil, errs.B().
			Code(errs.Unavailable).
			Msg("authentication is temporarily unavailable").
			Err()
	default:
		// The reason stays in the log, a client learns nothing it could use to probe.
		slog.Debug("authn: token rejected", "error", err)
		return "", nil, errs.B().Code(errs.Unauthenticated).Msg("invalid or expired token").Err()
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

//encore:api auth method=GET path=/me
func Me(ctx context.Context) (*MeResponse, error) {
	d := auth.Data().(*AuthData)
	uid, _ := auth.UserID()
	return &MeResponse{ID: string(uid), Email: d.Email, Name: d.Name}, nil
}

type MeResponse struct{ ID, Email, Name string }
