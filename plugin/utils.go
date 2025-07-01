package plugin

import (
	"context"
	"iter"
	"slices"
	"time"
)

// CollectError returns a slice of []K elements, gathered from
// a iter.Seq2 collection of [*K, error] pairs.
// If any element's error is non-nil, the slice will be nil,
// and the error will be returned.
func CollectError[T any](seq iter.Seq2[T, error]) ([]T, error) {
	var err error
	result := slices.Collect[T](func(yield func(t T) bool) {
		for k, v := range seq {
			if v != nil {
				err = v
				return
			}
			if !yield(k) {
				return
			}
		}
	})
	return result, err
}

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
