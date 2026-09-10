package logic

import (
	"fmt"
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
