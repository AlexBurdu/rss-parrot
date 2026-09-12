package logic

import (
	"sync"
	"time"
)

// hostThrottle spaces out requests to any one host.
//
// A feed check can turn up a whole batch of new items
// from the same site at once, and each of them wants
// its own page download. Firing those back to back is
// exactly the behaviour a site rate-limits, so the
// throttle hands out one slot per host per gap.
type hostThrottle struct {
	mu       sync.Mutex
	nextFree map[string]time.Time
	gap      time.Duration
	// maxHosts is when to start dropping hosts whose
	// slot has already come free. A best-effort cap,
	// not a hard one: if every tracked host is still
	// waiting its turn there is nothing safe to drop,
	// and the map is allowed past it until some are.
	maxHosts int
}

func newHostThrottle(
	gap time.Duration,
	maxHosts int,
) *hostThrottle {
	return &hostThrottle{
		nextFree: make(map[string]time.Time),
		gap:      gap,
		maxHosts: maxHosts,
	}
}

// reserve books the next free slot for host and returns
// the time from which the caller may make its request.
// The slot is taken before reserve returns, so a
// concurrent caller for the same host is handed a later
// one rather than the same one.
func (t *hostThrottle) reserve(
	host string,
	now time.Time,
) time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	at := now
	if next, ok := t.nextFree[host]; ok && next.After(at) {
		at = next
	}
	t.prune(now, host)
	t.nextFree[host] = at.Add(t.gap)
	return at
}

// prune drops hosts whose slot has already come free,
// so the map does not grow with every site ever seen.
// keep is the host about to be re-inserted, left alone
// so a pruning pass cannot race its own caller.
// Caller holds the lock.
func (t *hostThrottle) prune(now time.Time, keep string) {
	if len(t.nextFree) < t.maxHosts {
		return
	}
	for host, next := range t.nextFree {
		if host != keep && !next.After(now) {
			delete(t.nextFree, host)
		}
	}
}
