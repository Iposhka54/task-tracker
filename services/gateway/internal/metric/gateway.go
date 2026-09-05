package metric

import (
	"context"

	"go.opentelemetry.io/otel/metric"
)

type GatewayMetrics struct {
	redisBucketConflict metric.Int64Counter
}

func New(meter metric.Meter) (*GatewayMetrics, error) {
	if meter == nil {
		return nil, nil
	}

	m := &GatewayMetrics{}
	var err error

	if m.redisBucketConflict, err = meter.Int64Counter("redis.bucket.conflict",
		metric.WithDescription("Count of conflict one bucket from two sessions"),
	); err != nil {
		return nil, err
	}

	return m, nil
}

func (m *GatewayMetrics) RegisterRedisBucketConflict(ctx context.Context) {
	m.redisBucketConflict.Add(ctx, 1)
}
