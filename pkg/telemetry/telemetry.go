package telemetry

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

type Config struct {
	TraceEndpoint string `env:"TRACE_ENDPOINT" env-default:"localhost:4317"`
	TraceInsecure bool   `env:"TRACE_INSECURE" env-default:"true"`

	MetricsEndpoint string `env:"METRICS_ENDPOINT" env-default:"localhost:9090"`
	MetricsURLPath  string `env:"METRICS_URL_PATH" env-default:"/api/v1/otlp/v1/metrics"`
	MetricsInsecure bool   `env:"METRICS_INSECURE" env-default:"true"`

	Environment string `env:"ENVIRONMENT" env-default:"development"`

	SamplingRate float64 `env:"TRACE_SAMPLING_RATE" env-default:"1.0"`

	BatchTimeout time.Duration `env:"TRACE_BATCH_TIMEOUT" env-default:"5s"`
}

func Init(ctx context.Context, cfg Config, serviceName string) (func(context.Context) error, error) {
	res, err := serviceResource(serviceName, cfg.Environment)
	if err != nil {
		return nil, err
	}

	shutdownTrace, err := initTracer(ctx, cfg, res)
	if err != nil {
		return nil, err
	}

	shutdownMeter, err := initMeter(ctx, cfg, res)
	if err != nil {
		_ = shutdownTrace(ctx)
		return nil, err
	}

	return func(ctx context.Context) error {
		return errors.Join(shutdownMeter(ctx), shutdownTrace(ctx))
	}, nil
}

func initTracer(ctx context.Context, cfg Config, res *resource.Resource) (func(context.Context) error, error) {
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(cfg.TraceEndpoint),
	}
	if cfg.TraceInsecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, err
	}

	var sampler sdktrace.Sampler
	if cfg.SamplingRate >= 1.0 {
		sampler = sdktrace.AlwaysSample()
	} else if cfg.SamplingRate <= 0.0 {
		sampler = sdktrace.NeverSample()
	} else {
		sampler = sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SamplingRate))
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sampler),
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(cfg.BatchTimeout)),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return tp.Shutdown, nil
}

func initMeter(ctx context.Context, cfg Config, res *resource.Resource) (func(context.Context) error, error) {
	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpoint(cfg.MetricsEndpoint),
		otlpmetrichttp.WithURLPath(cfg.MetricsURLPath),
	}
	if cfg.MetricsInsecure {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}

	exporter, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(cfg.BatchTimeout))),
	)
	otel.SetMeterProvider(mp)
	return mp.Shutdown, nil
}

func serviceResource(serviceName, env string) (*resource.Resource, error) {
	return resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(serviceName),
		attribute.String("deployment.environment", env),
	))
}
