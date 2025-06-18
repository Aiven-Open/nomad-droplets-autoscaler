package plugin_test

import (
	"context"
	"fmt"
	"net"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/Aiven-Open/nomad-droplets-autoscaler/plugin"
	"github.com/coder/quartz"
	"github.com/digitalocean/godo"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
)

// TODO:
// 		test that still having a tag doesn't cause the system to try to
// re-assign a reserved address if it has one already.

type mockGodo struct {
	counterV4    atomic.Int32
	counterV6    atomic.Int32
	reservedIPv4 []godo.ReservedIP
	reservedIPv6 []godo.ReservedIPV6
	droplets     map[int]*godo.Droplet
}

func (m *mockGodo) GetReservedIPv4(dropletID int) *godo.ReservedIP {
	for _, reservedIP := range m.reservedIPv4 {
		if reservedIP.Droplet != nil && reservedIP.Droplet.ID == dropletID {
			return &reservedIP
		}
	}
	return nil
}

func (m *mockGodo) GetReservedIPv6(dropletID int) *godo.ReservedIPV6 {
	for _, reservedIP := range m.reservedIPv6 {
		if reservedIP.Droplet != nil && reservedIP.Droplet.ID == dropletID {
			return &reservedIP
		}
	}
	return nil
}

type mockReservedIPs struct {
	mock *mockGodo
}

func (m *mockReservedIPs) List(
	ctx context.Context,
	lo *godo.ListOptions,
) ([]godo.ReservedIP, *godo.Response, error) {
	return m.mock.reservedIPv4, nil, nil
}

func (m *mockReservedIPs) Create(
	ctx context.Context,
	req *godo.ReservedIPCreateRequest,
) (*godo.ReservedIP, *godo.Response, error) {
	if req.DropletID == 0 {
		panic("only supporting droplet assignment in this mock")
	}
	ipv4 := fmt.Sprintf("1.2.3.%v", m.mock.counterV4.Add(1))
	droplet, exists := m.mock.droplets[req.DropletID]
	if !exists {
		return nil, nil, fmt.Errorf("droplet %v does not exist", req.DropletID)
	}
	// TODO: verify not already in reservedIPv4
	result := godo.ReservedIP{Droplet: droplet, IP: ipv4}
	m.mock.reservedIPv4 = append(m.mock.reservedIPv4, result)
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
		for i, reservedIP := range m.mock.reservedIPv4 {
			if reservedIP.Droplet == nil {
				reservedIP.Droplet = droplet
				m.mock.reservedIPv4[i] = reservedIP
				return nil, nil, nil
			}
		}
		return nil, nil, fmt.Errorf("no IPs are available")
	} else {
		return nil, nil, fmt.Errorf("droplet does not exist")
	}
}

type mockReservedIPV6s struct {
	mock *mockGodo
}

