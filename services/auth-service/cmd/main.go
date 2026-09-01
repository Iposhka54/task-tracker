package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"time"

	authpb "github.com/Iposhka54/task-tracker/pkg/api/auth"
	pkglogger "github.com/Iposhka54/task-tracker/pkg/logger"
	pgpool "github.com/Iposhka54/task-tracker/pkg/postgres"
	pkgredis "github.com/Iposhka54/task-tracker/pkg/redis"
	"github.com/Iposhka54/task-tracker/pkg/telemetry"
	bcryptadapter "github.com/Iposhka54/task-tracker/services/auth-service/internal/adapter/bcrypt"
	grpcadapter "github.com/Iposhka54/task-tracker/services/auth-service/internal/adapter/grpc"
	jwtadapter "github.com/Iposhka54/task-tracker/services/auth-service/internal/adapter/jwt"
	pgadapter "github.com/Iposhka54/task-tracker/services/auth-service/internal/adapter/postgres"
	redisadapter "github.com/Iposhka54/task-tracker/services/auth-service/internal/adapter/redis"
	"github.com/Iposhka54/task-tracker/services/auth-service/internal/config"
	authmetrics "github.com/Iposhka54/task-tracker/services/auth-service/internal/metrics"
	"github.com/Iposhka54/task-tracker/services/auth-service/internal/service"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"
)

const serviceName = "auth-service"

func main() {
	cfg, err := config.New()
	if err != nil {
		slog.Error("read config", "error", err)
		os.Exit(1)
	}

	log := pkglogger.New(cfg.Log)
	slog.SetDefault(log)

	ctx := context.Background()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	shutdown, err := telemetry.Init(ctx, cfg.Telemetry, serviceName)
	if err != nil {
		log.Error("init telemetry", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err = shutdown(shutdownCtx); err != nil {
			log.Error("telemetry shutdown", "error", err)
		}
	}()

	dbPool, err := pgpool.New(ctx, cfg.Postgres)
	if err != nil {
		log.Error("connect postgres", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	rdb, err := pkgredis.New(ctx, cfg.Redis)
	if err != nil {
		log.Error("connect redis", "error", err)
		os.Exit(1)
	}
	defer rdb.Close()

	authMetrics, err := authmetrics.New(otel.Meter(serviceName))
	if err != nil {
		log.Error("init auth metrics", "error", err)
		os.Exit(1)
	}

	auth := service.NewAuth(
		pgadapter.NewUserRepo(dbPool),
		bcryptadapter.New(12),
		jwtadapter.New(cfg.JWT.Secret, cfg.JWT.AccessTTL),
		pgadapter.NewRefreshRepo(dbPool),
		redisadapter.NewCache(rdb),
		cfg.JWT.RefreshTTL,
		authMetrics,
	)

	lis, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", cfg.GRPCPort))
	if err != nil {
		log.Error("listen", "error", err, "port", cfg.GRPCPort)
		os.Exit(1)
	}

	srv := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler(
			otelgrpc.WithTracerProvider(otel.GetTracerProvider()),
			otelgrpc.WithMeterProvider(otel.GetMeterProvider()),
			otelgrpc.WithPropagators(otel.GetTextMapPropagator()),
		)),
		grpc.UnaryInterceptor(grpcadapter.UnaryLogger),
	)

	authpb.RegisterAuthServiceServer(srv, grpcadapter.New(auth))

	go func() {
		log.Info("auth-service started", "grpc_port", cfg.GRPCPort)
		if serveErr := srv.Serve(lis); serveErr != nil {
			log.Error("serve", "error", serveErr)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("auth-service stopping")
	srv.GracefulStop()
}
