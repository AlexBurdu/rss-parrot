package logic

import (
	"sync"
	"time"
)

// articleCache remembers extraction results per
// article URL so the same page is not downloaded twice.
//
// The same article routinely shows up in more than one
// followed feed — a site's main feed and its per-tag
// feeds carry identical items, and each feed is its own
// parrot account with its own toot. Without the cache
// every one of those toots would re-fetch the page.
//
// Failures are cached too, and deliberately so: a page
// behind a paywall or a bot wall fails the same way on
// every attempt, and re-asking it once per sibling feed
// only wastes requests the site did not want.
type articleCache struct {
	mu      sync.Mutex
	entries map[string]articleCacheEntry
	ttl     time.Duration
	maxSize int
}

type articleCacheEntry struct {
	article  ExtractedArticle
	storedAt time.Time
}

func newArticleCache(
	ttl time.Duration,
	maxSize int,
) *articleCache {
	return &articleCache{
		entries: make(map[string]articleCacheEntry),
		ttl:     ttl,
		maxSize: maxSize,
	}
}

// get returns the cached extraction result for url and
// whether there was one. An empty article with ok=true
// is a remembered failure, and must not be retried.
func (c *articleCache) get(
	url string,
	now time.Time,
) (article ExtractedArticle, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, found := c.entries[url]
	if !found {
		return ExtractedArticle{}, false
	}
	if now.Sub(entry.storedAt) >= c.ttl {
		delete(c.entries, url)
		return ExtractedArticle{}, false
	}
	return entry.article, true
}

// put records an extraction result, making room first
// if the cache is full.
func (c *articleCache) put(
	url string,
	article ExtractedArticle,
	now time.Time,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, found := c.entries[url]; !found {
		c.makeRoom(now)
	}
	c.entries[url] = articleCacheEntry{
		article:  article,
		storedAt: now,
	}
}

// makeRoom keeps the cache under its size limit: first
// by dropping everything that has expired, and only if
// that was not enough by evicting the oldest entry.
// Caller holds the lock.
func (c *articleCache) makeRoom(now time.Time) {
	if len(c.entries) < c.maxSize {
		return
	}
	for url, entry := range c.entries {
		if now.Sub(entry.storedAt) >= c.ttl {
			delete(c.entries, url)
		}
	}
	if len(c.entries) < c.maxSize {
		return
	}
	oldestUrl := ""
	var oldestAt time.Time
	for url, entry := range c.entries {
		if oldestUrl == "" || entry.storedAt.Before(oldestAt) {
			oldestUrl, oldestAt = url, entry.storedAt
		}
	}
	delete(c.entries, oldestUrl)
}
