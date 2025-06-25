package plugin

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMockVault(t *testing.T) {
	ctx := t.Context()
	v, err := NewVault()
	require.NoError(t, err)
	secret, err := v.GenerateSecretId(ctx, "mock", "1.2.3.4", "fe80::/10", time.Minute, time.Minute)
	require.NoError(t, err)
	require.Equal(t, `mock-wrapped-token-for-1_2_3_4-and-fe80::_10`, secret)
}
