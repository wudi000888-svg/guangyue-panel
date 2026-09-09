package cache

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestLocalCacheBoundsAndCopies(t *testing.T) {
	c, _ := New("", "lite")
	ctx := context.Background()
	v := []byte("before")
	c.Set(ctx, "one", v, time.Minute)
	v[0] = '!'
	b, ok := c.Get(ctx, "one")
	if !ok || string(b) != "before" {
		t.Fatal("input not copied")
	}
	b[0] = '!'
	b, _ = c.Get(ctx, "one")
	if string(b) != "before" {
		t.Fatal("output not copied")
	}
	for i := 0; i < 30; i++ {
		c.Set(ctx, fmt.Sprint(i), v, time.Second)
	}
	if len(c.items) > 16 {
		t.Fatal("unbounded cache")
	}
	c.Set(ctx, "expired", v, time.Nanosecond)
	time.Sleep(time.Millisecond)
	if _, ok = c.Get(ctx, "expired"); ok {
		t.Fatal("expired cache returned")
	}
}
func TestRedisSiteIsolationAndInvalidation(t *testing.T) {
	address := os.Getenv("GY_TEST_REDIS_URL")
	if address == "" {
		t.Skip("Redis integration not configured")
	}
	site := fmt.Sprintf("test_%x", time.Now().UnixNano())
	a, err := New(address, site+"a")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := New(address, site+"b")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx := context.Background()
	if err = a.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err = b.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	a.Set(ctx, "stats:x", []byte("first"), 10*time.Second)
	b.Set(ctx, "stats:x", []byte("second"), 10*time.Second)
	if err = a.Invalidate(ctx, "stats:"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Get(ctx, "stats:x"); ok {
		t.Fatal("invalidation failed")
	}
	if v, ok := b.Get(ctx, "stats:x"); !ok || string(v) != "second" {
		t.Fatal("cross-site deletion")
	}
}
