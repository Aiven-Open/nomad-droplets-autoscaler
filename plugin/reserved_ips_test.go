package plugin

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/digitalocean/godo"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
)

type mockGodo struct {
	counterV4 atomic.Int32
	counterV6 atomic.Int32
	// prereservedIPv4s map[string]PrereservedIP
	// prereservedIPv6s map[string]PrereservedIPV6
	reservedIPv4s []godo.ReservedIP
	reservedIPv6s []godo.ReservedIPV6
	droplets      map[int]*godo.Droplet
}

func (m *mockGodo) GetReservedIPv4(dropletID int) *godo.ReservedIP {
	for _, reservedIP := range m.reservedIPv4s {
		if reservedIP.Droplet != nil && reservedIP.Droplet.ID == dropletID {
			return &reservedIP
		}
	}
	return nil
}

func (m *mockGodo) GetReservedIPv6(dropletID int) *godo.ReservedIPV6 {
	for _, reservedIP := range m.reservedIPv6s {
		if reservedIP.Droplet != nil && reservedIP.Droplet.ID == dropletID {
			return &reservedIP
		}
	}
	return nil
}

type mockReservedIPs struct {
	clock quartz.Clock
	mock  *mockGodo
}

func (m *mockReservedIPs) List(
	ctx context.Context,
	lo *godo.ListOptions,
) ([]godo.ReservedIP, *godo.Response, error) {
	return m.mock.reservedIPv4s, nil, nil
}

func (m *mockReservedIPs) Create(
	ctx context.Context,
	req *godo.ReservedIPCreateRequest,
) (*godo.ReservedIP, *godo.Response, error) {
	if req.DropletID != 0 {
		panic("not supporting droplet assignment in this mock")
	}
	if req.Region == "" {
		panic("only supporting region assignment in this mock")
	}
	ipv4 := fmt.Sprintf("1.2.3.%v", m.mock.counterV4.Add(1))
	// TODO: verify not already in reservedIPv4
	r := godo.Region{Name: req.Region}
	result := godo.ReservedIP{Region: &r, IP: ipv4}
	m.mock.reservedIPv4s = append(m.mock.reservedIPv4s, result)
	/*
		m.mock.prereservedIPv4s[ipv4] = PrereservedIP{
			expiryTime: m.clock.Now().Add(time.Minute),
			reservedIP: &result,
		}
	*/
	return &result, nil, nil
}

type mockReservedIPActions struct {
	mock *mockGodo
}

func (m *mockReservedIPActions) Assign(
	ctx context.Context,
	ip string,
	dropletID int,
) (*godo.Action, *godo.Response, error) {
	if droplet := m.mock.GetReservedIPv4(dropletID); droplet != nil {
		return nil, nil, fmt.Errorf("droplet already has an IPv4 reservation")
	}
	if droplet, exists := m.mock.droplets[dropletID]; exists {
		for i, reservedIP := range m.mock.reservedIPv4s {
			if reservedIP.Droplet == nil {
				reservedIP.Droplet = droplet
				m.mock.reservedIPv4s[i] = reservedIP
				return nil, nil, nil
			}
		}
		return nil, nil, fmt.Errorf("no IPs are available")
	} else {
		return nil, nil, fmt.Errorf("droplet does not exist")
	}
}

type mockReservedIPV6s struct {
	clock quartz.Clock
	mock  *mockGodo
}

func (m *mockReservedIPV6s) List(
	ctx context.Context,
	lo *godo.ListOptions,
) ([]godo.ReservedIPV6, *godo.Response, error) {
	return m.mock.reservedIPv6s, nil, nil
}

func (m *mockReservedIPV6s) Create(
	ctx context.Context,
	req *godo.ReservedIPV6CreateRequest,
) (*godo.ReservedIPV6, *godo.Response, error) {
	if req.Region == "" {
		panic("must supply a region")
	}

	// Create a 16-byte slice for the IPv6 address
	ipBytes := make([]byte, net.IPv6len)

	// Set the prefix for a link-local address (fe80::/10)
	ipBytes[0] = 0xfe
	ipBytes[1] = 0x80
	counter := m.mock.counterV6.Add(1)
	ipBytes[2] = byte(counter / 256)
	ipBytes[3] = byte(counter % 256)
	ipv6 := net.IP(ipBytes)
	result := godo.ReservedIPV6{RegionSlug: req.Region, IP: ipv6.String()}
	m.mock.reservedIPv6s = append(m.mock.reservedIPv6s, result)
	return &result, nil, nil
}

type mockReservedIPV6Actions struct {
	mock *mockGodo
}

func (m *mockReservedIPV6Actions) Assign(
	ctx context.Context,
	ip string,
	dropletID int,
) (*godo.Action, *godo.Response, error) {
	if droplet := m.mock.GetReservedIPv6(dropletID); droplet != nil {
		return nil, nil, fmt.Errorf("droplet already has an IPv6 reservation")
	}
	if droplet, exists := m.mock.droplets[dropletID]; exists {
		for i, reservedIP := range m.mock.reservedIPv6s {
			if reservedIP.Droplet == nil {
				reservedIP.Droplet = droplet
				m.mock.reservedIPv6s[i] = reservedIP
				return nil, nil, nil
			}
		}
		return nil, nil, fmt.Errorf("no IPs are available")
	} else {
		return nil, nil, fmt.Errorf("droplet does not exist")
	}
}

func createMockGodo() *mockGodo {
	return &mockGodo{
		reservedIPv4s: make([]godo.ReservedIP, 0, 20),
		reservedIPv6s: make([]godo.ReservedIPV6, 0, 20),
		droplets:      make(map[int]*godo.Droplet),
	}
}

func (m *mockGodo) NewReservedAddressPool(
	logger hclog.Logger,
	clock *quartz.Mock,
) *ReservedAddressesPool {
	return CreateReservedAddressesPool(
		logger,
		WithClock(clock),
		WithClient(
			&mockReservedIPs{mock: m, clock: clock},
			&mockReservedIPActions{mock: m},
			&mockReservedIPV6s{mock: m, clock: clock},
			&mockReservedIPV6Actions{mock: m},
		),
		WithRateLimiterOption(WithMockClock(clock)),
	)
}

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