func (m *mockReservedIPV6s) List(
	ctx context.Context,
	lo *godo.ListOptions,
) ([]godo.ReservedIPV6, *godo.Response, error) {
	return m.mock.reservedIPv6, nil, nil
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
	m.mock.reservedIPv6 = append(m.mock.reservedIPv6, result)
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
		for i, reservedIP := range m.mock.reservedIPv6 {
			if reservedIP.Droplet == nil {
				reservedIP.Droplet = droplet
				m.mock.reservedIPv6[i] = reservedIP
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
		reservedIPv4: make([]godo.ReservedIP, 0, 20),
		reservedIPv6: make([]godo.ReservedIPV6, 0, 20),
		droplets:     make(map[int]*godo.Droplet),
	}
}

type mockDroplets struct {
	mock *mockGodo
}

func (m *mockDroplets) ListByTag(
	ctx context.Context,
	tag string,
	options *godo.ListOptions,
) ([]godo.Droplet, *godo.Response, error) {
	response := godo.Response{}
	return slices.Collect(func(yield func(godo.Droplet) bool) {
		for _, d := range m.mock.droplets {
			if d.Tags != nil && slices.Contains(d.Tags, tag) {
				if !yield(*d) {
					return
				}
			}
		}
	}), &response, nil
}

type mockTags struct {
	mock *mockGodo
}

func (m *mockTags) UntagResources(
	ctx context.Context,
	tag string,
	req *godo.UntagResourcesRequest,
) (*godo.Response, error) {
	if req.Resources == nil || len(req.Resources) != 1 {
		panic("one resource must be requested")
	}
	resource := req.Resources[0]
	if resource.Type != godo.DropletResourceType {
		panic("resource type must be droplet")
	}
loop1:
	for i, droplet := range m.mock.droplets {
		if droplet.Tags != nil {
			nTags := len(droplet.Tags)
			for j, dropletTag := range droplet.Tags {
				if dropletTag == tag {
					if nTags-1 > j {
						droplet.Tags[j] = droplet.Tags[nTags-1]
					}
					droplet.Tags = droplet.Tags[:nTags-1]
					m.mock.droplets[i] = droplet
					continue loop1
				}
			}
		}
	}
	return nil, nil
}

func (m *mockGodo) NewReservedAddressPool(
	logger hclog.Logger,
	clock *quartz.Mock,
) *plugin.ReservedAddressesPool {
	return plugin.CreateReservedAddressesPool(
		logger,
		plugin.WithClient(
			&mockReservedIPs{mock: m},
			&mockReservedIPActions{mock: m},
			&mockReservedIPV6s{mock: m},
			&mockReservedIPV6Actions{mock: m},
			&mockDroplets{mock: m},
			&mockTags{mock: m},
		),
		plugin.WithRateLimiterOption(plugin.WithMockClock(clock)),
	)
}

func TestReserveIPv4(t *testing.T) {
	requiresIPv4Tag := "requires-ip-v4-reserved-address"
	mock := createMockGodo()
	clock := quartz.NewMock(t)
	pool := mock.NewReservedAddressPool(hclog.New(&hclog.LoggerOptions{
		Name:  "test",
		Level: hclog.LevelFromString("TRACE"),
	}), clock)

	// define 2 droplets which require reserved IPv4 and IPv6 IP addresses,
	mock.droplets[1] = &godo.Droplet{ID: 1, Tags: []string{requiresIPv4Tag}}
	mock.droplets[2] = &godo.Droplet{ID: 2, Tags: []string{requiresIPv4Tag}}

	// try to apply the IPv4 reserved addresses, disallowing the creation of new reserved addresses
	err := pool.AssignMissingIPv4(t.Context(), false, requiresIPv4Tag)
	require.Error(t, err)
	require.Equal(t, err, fmt.Errorf("insufficient reserved IPv4 addresses"))
	require.Equal(t, 0, len(mock.reservedIPv4))

	// allow the creation of addresses, which should now succeed
	require.NoError(t, pool.AssignMissingIPv4(t.Context(), true, requiresIPv4Tag))

	require.Equal(t, 2, len(mock.reservedIPv4))
	require.Equal(t, 1+2, mock.reservedIPv4[0].Droplet.ID+mock.reservedIPv4[1].Droplet.ID)
}

func TestReserveIPv6(t *testing.T) {
	requiresIPv6Tag := "requires-ip-v6-reserved-address"
	mock := createMockGodo()
	clock := quartz.NewMock(t)
	pool := mock.NewReservedAddressPool(hclog.New(&hclog.LoggerOptions{
		Name:  "test",
		Level: hclog.LevelFromString("TRACE"),
	}), clock)

	// define 2 droplets which require reserved IPv6 IP addresses,
	region := godo.Region{Name: "mel1"}
	mock.droplets[1] = &godo.Droplet{
		ID:       1,
		Region:   &region,
		Tags:     []string{requiresIPv6Tag},
		Networks: &godo.Networks{V6: []godo.NetworkV6{{Type: "public", IPAddress: "6.6.6.200"}}},
	}
	mock.droplets[2] = &godo.Droplet{
		ID:       2,
		Region:   &region,
		Tags:     []string{requiresIPv6Tag},
		Networks: &godo.Networks{V6: []godo.NetworkV6{{Type: "public", IPAddress: "6.6.6.201"}}},
	}

	// try to apply the IPv6 reserved addresses, disallowing the creation of new reserved addresses
	err := pool.AssignMissingIPv6(t.Context(), false, requiresIPv6Tag)
	require.Error(t, err)
	require.Equal(t, err, fmt.Errorf("insufficient reserved IPv6 addresses"))

	// allow the creation of addresses, which should now succeed
	require.NoError(t, pool.AssignMissingIPv6(t.Context(), true, requiresIPv6Tag))

	require.Equal(t, 2, len(mock.reservedIPv6))
	require.Equal(t, 1+2, mock.reservedIPv6[0].Droplet.ID+mock.reservedIPv6[1].Droplet.ID)
}
