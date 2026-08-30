package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"time"

	authpb "github.com/Iposhka54/task-tracker/pkg/api/auth"
	pgpool "github.com/Iposhka54/task-tracker/pkg/postgres"
	pkgredis "github.com/Iposhka54/task-tracker/pkg/redis"
	"github.com/Iposhka54/task-tracker/pkg/telemetry"
	bcryptadapter "github.com/Iposhka54/task-tracker/services/auth-service/internal/adapter/bcrypt"
	grpcadapter "github.com/Iposhka54/task-tracker/services/auth-service/internal/adapter/grpc"
	jwtadapter "github.com/Iposhka54/task-tracker/services/auth-service/internal/adapter/jwt"
	pgadapter "github.com/Iposhka54/task-tracker/services/auth-service/internal/adapter/postgres"
	redisadapter "github.com/Iposhka54/task-tracker/services/auth-service/internal/adapter/redis"
	"github.com/Iposhka54/task-tracker/services/auth-service/internal/config"
	"github.com/Iposhka54/task-tracker/services/auth-service/internal/service"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"
)

func main() {
	cfg, err := config.New()
	if err != nil {
		log.Fatalf("%v", err)
	}
	ctx := context.Background()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	shutdown, err := telemetry.InitTracer(ctx, cfg.Telemetry, "auth-service")
	if err != nil {
		log.Fatalf("opentelemetry: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err = shutdown(shutdownCtx); err != nil {
			log.Printf("tracer shutdown error: %v", err)
		}
	}()

	dbPool, err := pgpool.New(ctx, cfg.Postgres)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer dbPool.Close()

	rdb, err := pkgredis.New(ctx, cfg.Redis)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer rdb.Close()

	auth := service.NewAuth(
		pgadapter.NewUserRepo(dbPool),
		bcryptadapter.New(12),
		jwtadapter.New(cfg.JWT.Secret, cfg.JWT.AccessTTL),
		pgadapter.NewRefreshRepo(dbPool),
		redisadapter.NewCache(rdb),
		cfg.JWT.RefreshTTL,
	)

	lis, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", cfg.GRPCPort))
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	srv := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler(
		otelgrpc.WithTracerProvider(otel.GetTracerProvider()),
		otelgrpc.WithPropagators(otel.GetTextMapPropagator()),
	)))

	authpb.RegisterAuthServiceServer(srv, grpcadapter.New(auth))

	go func() {
		log.Printf("auth-service grpc on :%d", cfg.GRPCPort)
		if serveErr := srv.Serve(lis); serveErr != nil {
			log.Printf("serve: %v", serveErr)
			stop()
		}
	}()

	<-ctx.Done()
	srv.GracefulStop()
}
