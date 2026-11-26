package tally

import (
	"context"
	"strings"
	"sync"
	"time"
)

type cacheItem struct {
	value     any
	expiresAt time.Time
}

// MemoryCache provides a thread-safe in-memory cache with TTL expiration.
// It allows instant (< 1ms) responses for static/infrequent master data (companies, ledgers, stock items),
// bypassing the single-threaded Tally query queue on cache hits.
type MemoryCache struct {
	mu         sync.RWMutex
	items      map[string]cacheItem
	defaultTTL time.Duration
}

// NewMemoryCache initializes an in-memory TTL cache.
func NewMemoryCache(defaultTTL time.Duration) *MemoryCache {
	if defaultTTL <= 0 {
		defaultTTL = 30 * time.Second
	}
	return &MemoryCache{
		items:      make(map[string]cacheItem),
		defaultTTL: defaultTTL,
	}
}

// Get retrieves an unexpired value from the cache.
func (c *MemoryCache) Get(key string) (any, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.RLock()
	item, exists := c.items[key]
	c.mu.RUnlock()

	if !exists || time.Now().After(item.expiresAt) {
		return nil, false
	}
	return item.value, true
}

// Set stores a value in the cache with the default TTL.
func (c *MemoryCache) Set(key string, val any) {
	if c == nil {
		return
	}
	c.SetWithTTL(key, val, c.defaultTTL)
}

// SetWithTTL stores a value with an explicit TTL duration and lazily prunes expired entries.
func (c *MemoryCache) SetWithTTL(key string, val any, ttl time.Duration) {
	if c == nil {
		return
	}
	if ttl <= 0 {
		ttl = c.defaultTTL
	}

	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.items) > 64 {
		for k, item := range c.items {
			if now.After(item.expiresAt) {
				delete(c.items, k)
			}
		}
	}

	c.items[key] = cacheItem{
		value:     val,
		expiresAt: now.Add(ttl),
	}
}

// Invalidate removes entries starting with prefix, or all entries if prefix is empty.
func (c *MemoryCache) Invalidate(prefix string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if prefix == "" {
		c.items = make(map[string]cacheItem)
		return
	}

	for k := range c.items {
		if strings.HasPrefix(k, prefix) {
			delete(c.items, k)
		}
	}
}

// Size returns the count of currently cached items (including possibly expired ones before cleanup).
func (c *MemoryCache) Size() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

type flightCall struct {
	done chan struct{}
	val  any
	err  error
}

// Singleflight coalesces concurrent identical cache-miss fetches so Tally's single-threaded
// XML server is only queried once for concurrent identical requests.
type Singleflight struct {
	mu    sync.Mutex
	calls map[string]*flightCall
}

// Do executes fn once per in-flight key, sharing its result across concurrent callers
// while still honoring individual caller context cancellation.
func (g *Singleflight) Do(ctx context.Context, key string, fn func(context.Context) (any, error)) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*flightCall)
	}
	if call, ok := g.calls[key]; ok {
		g.mu.Unlock()
		select {
		case <-call.done:
			return call.val, call.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	call := &flightCall{done: make(chan struct{})}
	g.calls[key] = call
	g.mu.Unlock()

	go func() {
		defer func() {
			g.mu.Lock()
			delete(g.calls, key)
			g.mu.Unlock()
			close(call.done)
		}()
		call.val, call.err = fn(context.WithoutCancel(ctx))
	}()

	select {
	case <-call.done:
		return call.val, call.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
