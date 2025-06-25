package plugin

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/digitalocean/godo"
	"github.com/hashicorp/go-hclog"
)

// retryFunc is the function signature for a function which is retryable.
// A returned error is not considered fatal, but if the context is cancelled
// (or times out), that error will be returned
type retryFunc func(ctx context.Context, cancel context.CancelCauseFunc) error

// retry will retry the passed function f until any of the following conditions
// are met:
//   - the function return with err=nil
//   - the retryAttempts limit is reached
//   - the context is cancelled
func retry(
	ctx context.Context,
	logger hclog.Logger,
	retryInterval time.Duration,
	retryAttempts int,
	f retryFunc,
) error {
	var (
		retryCount    int
		lastErr, cerr error
	)
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	jitter := time.Duration(rand.Int64N(int64(retryInterval)))

	// randomly add/subtract up to 10% of the retry interval
	ticker := time.NewTicker(retryInterval + jitter/5 - retryInterval/10)
	defer ticker.Stop()

	for {
		err := f(ctx, cancel)

		if err == nil {
			if retryCount > 0 {
				logger.Info(
					"retry succeeded",
					"retry count", retryCount,
				)
			}
			return nil
		}

		if cerr = ctx.Err(); cerr != nil {
			break
		}
		lastErr = err
		logger.Info(
			"retry attempt failed",
			"retry count", retryCount,
			"error", err,
		)

		retryCount++

		if retryCount == retryAttempts {
			return errors.New("reached retry limit")
		}
		select {
		case <-ctx.Done():
			break
		case <-ticker.C:
		}
	}
	return fmt.Errorf(
		"giving up after %v retries as the context is cancelled: %w",
		retryCount,
		errors.Join(cerr, lastErr),
	)
}

// RetryOnTransientError will retry the provided callable
// if the error is one which is likely to indicate a transient error,
// which might just require some time to resolve.
// godo already handles rate-limiting, but HTTP 422s have been observed
// when trying to do things like conccurently assign multiple reserved IP addresses.
// If an unrecognise error is returned, this will exit as normal, immediately.
func RetryOnTransientError(
	ctx context.Context,
	logger hclog.Logger,
	f func(ctx context.Context, cancel context.CancelCauseFunc) error,
) error {
	return retry(ctx, logger, 10*time.Second, 30,
		func(ctx context.Context, cancel context.CancelCauseFunc) error {
			err := f(ctx, cancel)
			if err == nil {
				// success
				return nil
			}

			respErr := &godo.ErrorResponse{}
			if errors.As(err, &respErr) && respErr.Response != nil {
				logger.Info(
					"response is a DO HTTP error",
					"response",
					fmt.Sprintf("%+v", respErr.Response),
				)
				if respErr.Response.StatusCode == 422 {
					// try again
					return err
				}
			}

			// do not retry
			cancel(err)
			return err
		})
}
