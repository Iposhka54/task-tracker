package limiter

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type CleanableRateLimiter struct {
	limiters        map[string]*rate.Limiter
	mu              sync.RWMutex
	rate            rate.Limit
	burst           int
	lastAccess      map[string]time.Time
	cleanupInterval time.Duration
	maxAge          time.Duration
}

func NewCleanableRateLimiter(rpm float64, burst int, cleanupInterval, maxAge time.Duration) *CleanableRateLimiter {
	if burst < 1 {
		burst = 1
	}
	rl := &CleanableRateLimiter{
		limiters:        make(map[string]*rate.Limiter),
		rate:            rate.Limit(rpm / 60),
		burst:           burst,
		lastAccess:      make(map[string]time.Time),
		cleanupInterval: cleanupInterval,
		maxAge:          maxAge,
	}

	go rl.cleanupLoop()

	return rl
}

func (rl *CleanableRateLimiter) cleanupLoop() {
	if rl.cleanupInterval <= 0 {
		return
	}
	ticker := time.NewTicker(rl.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		rl.cleanup()
	}
}

func (rl *CleanableRateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for ip, lastAccess := range rl.lastAccess {
		if now.Sub(lastAccess) > rl.maxAge {
			delete(rl.limiters, ip)
			delete(rl.lastAccess, ip)
		}
	}
}

func (rl *CleanableRateLimiter) GetLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	limiter, exists := rl.limiters[ip]
	if !exists {
		limiter = rate.NewLimiter(rl.rate, rl.burst)
		rl.limiters[ip] = limiter
	}
	rl.lastAccess[ip] = time.Now()
	return limiter
}
