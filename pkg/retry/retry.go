package retry

import (
	"context"
	"fmt"
	"time"
)

type Retrier struct {
	MaxAttempts int
	Delay       time.Duration
	MaxDelay    time.Duration
	Backoff     bool
	RetryIf     func(error) bool
}

func (r *Retrier) WithRetry(ctx context.Context, fn func() error) error {
	maxAttempts := r.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var err error
	for i := 1; i <= maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled: %w", ctx.Err())
		default:
		}

		err = fn()
		if err == nil {
			return nil
		}

		retryable := r.RetryIf == nil || r.RetryIf(err)
		if !retryable || i == maxAttempts {
			return fmt.Errorf("after %d attempts: %w", i, err)
		}

		delay := r.Delay
		if r.Backoff {
			delay = r.Delay * time.Duration(1<<(i-1))
		}
		if r.MaxDelay > 0 && delay > r.MaxDelay {
			delay = r.MaxDelay
		}

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled while waiting: %w", ctx.Err())
		}
	}

	return fmt.Errorf("after %d attempts: %w", maxAttempts, err)
}
