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

type RateLimitBucket struct {
	RPM   float64 `env:"RPM"`
	Burst int     `env:"BURST"`
}

type RateLimitConfig struct {
	Auth RateLimitBucket `env-prefix:"AUTH_"`
	API  RateLimitBucket `env-prefix:"API_"`
}

type Config struct {
	HTTPPort         int             `env:"HTTP_PORT" env-default:"80"`
	AuthGRPCAddr     string          `env:"AUTH_GRPC_ADDRESS" env-default:"localhost:9001"`
	AuthGRPCInsecure bool            `env:"AUTH_GRPC_INSECURE" env-default:"true"`
	Redis            pkgredis.Config `env-prefix:"REDIS_"`
	JWT              JWTConfig       `env-prefix:"JWT_"`
	RateLimit        RateLimitConfig `env-prefix:"RATE_LIMIT_"`
	Telemetry        telemetry.Config
	Log              logger.Config
}

func New() (Config, error) {
	_ = godotenv.Load(".env")
	_ = godotenv.Load("services/gateway/.env")

	var cfg Config
	cfg.RateLimit.Auth = RateLimitBucket{RPM: 10, Burst: 10}
	cfg.RateLimit.API = RateLimitBucket{RPM: 100, Burst: 100}

	if err := cleanenv.ReadEnv(&cfg); err != nil {
		return Config{}, fmt.Errorf("failed to read env vars: %w", err)
	}

	return cfg, nil
}
