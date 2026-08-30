package postgres

import (
	"context"
	"fmt"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	Host     string `env:"HOST" env-default:"localhost"`
	Port     uint16 `env:"PORT" env-default:"5432"`
	Username string `env:"USER" env-default:"postgres"`
	Password string `env:"PASSWORD" env-default:"postgres"`
	Database string `env:"DB" env-default:"task-tracker"`
	MaxConns int32  `env:"MAX_CONNS" env-default:"10"`
	MinConns int32  `env:"MIN_CONNS" env-default:"5"`
}

func New(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(GetConnString(cfg))
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}
	poolCfg.ConnConfig.Tracer = otelpgx.NewTracer()

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect database: %w", err)
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return pool, nil
}

func GetConnString(c Config) string {
	connString := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable&pool_max_conns=%d&pool_min_conns=%d",
		c.Username,
		c.Password,
		c.Host,
		c.Port,
		c.Database,
		c.MaxConns,
		c.MinConns,
	)

	return connString
}
