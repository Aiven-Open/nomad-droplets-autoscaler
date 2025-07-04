package plugin_test

import (
	"context"
	"testing"
	"time"

	"github.com/Aiven-Open/nomad-droplets-autoscaler/plugin"
	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
)

func TestRateLimiter(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	clock := quartz.NewMock(t)
	initialTime := clock.Now()

	// burst of 2, 5 second recharge, starting full
	rl := plugin.NewRateLimiter(2, 5*time.Second, true, plugin.WithMockClock(clock))

	// first call returns instantly
	rl.Consume(ctx)
	assert.Equal(t, clock.Now(), initialTime)

	// second call returns instantly
	rl.Consume(ctx)
	assert.Equal(t, clock.Now(), initialTime)

	// expect the third call will start a timer, so set a trap
	trap := clock.Trap().NewTimer()
	defer trap.Close()

	go rl.Consume(ctx)

	// wait for the trap to be called (ie; the timer is created)
	call := trap.MustWait(ctx)
	call.MustRelease(ctx)

	// now allow the mock time to flow until the next event. This
	// should be 5 seconds later, when the timer fires
	_, w := clock.AdvanceNext()
	w.MustWait(ctx)
	assert.Equal(t, clock.Now(), initialTime.Add(5*time.Second))

	// advance the time 8 seconds. This should allow the next
	// token to be immediately available, and the following one
	// to be available 2 seconds later
	clock.Advance(8 * time.Second).MustWait(ctx)
	initialTime = clock.Now()
	rl.Consume(ctx)
	assert.Equal(t, clock.Now(), initialTime)

	// a trap is already set for the NewTimer() call,
	// so start the consume operation
	go rl.Consume(ctx)
	// wait until NewTimer is called in the other goroutine
	call = trap.MustWait(ctx)
	call.MustRelease(ctx)
	// allow the clock to advance to the next event
	_, w = clock.AdvanceNext()
	w.MustWait(ctx)
	// .. which should be 2 seconds later
	assert.Equal(t, clock.Now(), initialTime.Add(2*time.Second))
}
