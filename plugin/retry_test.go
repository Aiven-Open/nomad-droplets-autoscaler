package plugin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/assert"
)

func Test_retry(t *testing.T) {
	testCases := []struct {
		inputContext   context.Context
		inputInterval  time.Duration
		inputRetry     int
		inputFunc      retryFunc
		expectedOutput error
		name           string
	}{
		{
			inputContext:  t.Context(),
			inputInterval: 1 * time.Millisecond,
			inputRetry:    1,
			inputFunc: func(ctx context.Context, cancel context.CancelCauseFunc) error {
				return nil
			},
			expectedOutput: nil,
			name:           "successful function first time",
		},
		{
			inputContext:  t.Context(),
			inputInterval: 1 * time.Microsecond,
			inputRetry:    1,
			inputFunc: func(ctx context.Context, cancel context.CancelCauseFunc) error {
				return errors.New("error")
			},
			expectedOutput: errors.New("reached retry limit"),
			name:           "function never successful and reaches retry limit",
		},
	}

	logger := hclog.Default()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualOutput := retry(
				tc.inputContext,
				logger,
				tc.inputInterval,
				tc.inputRetry,
				tc.inputFunc,
			)
			assert.Equal(t, tc.expectedOutput, actualOutput, tc.name)
		})
	}
}
