package middleware

import (
	"sync"
	"time"
)

type TokenBucket struct {
	mu         sync.Mutex
	tokens     float64
	maxTokens  float64
	refillRate float64
	lastRefill time.Time
}

func NewTokenBucket(burst, rpc float64) *TokenBucket {
	return &TokenBucket{
		tokens:     burst,
		maxTokens:  burst,
		refillRate: rpc,
		lastRefill: time.Now(),
	}
}

func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.tokens += elapsed * tb.refillRate
	if tb.tokens > tb.maxTokens {
		tb.tokens = tb.maxTokens
	}
	tb.lastRefill = now
}

func (tb *TokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	if tb.tokens >= 1 {
		tb.tokens -= 1
		return true
	}
	return false
}

type IPTokenBucket struct {
	buckets    map[string]*TokenBucket
	mu         sync.RWMutex
	maxTokens  float64
	refillRate float64
}

func NewIPTokenBucket(maxTokens, refillRate float64) *IPTokenBucket {
	return &IPTokenBucket{
		buckets:    make(map[string]*TokenBucket),
		maxTokens:  maxTokens,
		refillRate: refillRate,
	}
}

func (itb *IPTokenBucket) getBucket(ip string) *TokenBucket {
	itb.mu.RLock()
	bucket, exists := itb.buckets[ip]
	itb.mu.RUnlock()

	if exists {
		return bucket
	}

	itb.mu.Lock()
	defer itb.mu.Unlock()

	bucket, exists = itb.buckets[ip]
	if exists {
		return bucket
	}

	bucket = NewTokenBucket(itb.maxTokens, itb.refillRate)
	itb.buckets[ip] = bucket
	return bucket
}
