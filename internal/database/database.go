package database

import (
	"context"
	"crypto/tls"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

type RedisConfig struct {
	URL      string
	Password string
	TLS      bool
}

func OpenRedis(cfg RedisConfig) *redis.Client {
	opt, err := redis.ParseURL(cfg.URL)
	if err != nil {
		opt = &redis.Options{Addr: "localhost:6379"}
	}
	if cfg.Password != "" {
		opt.Password = cfg.Password
	}
	if cfg.TLS {
		opt.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return redis.NewClient(opt)
}
