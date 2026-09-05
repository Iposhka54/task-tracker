package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetrier_Succeeds(t *testing.T) {
	t.Parallel()

	var n int
	err := (&Retrier{MaxAttempts: 3}).WithRetry(context.Background(), func() error {
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("calls: %d", n)
	}
}

func TestRetrier_RetriesThenSucceeds(t *testing.T) {
	t.Parallel()

	var n int
	err := (&Retrier{MaxAttempts: 3}).WithRetry(context.Background(), func() error {
		n++
		if n < 3 {
			return errors.New("busy")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("calls: %d", n)
	}
}

func TestRetrier_StopsWhenNotRetryable(t *testing.T) {
	t.Parallel()

	permanent := errors.New("permanent")
	var n int
	err := (&Retrier{
		MaxAttempts: 5,
		RetryIf: func(err error) bool {
			return !errors.Is(err, permanent)
		},
	}).WithRetry(context.Background(), func() error {
		n++
		return permanent
	})
	if !errors.Is(err, permanent) {
		t.Fatalf("err: %v", err)
	}
	if n != 1 {
		t.Fatalf("calls: %d", n)
	}
}

func TestRetrier_RespectsContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := (&Retrier{MaxAttempts: 3, Delay: time.Millisecond}).WithRetry(ctx, func() error {
		return errors.New("busy")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err: %v", err)
	}
}
