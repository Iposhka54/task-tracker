package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	authpb "github.com/Iposhka54/task-tracker/pkg/api/auth"
	pkglogger "github.com/Iposhka54/task-tracker/pkg/logger"
	pkgredis "github.com/Iposhka54/task-tracker/pkg/redis"
	"github.com/Iposhka54/task-tracker/pkg/telemetry"
	"github.com/Iposhka54/task-tracker/services/gateway/internal/adapter/http/middleware"
	"github.com/Iposhka54/task-tracker/services/gateway/internal/config"
	"github.com/Iposhka54/task-tracker/services/gateway/internal/limiter"
	gatewaymetrics "github.com/Iposhka54/task-tracker/services/gateway/internal/metric"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const serviceName = "gateway"

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
		log.Error("failed to init telemetry", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err = shutdown(shutdownCtx); err != nil {
			log.Error("telemetry shutdown", "error", err)
		}
		cancel()
	}()

	rdb, err := pkgredis.New(ctx, cfg.Redis)
	if err != nil {
		log.Error("connect redis", "error", err)
		os.Exit(1)
	}
	defer rdb.Close()

	metrics, err := gatewaymetrics.New(otel.Meter(serviceName))
	if err != nil {
		log.Error("create metrics", "error", err)
		os.Exit(1)
	}

	dialOpts := []grpc.DialOption{
		grpc.WithStatsHandler(otelgrpc.NewClientHandler(
			otelgrpc.WithTracerProvider(otel.GetTracerProvider()),
			otelgrpc.WithMeterProvider(otel.GetMeterProvider()),
			otelgrpc.WithPropagators(otel.GetTextMapPropagator()),
		)),
	}
	if cfg.AuthGRPCInsecure {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})))
	}

	conn, err := grpc.NewClient(cfg.AuthGRPCAddr, dialOpts...)
	if err != nil {
		log.Error("dial auth-service", "error", err, "addr", cfg.AuthGRPCAddr)
		os.Exit(1)
	}
	defer conn.Close()

	gwMux := runtime.NewServeMux(
		runtime.WithMetadata(func(ctx context.Context, _ *http.Request) metadata.MD {
			if uid := middleware.UserIDFromContext(ctx); uid != "" {
				return metadata.Pairs(middleware.UserIDMetadataKey, uid)
			}
			return nil
		}),
	)

	if err = authpb.RegisterAuthServiceHandler(ctx, gwMux, conn); err != nil {
		log.Error("register auth gateway", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+middleware.HealthPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("/", gwMux)

	authLimiter := limiter.New(rdb, limiter.ScopeAuth, cfg.RateLimit.Auth.RPM, cfg.RateLimit.Auth.Burst, metrics)
	apiLimiter := limiter.New(rdb, limiter.ScopeAPI, cfg.RateLimit.API.RPM, cfg.RateLimit.API.Burst, metrics)
	handler := middleware.Limiter(authLimiter, apiLimiter, middleware.JWT(cfg.JWT.Secret, middleware.RequestLog(
		otelhttp.NewHandler(mux, serviceName,
			otelhttp.WithTracerProvider(otel.GetTracerProvider()),
			otelhttp.WithMeterProvider(otel.GetMeterProvider()),
			otelhttp.WithPropagators(otel.GetTextMapPropagator()),
		),
	)))
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Info("Gateway started", "http_port", cfg.HTTPPort)
		if err = server.ListenAndServe(); err != nil && !errors.Is(http.ErrServerClosed, err) {
			log.Error("serve", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("Gateway interrupted")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err = server.Shutdown(shutdownCtx); err != nil {
		log.Error("http shutdown", "error", err)
	}
}
