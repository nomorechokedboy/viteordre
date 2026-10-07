package cache

import (
	"encore.app/internal/config"
	"github.com/redis/go-redis/v9"
)

func New(cfg *config.CacheConfig) *redis.Client {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.URI,
		Password: cfg.Passwd,
		DB:       cfg.DB,
	})

	return rdb
}
