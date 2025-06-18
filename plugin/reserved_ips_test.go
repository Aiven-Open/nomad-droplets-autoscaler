package plugin

import (
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/digitalocean/godo"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
)

func TestReserveIPv4(t *testing.T) {
	ctx := t.Context()
	mock := createMockGodo()
	clock := quartz.NewMock(t)
	pool := mock.NewReservedAddressPool(hclog.New(&hclog.LoggerOptions{
		Name:  "test",
		Level: hclog.LevelFromString("TRACE"),
	}), clock)

	// request 2 IPv4 addresses without allowing creation. This should fail.
	_, err := pool.PrereserveIPs(ctx, 2, "mel1", false, time.Minute)
	require.Error(t, err)

	// request 2, allowing creation
	preservedV4s, err := pool.PrereserveIPs(ctx, 2, "mel1", true, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, preservedV4s)
	require.Len(t, preservedV4s, 2)

	// wait until they expire
	require.NoError(t, clock.Advance(2*time.Minute).Wait(ctx))

	// define 2 droplets (as though made with Droplets.Create(...)
	mock.droplets[1] = &godo.Droplet{ID: 1}
	mock.droplets[2] = &godo.Droplet{ID: 2}

	// try to assign one of these addresses to a droplet and assert it fails
	require.Error(t, pool.AssignIPv4(ctx, mock.droplets[1], preservedV4s[0]))
	require.Error(t, pool.AssignIPv4(ctx, mock.droplets[2], preservedV4s[1]))

	// request 2 without allowing creation, which should succeed
	preservedV4s, err = pool.PrereserveIPs(ctx, 2, "mel1", false, time.Minute)
	require.NoError(t, err)

	// assign one to a droplet, which should succeed
	require.NoError(t, pool.AssignIPv4(ctx, mock.droplets[1], preservedV4s[0]))

	// assign the same one to a different droplet (should fail)
	require.Error(t, pool.AssignIPv4(ctx, mock.droplets[2], preservedV4s[0]))

	// assign the second one to a second droplet
	require.NoError(t, pool.AssignIPv4(ctx, mock.droplets[2], preservedV4s[1]))
}

func TestReserveIPv6(t *testing.T) {
	ctx := t.Context()
	mock := createMockGodo()
	clock := quartz.NewMock(t)
	pool := mock.NewReservedAddressPool(hclog.New(&hclog.LoggerOptions{
		Name:  "test",
		Level: hclog.LevelFromString("TRACE"),
	}), clock)

	// request 2 IPv6 addresses without allowing creation. This should fail.
	_, err := pool.PrereserveIPV6s(ctx, 2, "mel1", false, time.Minute)
	require.Error(t, err)

	// request 2, allowing creation
	preservedV6s, err := pool.PrereserveIPV6s(ctx, 2, "mel1", true, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, preservedV6s)
	require.Len(t, preservedV6s, 2)

	// wait until they expire
	require.NoError(t, clock.Advance(2*time.Minute).Wait(ctx))

	// define 2 droplets (as though made with Droplets.Create(...)
	mock.droplets[1] = &godo.Droplet{ID: 1}
	mock.droplets[2] = &godo.Droplet{ID: 2}

	// try to assign one of these addresses to a droplet and assert it fails
	require.Error(t, pool.AssignIPv6(ctx, mock.droplets[1], preservedV6s[0]))
	require.Error(t, pool.AssignIPv6(ctx, mock.droplets[2], preservedV6s[1]))

	// request 2 without allowing creation, which should succeed
	preservedV6s, err = pool.PrereserveIPV6s(ctx, 2, "mel1", false, time.Minute)
	require.NoError(t, err)

	// assign one to a droplet, which should succeed
	require.NoError(t, pool.AssignIPv6(ctx, mock.droplets[1], preservedV6s[0]))

	// assign the same one to a different droplet (should fail)
	require.Error(t, pool.AssignIPv6(ctx, mock.droplets[2], preservedV6s[0]))

	// assign the second one to a second droplet
	require.NoError(t, pool.AssignIPv6(ctx, mock.droplets[2], preservedV6s[1]))
}
