package plugin

import (
	"context"
	"sync"
	"time"
)

func Sleep(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Must panics if it is given a non-nil error.
// Otherwise, it returns the first argument
func Must[T any](result T, err error) T {
	if err != nil {
		panic(err)
	}
	return result
}

func Must0(err error) {
	if err != nil {
		panic(err)
	}
}

var (
	debounceMap   map[string]time.Time // records the time the function was last run
	debounceMutex *sync.Mutex
)

func init() {
	debounceMap = make(map[string]time.Time)
	debounceMutex = new(sync.Mutex)
}

func debounce(key string, fn func(), period time.Duration) {
	debounceMutex.Lock()
	defer debounceMutex.Unlock()
	if lastrun, found := debounceMap[key]; found {
		if time.Since(lastrun) < period {
			return
		}
	}
	go fn()
	debounceMap[key] = time.Now()
}
