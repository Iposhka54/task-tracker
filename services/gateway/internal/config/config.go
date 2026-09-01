package config

import (
	"fmt"

	"github.com/Iposhka54/task-tracker/pkg/logger"
	pkgredis "github.com/Iposhka54/task-tracker/pkg/redis"
	"github.com/Iposhka54/task-tracker/pkg/telemetry"
	"github.com/ilyakaznacheev/cleanenv"
	"github.com/joho/godotenv"
)

type JWTConfig struct {
	Secret string `env:"SECRET" env-default:"dev-only-change-me"`
}

type Config struct {
	HTTPPort     int             `env:"HTTP_PORT" env-default:"80"`
	AuthGRPCAddr string          `env:"AUTH_GRPC_ADDRESS" env-default:"localhost:9001"`
	Redis        pkgredis.Config `env-prefix:"REDIS_"`
	JWT          JWTConfig       `env-prefix:"JWT_"`
	Telemetry    telemetry.Config
	Log          logger.Config
}

func New() (Config, error) {
	_ = godotenv.Load(".env")

	var cfg Config

	if err := cleanenv.ReadEnv(&cfg); err != nil {
		return Config{}, fmt.Errorf("failed to read env vars: %w", err)
	}

	return cfg, nil
}
