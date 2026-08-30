package main

import (
	"context"
	"fmt"
	"log"
	"net"

	authpb "github.com/Iposhka54/task-tracker/pkg/api/auth"
	pgpool "github.com/Iposhka54/task-tracker/pkg/postgres"
	pkgredis "github.com/Iposhka54/task-tracker/pkg/redis"
	bcryptadapter "github.com/Iposhka54/task-tracker/services/auth-service/internal/adapter/bcrypt"
	grpcadapter "github.com/Iposhka54/task-tracker/services/auth-service/internal/adapter/grpc"
	jwtadapter "github.com/Iposhka54/task-tracker/services/auth-service/internal/adapter/jwt"
	pgadapter "github.com/Iposhka54/task-tracker/services/auth-service/internal/adapter/postgres"
	redisadapter "github.com/Iposhka54/task-tracker/services/auth-service/internal/adapter/redis"
	"github.com/Iposhka54/task-tracker/services/auth-service/internal/config"
	"github.com/Iposhka54/task-tracker/services/auth-service/internal/service"
	"google.golang.org/grpc"
)

func main() {
	cfg, err := config.New()
	if err != nil {
		log.Fatalf("%v", err)
	}
	ctx := context.Background()

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

	srv := grpc.NewServer()
	authpb.RegisterAuthServiceServer(srv, grpcadapter.New(auth))

	log.Printf("auth-service grpc on :%d", cfg.GRPCPort)
	if err = srv.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
