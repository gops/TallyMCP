package tally

import (
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
	mu      sync.RWMutex
	items   map[string]cacheItem
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
	defer c.mu.RUnlock()

	item, exists := c.items[key]
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

// SetWithTTL stores a value with an explicit TTL duration.
func (c *MemoryCache) SetWithTTL(key string, val any, ttl time.Duration) {
	if c == nil {
		return
	}
	if ttl <= 0 {
		ttl = c.defaultTTL
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.items[key] = cacheItem{
		value:     val,
		expiresAt: time.Now().Add(ttl),
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
