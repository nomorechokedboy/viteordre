package config

import "log/slog"

type CacheConfig struct {
	URI    string `env:"REDIS_URI" env-default:"localhost:6379"`
	Passwd string `env:"REDIS_PWD" env-default:""`
	DB     int    `env:"REDIS_DB"  env-default:"0"`
}

var _ slog.LogValuer = (*CacheConfig)(nil)

func (c *CacheConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("REDIS_URI", c.URI),
		slog.String("REDIS_PASSWD", c.Passwd),
		slog.Int("REDIS_DB", c.DB),
	)
}
