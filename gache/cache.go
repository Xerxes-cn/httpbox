package gache

import "sync"

type cache struct {
	mu         sync.Mutex
	lru        *Cache
	cacheBytes int64
}

func (c *cache) add(key string, val ByteView) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lru == nil {
		c.lru = New(c.cacheBytes, nil)
	}
	c.lru.Add(key, val)
}

func (c *cache) get(key string) (val ByteView, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lru == nil {
		return
	}
	if v, ok := c.lru.Get(key); ok {
		return v.(ByteView), ok
	}
	return
}
