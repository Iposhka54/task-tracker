package grpcadapter

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func UnaryLogger(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	code := status.Code(err)
	attrs := []any{
		slog.String("grpc.method", info.FullMethod),
		slog.String("grpc.code", code.String()),
		slog.Duration("duration", time.Since(start)),
	}
	switch {
	case err == nil:
		slog.InfoContext(ctx, "grpc request", attrs...)
	case code == codes.Internal || code == codes.Unknown:
		slog.ErrorContext(ctx, "grpc request failed", append(attrs, slog.String("error", err.Error()))...)
	default:
		slog.WarnContext(ctx, "grpc request failed", append(attrs, slog.String("error", err.Error()))...)
	}
	return resp, err
}
