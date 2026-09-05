package limiter

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type MockMetrics struct {
	counter int64
}

func NewMetrics() *MockMetrics {
	return &MockMetrics{counter: 0}
}

func (m *MockMetrics) RegisterRedisBucketConflict(ctx context.Context) {
	m.counter++
}

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func TestLimiter_BurstThenDeny(t *testing.T) {
	t.Parallel()

	metrics := NewMetrics()
	l := New(testRedis(t), ScopeAuth, 3, 3, metrics)
	now := time.Now()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		ok, err := l.allowAt(ctx, "1.2.3.4", now)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("request %d denied", i+1)
		}
	}

	if metrics.counter != 0 {
		t.Fatalf("expected not conflicts, but was %d", metrics.counter)
	}

	ok, err := l.allowAt(ctx, "1.2.3.4", now)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("limit exceeded, expected deny")
	}

	ok, err = l.allowAt(ctx, "5.6.7.8", now)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("other ip should have its own counter")
	}
}

func TestLimiter_NewWindow(t *testing.T) {
	t.Parallel()

	metrics := NewMetrics()
	l := New(testRedis(t), ScopeAPI, 1, 1, metrics)
	now := time.Now()
	ctx := context.Background()

	ok, err := l.allowAt(ctx, "10.0.0.1", now)
	if err != nil || !ok {
		t.Fatalf("first: ok=%v err=%v", ok, err)
	}
	ok, err = l.allowAt(ctx, "10.0.0.1", now)
	if err != nil || ok {
		t.Fatalf("same window: ok=%v err=%v", ok, err)
	}

	ok, err = l.allowAt(ctx, "10.0.0.1", now.Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("next minute: ok=%v err=%v", ok, err)
	}
}

func TestLimiter_ScopesAreIndependent(t *testing.T) {
	t.Parallel()

	rdb := testRedis(t)
	metrics := NewMetrics()
	auth := New(rdb, ScopeAuth, 1, 1, metrics)
	api := New(rdb, ScopeAPI, 1, 1, metrics)
	now := time.Now()
	ctx := context.Background()

	ok, err := auth.allowAt(ctx, "1.1.1.1", now)
	if err != nil || !ok {
		t.Fatalf("auth: ok=%v err=%v", ok, err)
	}
	ok, err = api.allowAt(ctx, "1.1.1.1", now)
	if err != nil || !ok {
		t.Fatalf("api should not share auth counter: ok=%v err=%v", ok, err)
	}
}
