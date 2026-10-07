package config

import (
	"log/slog"
)

type Oauth2Config struct {
	ClientId     string `env:"CLIENT_ID"     json:"client_id"`
	ClientSecret string `env:"CLIENT_SECRET" json:"client_secret"`
	OriginUrl    string `env:"ORIGIN_URL"    json:"origin_url"`
	AuthUrl      string `env:"AUTH_URL"      json:"auth_url"`
	TokenUrl     string `env:"TOKEN_URL"     json:"token_url"`
	RedirectURL  string `env:"REDIRECT_URL"  json:"redirect_url"  env-default:"http://localhost:4000/oauth2/callback"`
}

var _ slog.LogValuer = (*Oauth2Config)(nil)

func (c *Oauth2Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("client_id", c.ClientId),
		slog.String("client_secret", generateMaskedString(c.ClientSecret)),
		slog.String("origin_url", c.OriginUrl),
		slog.String("auth_url", c.AuthUrl),
		slog.String("token_url", c.TokenUrl),
		slog.String("redirect_url", c.RedirectURL),
	)
}
