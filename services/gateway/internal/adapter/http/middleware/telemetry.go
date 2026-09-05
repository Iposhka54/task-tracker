package middleware

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "gateway.middleware"

func WithTracing(name string, mw func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return TraceMiddleware(name, mw(next))
	}
}

func TraceMiddleware(name string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := StartSpan(r.Context(), name)
		defer span.End()

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func StartSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return otel.Tracer(tracerName).Start(ctx, name)
}
