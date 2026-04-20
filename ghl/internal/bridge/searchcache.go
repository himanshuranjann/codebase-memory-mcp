// Package bridge — SearchCache provides a short-lived in-memory cache for
// search tool results to avoid redundant grep pipeline work on repeated queries.
package bridge

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// cacheEntry holds a cached result and its expiry.
type cacheEntry struct {
	result    json.RawMessage
	expiresAt time.Time
	insertedAt time.Time
}

// SearchCache is a concurrent-safe, TTL-bounded in-memory cache for tool results.
// It is per-process (not shared across Cloud Run instances).
type SearchCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	maxSize int
	ttl     time.Duration
}

// NewSearchCache creates a SearchCache and starts a background goroutine that
// evicts expired entries every ttl/2 (minimum 5 s).
func NewSearchCache(maxSize int, ttl time.Duration) *SearchCache {
	c := &SearchCache{
		entries: make(map[string]*cacheEntry, maxSize),
		maxSize: maxSize,
		ttl:     ttl,
	}

	sweepInterval := ttl / 2
	if sweepInterval < 5*time.Second {
		sweepInterval = 5 * time.Second
	}
	go c.sweepLoop(sweepInterval)

	return c
}

// Key derives a cache key from the tool name and its arguments map.
// The key is a hex-encoded SHA-256 of "toolName\x00<canonical JSON of params>".
func (c *SearchCache) Key(toolName string, params map[string]interface{}) string {
	b, err := json.Marshal(params)
	if err != nil {
		// Fallback: uncacheable; return empty string (callers must handle "").
		return ""
	}
	h := sha256.New()
	h.Write([]byte(toolName))
	h.Write([]byte{0x00})
	h.Write(b)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// Get returns the cached result for key if it exists and has not expired.
// The second return value is false on a cache miss.
func (c *SearchCache) Get(key string) (json.RawMessage, bool) {
	if key == "" {
		return nil, false
	}
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.result, true
}

// Set stores result under key with the configured TTL.
// If the cache is at maxSize, the oldest entry is evicted first.
func (c *SearchCache) Set(key string, result json.RawMessage) {
	if key == "" || len(result) == 0 {
		return
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict oldest entry when at capacity (only when adding a new key).
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.maxSize {
		c.evictOldestLocked()
	}

	c.entries[key] = &cacheEntry{
		result:    result,
		expiresAt:  now.Add(c.ttl),
		insertedAt: now,
	}
}

// evictOldestLocked removes the entry with the earliest insertedAt.
// Must be called with c.mu held for writing.
func (c *SearchCache) evictOldestLocked() {
	var oldestKey string
	var oldestTime time.Time
	for k, e := range c.entries {
		if oldestKey == "" || e.insertedAt.Before(oldestTime) {
			oldestKey = k
			oldestTime = e.insertedAt
		}
	}
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

// sweepLoop periodically removes expired entries to bound memory usage.
func (c *SearchCache) sweepLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		c.mu.Lock()
		evicted := 0
		for k, e := range c.entries {
			if now.After(e.expiresAt) {
				delete(c.entries, k)
				evicted++
			}
		}
		c.mu.Unlock()
		if evicted > 0 {
			slog.Debug("search cache: swept expired entries", "evicted", evicted)
		}
	}
}
