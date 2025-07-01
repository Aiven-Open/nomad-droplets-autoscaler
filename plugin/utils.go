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

func countIf[T any](items []T, predicate func(T) bool) int64 {
	var count int64 = 0
	for _, item := range items {
		if predicate(item) {
			count += 1
		}
	}
	return count
}

// Must panics if it is given a non-nil error.
// Otherwise, it returns the first argument
func Must[T any](result T, err error) T {
	if err != nil {
		panic(err)
	}
	return result
}
