package config

import (
	"fmt"
	"time"

	"github.com/Iposhka54/task-tracker/pkg/postgres"
	pkgredis "github.com/Iposhka54/task-tracker/pkg/redis"
	"github.com/ilyakaznacheev/cleanenv"
)

type JWTConfig struct {
	Secret     string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

type Config struct {
	GRPCPort int
	Postgres postgres.Config `env:"POSTGRES"`
	Redis    pkgredis.Config `env:"REDIS"`
	JWT      JWTConfig
}

func New() (Config, error) {
	var cfg Config

	if err := cleanenv.ReadEnv(cfg); err != nil {
		return Config{}, fmt.Errorf("failed to read env vars: %w", err)
	}

	return cfg, nil
}

func Default() Config {
	return Config{
		GRPCPort: 9001,
		Postgres: postgres.Config{
			Host:     "localhost",
			Port:     5432,
			Username: "postgres",
			Password: "postgres",
			Database: "auth",
			MaxConns: 10,
			MinConns: 1,
		},
		Redis: pkgredis.Config{
			Host: "localhost",
			Port: 6379,
		},
		JWT: JWTConfig{
			Secret:     "dev-only-change-me",
			AccessTTL:  15 * time.Minute,
			RefreshTTL: 30 * 24 * time.Hour,
		},
	}
}
