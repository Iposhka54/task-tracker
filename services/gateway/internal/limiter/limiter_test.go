package limiter

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func TestLimiter_BurstThenDeny(t *testing.T) {
	t.Parallel()

	l := New(testRedis(t), ScopeAuth, 3, 3, nil)
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

	l := New(testRedis(t), ScopeAPI, 1, 1, nil)
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
	auth := New(rdb, ScopeAuth, 1, 1, nil)
	api := New(rdb, ScopeAPI, 1, 1, nil)
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

func TestLimiter_WatchConflictRetries(t *testing.T) {
	t.Parallel()

	l := New(testRedis(t), ScopeAuth, 60, 8, nil)
	ctx := context.Background()
	now := time.Now()

	const n = 8
	got := make(chan bool, n)
	for range n {
		go func() {
			ok, err := l.allowAt(ctx, "9.9.9.9", now)
			if err != nil {
				t.Errorf("allow: %v", err)
				got <- false
				return
			}
			got <- ok
		}()
	}

	var allowed int
	for range n {
		if <-got {
			allowed++
		}
	}
	if allowed != n {
		t.Fatalf("allowed %d of %d concurrent requests", allowed, n)
	}
}
