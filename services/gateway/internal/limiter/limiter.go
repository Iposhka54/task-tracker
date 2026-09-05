package limiter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/Iposhka54/task-tracker/pkg/retry"
	"github.com/Iposhka54/task-tracker/services/gateway/internal/metric"
	"github.com/redis/go-redis/v9"
)

const (
	ScopeAuth = "auth"
	ScopeAPI  = "api"

	bucketKeyFmt    = "gateway:ratelimit:%s:%s"
	fieldTokens     = "tokens"
	fieldLastUpdate = "last_update"
)

type Limiter struct {
	rdb     *redis.Client
	scope   string
	rate    float64
	burst   int
	metrics *metric.GatewayMetrics
	retrier retry.Retrier
}

func New(rdb *redis.Client, scope string, rpm float64, burst int, metrics *metric.GatewayMetrics) *Limiter {
	if burst < 1 {
		burst = 1
	}
	if rpm <= 0 {
		rpm = 1
	}
	return &Limiter{
		rdb:     rdb,
		scope:   scope,
		rate:    rpm / 60,
		burst:   burst,
		metrics: metrics,
		retrier: retry.Retrier{
			MaxAttempts: 5,
			Delay:       10 * time.Millisecond,
			MaxDelay:    100 * time.Millisecond,
			Backoff:     true,
			RetryIf: func(err error) bool {
				return errors.Is(err, redis.TxFailedErr)
			},
		},
	}
}

func (l *Limiter) Allow(ctx context.Context, key string) (bool, error) {
	return l.allowAt(ctx, key, time.Now())
}

func (l *Limiter) allowAt(ctx context.Context, key string, now time.Time) (bool, error) {
	redisKey := fmt.Sprintf(bucketKeyFmt, l.scope, key)

	var allowed bool
	err := l.retrier.WithRetry(ctx, func() error {
		ok, err := l.tryAllow(ctx, redisKey, now)
		if err != nil {
			return err
		}
		allowed = ok
		return nil
	})
	return allowed, err
}

func (l *Limiter) tryAllow(ctx context.Context, redisKey string, now time.Time) (bool, error) {
	var allowed bool
	err := l.rdb.Watch(ctx, func(tx *redis.Tx) error {
		vals, err := tx.HGetAll(ctx, redisKey).Result()
		if err != nil {
			return err
		}

		tokens := float64(l.burst)
		lastUpdate := now.Unix()
		if s := vals[fieldTokens]; s != "" {
			if v, err := strconv.ParseFloat(s, 64); err == nil {
				tokens = v
			}
		}
		if s := vals[fieldLastUpdate]; s != "" {
			if v, err := strconv.ParseInt(s, 10, 64); err == nil {
				lastUpdate = v
			}
		}

		elapsed := now.Unix() - lastUpdate
		if elapsed < 0 {
			elapsed = 0
		}
		tokens = math.Min(float64(l.burst), tokens+float64(elapsed)*l.rate)

		allowed = false
		if tokens >= 1 {
			tokens--
			allowed = true
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.HSet(ctx, redisKey, fieldTokens, tokens, fieldLastUpdate, now.Unix())
			pipe.Expire(ctx, redisKey, time.Minute)
			return nil
		})

		if err != nil && errors.Is(err, redis.TxFailedErr) {
			l.metrics.RegisterRedisBucketConflict(ctx) //watch error, value in bucket was change
		}
		return err
	}, redisKey)
	if err != nil {
		return false, err
	}
	return allowed, nil
}
