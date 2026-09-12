package logic

import (
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_HostThrottle_SpacesOutSameHost(t *testing.T) {

	th := newHostThrottle(2*time.Second, 16)
	t0 := time.Now()

	first := th.reserve("x.test", t0)
	second := th.reserve("x.test", t0)
	third := th.reserve("x.test", t0)

	assert.Equal(t, t0, first)
	assert.Equal(t, t0.Add(2*time.Second), second)
	assert.Equal(t, t0.Add(4*time.Second), third)
}

// One slow site must not hold up every other feed.
func Test_HostThrottle_DoesNotDelayOtherHosts(t *testing.T) {

	th := newHostThrottle(2*time.Second, 16)
	t0 := time.Now()

	th.reserve("x.test", t0)
	th.reserve("x.test", t0)

	assert.Equal(t, t0, th.reserve("y.test", t0))
}

// A host whose slot came free long ago starts over from
// now, rather than accumulating credit.
func Test_HostThrottle_ForgetsIdleHost(t *testing.T) {

	th := newHostThrottle(2*time.Second, 16)
	t0 := time.Now()
	th.reserve("x.test", t0)

	later := t0.Add(time.Hour)
	assert.Equal(t, later, th.reserve("x.test", later))
}

func Test_HostThrottle_PrunesStaleHosts(t *testing.T) {

	th := newHostThrottle(time.Second, 8)
	t0 := time.Now()
	for i := 0; i < 40; i++ {
		th.reserve(fmt.Sprintf("host-%d.test", i), t0)
	}

	// Every slot handed out above has come free by now,
	// so the next reserve clears them out.
	th.reserve("last.test", t0.Add(time.Minute))
	assert.LessOrEqual(t, len(th.nextFree), 8)
}

// Concurrent callers must each get their own slot: the
// whole point of reserving under the lock is that two
// goroutines cannot be told to hit the same host at the
// same instant.
func Test_HostThrottle_ConcurrentReservesDoNotDoubleBook(t *testing.T) {

	const callers = 24
	th := newHostThrottle(2*time.Second, 64)
	t0 := time.Now()

	slots := make([]time.Time, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			slots[n] = th.reserve("x.test", t0)
		}(i)
	}
	wg.Wait()

	sort.Slice(slots, func(a, b int) bool {
		return slots[a].Before(slots[b])
	})
	for i := 1; i < callers; i++ {
		gap := slots[i].Sub(slots[i-1])
		assert.Equal(t, 2*time.Second, gap,
			"callers %d and %d share a slot", i-1, i)
	}
}
