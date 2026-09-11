package logic

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_ArticleCache_ForgetsExpiredEntries(t *testing.T) {

	c := newArticleCache(time.Hour, 4)
	t0 := time.Now()
	c.put("https://x.test/a", "body", t0)

	text, ok := c.get("https://x.test/a", t0.Add(59*time.Minute))
	assert.True(t, ok)
	assert.Equal(t, "body", text)

	_, ok = c.get("https://x.test/a", t0.Add(time.Hour))
	assert.False(t, ok)
}

// An empty string is a remembered failure, and must
// read back as a hit so the caller does not re-fetch.
func Test_ArticleCache_RemembersFailures(t *testing.T) {

	c := newArticleCache(time.Hour, 4)
	t0 := time.Now()
	c.put("https://x.test/a", "", t0)

	text, ok := c.get("https://x.test/a", t0)
	assert.True(t, ok)
	assert.Equal(t, "", text)
}

func Test_ArticleCache_StaysWithinItsSizeLimit(t *testing.T) {

	c := newArticleCache(time.Hour, 4)
	t0 := time.Now()
	for i := 0; i < 20; i++ {
		c.put(
			fmt.Sprintf("https://x.test/%d", i),
			"body",
			t0.Add(time.Duration(i)*time.Second))
	}

	assert.LessOrEqual(t, len(c.entries), 4)
	// The newest survives; the oldest was evicted.
	_, ok := c.get("https://x.test/19", t0)
	assert.True(t, ok)
	_, ok = c.get("https://x.test/0", t0)
	assert.False(t, ok)
}

// Re-storing a URL already held must not evict anything:
// it takes no new slot.
func Test_ArticleCache_RefreshDoesNotEvict(t *testing.T) {

	c := newArticleCache(time.Hour, 2)
	t0 := time.Now()
	c.put("https://x.test/a", "one", t0)
	c.put("https://x.test/b", "two", t0)
	c.put("https://x.test/a", "one again", t0)

	assert.Equal(t, 2, len(c.entries))
	text, ok := c.get("https://x.test/b", t0)
	assert.True(t, ok)
	assert.Equal(t, "two", text)
}

// The cache is reached from the feed check loop and
// from HTTP handler goroutines at the same time, so its
// lock has to hold up. Meaningful under -race.
func Test_ArticleCache_ConcurrentUseIsSafe(t *testing.T) {

	c := newArticleCache(time.Hour, 16)
	t0 := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// Overlapping keys, so readers and writers
			// contend on the same entries.
			url := fmt.Sprintf("https://x.test/%d", n%8)
			for j := 0; j < 50; j++ {
				c.put(url, "body", t0)
				c.get(url, t0)
			}
		}(i)
	}
	wg.Wait()

	assert.LessOrEqual(t, len(c.entries), 16)
}
