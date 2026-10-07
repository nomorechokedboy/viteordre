package config

import (
	"fmt"
	"log/slog"
)

type DatabaseConfig struct {
	Host     string `env:"DB_HOST" env-default:"0.0.0.0"`
	Name     string `env:"DB_NAME" env-default:"viteordre"`
	User     string `env:"DB_USER" env-default:"none"`
	Password string `env:"DB_PWD"  env-default:"none"`
}

var _ slog.LogValuer = (*DatabaseConfig)(nil)

func (c *DatabaseConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("DB_URL", generateMaskedString(c.Host)),
		slog.String("DB_PWD", generateMaskedString(c.Password)),
		slog.String("DB_USER", c.Name),
		slog.String("DB_NAME", c.Name),
	)
}

func (c *DatabaseConfig) GetDNS() string {
	return fmt.Sprintf("libsql://%s-%s.turso.io", c.Name, c.User)
}
