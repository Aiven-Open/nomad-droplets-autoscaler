package plugin

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/coder/quartz"
)

type rateLimiter struct {
	mutex          *sync.Mutex
	burst, current uint32
	rechargePeriod time.Duration
	nextCheck      time.Time
	clock          quartz.Clock
}

func (r *rateLimiter) String() string {
	return fmt.Sprintf(
		"%v: %v, next check in %v\n",
		r.clock.Now().GoString(),
		r.current,
		r.nextCheck.Sub(r.clock.Now()),
	)
}

type rateLimiterOption func(*rateLimiter)

func WithMockClock(m *quartz.Mock) rateLimiterOption {
	return func(r *rateLimiter) {
		r.clock = m
		r.nextCheck = m.Now().Add(r.rechargePeriod)
	}
}

func NewRateLimiter(
	burst uint32,
	rechargePeriod time.Duration,
	startFull bool,
	options ...rateLimiterOption,
) *rateLimiter {
	clock := quartz.NewReal()
	result := &rateLimiter{
		burst:          burst,
		rechargePeriod: rechargePeriod,
		mutex:          new(sync.Mutex),
		nextCheck:      clock.Now().Add(rechargePeriod),
		clock:          clock,
	}
	if startFull {
		result.current = burst
	}
	for _, option := range options {
		option(result)
	}
	return result
}

func (r *rateLimiter) Consume(ctx context.Context) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	now := r.clock.Now()
	for {
		if r.current == r.burst {
			r.nextCheck = now.Add(r.rechargePeriod)
			break
		}
		if r.nextCheck.After(now) {
			break
		}
		r.current += 1
		r.nextCheck = r.nextCheck.Add(r.rechargePeriod)
	}
	if r.current > 0 {
		r.current -= 1
		return
	}

	// wait until the next tick, or the context expires.
	// Note that if the context expires, the rate-limiter
	// token we were waiting for is NOT consumed.
	timer := r.clock.NewTimer(r.nextCheck.Sub(now))
	select {
	case <-timer.C:
		r.nextCheck = r.nextCheck.Add(r.rechargePeriod)
		return
	case <-ctx.Done():
		return // ctx.Err()
	}
}
