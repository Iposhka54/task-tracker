package logger

import (
	"context"
	"io"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/trace"
)

type Config struct {
	Level slog.Level `env:"LOG_LEVEL" env-default:"INFO"`
}

func New(cfg Config) *slog.Logger {
	return NewWithWriter(os.Stdout, cfg)
}

func NewWithWriter(w io.Writer, cfg Config) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: cfg.Level})
	return slog.New(&ctxHandler{Handler: h})
}

type ctxHandler struct {
	slog.Handler
}

func (h *ctxHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.Bool("sampled", sc.TraceFlags().IsSampled()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func (h *ctxHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ctxHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *ctxHandler) WithGroup(name string) slog.Handler {
	return &ctxHandler{Handler: h.Handler.WithGroup(name)}
}
