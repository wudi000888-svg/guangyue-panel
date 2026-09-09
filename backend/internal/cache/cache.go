// Package cache provides bounded, site-scoped ephemeral state. PostgreSQL/SQLite
// remain authoritative; a Redis outage is a cache miss, never a lost mutation.
package cache

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

const maxEntryBytes = 1 << 20

type entry struct {
	value   []byte
	expires time.Time
}
type Cache struct {
	client   *redis.Client
	prefix   string
	mu       sync.Mutex
	items    map[string]entry
	degraded atomic.Bool
}

func New(address, site string) (*Cache, error) {
	c := &Cache{prefix: "guangyue:" + site + ":", items: map[string]entry{}}
	if address != "" {
		opts, err := redis.ParseURL(address)
		if err != nil {
			return nil, errors.New("invalid Redis URL")
		}
		opts.PoolSize = 8
		opts.MinIdleConns = 0
		opts.MaxRetries = 0
		opts.DialTimeout = time.Second
		opts.ReadTimeout = time.Second
		opts.WriteTimeout = time.Second
		opts.ContextTimeoutEnabled = true
		c.client = redis.NewClient(opts)
	}
	return c, nil
}
func (c *Cache) Mode() string {
	if c == nil || c.client == nil {
		return "local"
	}
	return "redis"
}
func (c *Cache) Degraded() bool { return c != nil && c.degraded.Load() }
func (c *Cache) Ping(ctx context.Context) error {
	if c == nil || c.client == nil {
		return nil
	}
	err := c.client.Ping(ctx).Err()
	c.degraded.Store(err != nil)
	return err
}
func (c *Cache) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}
func (c *Cache) Get(ctx context.Context, key string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	if c.client != nil {
		ctx, cancel := context.WithTimeout(ctx, 350*time.Millisecond)
		defer cancel()
		v, err := c.client.Get(ctx, c.prefix+key).Bytes()
		c.degraded.Store(err != nil && !errors.Is(err, redis.Nil))
		return v, err == nil && len(v) <= maxEntryBytes
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok || time.Now().After(e.expires) {
		delete(c.items, key)
		return nil, false
	}
	return append([]byte(nil), e.value...), true
}
func (c *Cache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) {
	if c == nil || len(value) > maxEntryBytes || ttl <= 0 || ttl > time.Minute {
		return
	}
	if c.client != nil {
		ctx, cancel := context.WithTimeout(ctx, 350*time.Millisecond)
		defer cancel()
		c.degraded.Store(c.client.Set(ctx, c.prefix+key, value, ttl).Err() != nil)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= 16 {
		for k, v := range c.items {
			if time.Now().After(v.expires) {
				delete(c.items, k)
			}
		}
	}
	if len(c.items) >= 16 {
		for k := range c.items {
			delete(c.items, k)
			break
		}
	}
	c.items[key] = entry{append([]byte(nil), value...), time.Now().Add(ttl)}
}

// Invalidate clears only the named site and category. No FLUSHDB or broad keys
// are permitted on a shared Redis. Cache keys include runtime revision as well,
// preventing concurrent readers from reviving data across a no_logs transition.
func (c *Cache) Invalidate(ctx context.Context, category string) error {
	if c == nil {
		return nil
	}
	if strings.ContainsAny(category, "*?[]") {
		return errors.New("invalid cache category")
	}
	if c.client != nil {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		var cursor uint64
		for {
			keys, next, err := c.client.Scan(ctx, cursor, c.prefix+category+"*", 64).Result()
			if err != nil {
				return err
			}
			if len(keys) > 0 {
				if err = c.client.Del(ctx, keys...).Err(); err != nil {
					return err
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.items {
		if strings.HasPrefix(k, category) {
			delete(c.items, k)
		}
	}
	return nil
}
