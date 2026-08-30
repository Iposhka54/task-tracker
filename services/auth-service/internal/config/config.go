package config

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/Iposhka54/task-tracker/pkg/logger"
	"github.com/Iposhka54/task-tracker/pkg/postgres"
	pkgredis "github.com/Iposhka54/task-tracker/pkg/redis"
	"github.com/Iposhka54/task-tracker/pkg/telemetry"
	"github.com/ilyakaznacheev/cleanenv"
	"github.com/joho/godotenv"
)

type JWTConfig struct {
	Secret     string        `env:"SECRET" env-default:"dev-only-change-me"`
	AccessTTL  time.Duration `env:"ACCESS_TTL" env-default:"15m"`
	RefreshTTL time.Duration `env:"REFRESH_TTL" env-default:"720h"`
}

type Config struct {
	GRPCPort  int             `env:"GRPC_PORT" env-default:"9001"`
	Postgres  postgres.Config `env-prefix:"POSTGRES_"`
	Redis     pkgredis.Config `env-prefix:"REDIS_"`
	JWT       JWTConfig       `env-prefix:"JWT_"`
	Telemetry telemetry.Config
	Log       logger.Config
}

func New() (Config, error) {
	_ = godotenv.Load(".env")
	_ = godotenv.Load("services/auth-service/.env")

	var cfg Config
	if err := cleanenv.ReadEnv(&cfg); err != nil {
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
		Telemetry: telemetry.Config{
			TraceEndpoint:   "localhost:4317",
			TraceInsecure:   true,
			MetricsEndpoint: "localhost:9090",
			MetricsURLPath:  "/api/v1/otlp/v1/metrics",
			MetricsInsecure: true,
			Environment:     "development",
			SamplingRate:    1.0,
			BatchTimeout:    5 * time.Second,
		},
		Log: logger.Config{
			Level: slog.LevelInfo,
		},
	}
}
