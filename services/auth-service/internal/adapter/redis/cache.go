package redisadapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Iposhka54/task-tracker/services/auth-service/internal/port"
	goredis "github.com/redis/go-redis/v9"
)

type Cache struct {
	rdb *goredis.Client
}

func NewCache(rdb *goredis.Client) *Cache {
	return &Cache{rdb: rdb}
}

var _ port.Cache = (*Cache)(nil)

func (c *Cache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if err := c.rdb.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("cache set: %w", err)
	}
	return nil
}

func (c *Cache) Get(ctx context.Context, key string) (string, error) {
	val, err := c.rdb.Get(ctx, key).Result()
	if errors.Is(err, goredis.Nil) {
		return "", port.ErrCacheMiss
	}
	if err != nil {
		return "", fmt.Errorf("cache get: %w", err)
	}
	return val, nil
}

func (c *Cache) Del(ctx context.Context, key string) error {
	if err := c.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("cache del: %w", err)
	}
	return nil
}
