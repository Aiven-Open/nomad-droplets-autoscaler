package plugin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/quartz"
	"github.com/digitalocean/godo"
	"github.com/hashicorp/go-hclog"
)

type mockVaultProxy struct{}

func (v *mockVaultProxy) GenerateSecretId(
	ctx context.Context,
	appRole string,
	allowedIPv4, allowedIPv6 string,
	secretValidity, wrapperValidity time.Duration,
) (string, error) {
	return "abcd", nil
}

type mockGodo struct {
	counterDropletID atomic.Int32
	counterV4        atomic.Int32
	counterV6        atomic.Int32
	// prereservedIPv4s map[string]PrereservedIP
	// prereservedIPv6s map[string]PrereservedIPV6
	reservedIPv4s   []godo.ReservedIP
	reservedIPv6s   []godo.ReservedIPV6
	droplets        map[int]*godo.Droplet
	dropletUserData map[int]string
	dropletTags     map[int][]string
	mutex           *sync.Mutex
}

func (m *mockGodo) DropletActions() DropletActions {
	return &mockDropletActions{mock: m}
}

func (m *mockGodo) Droplets() Droplets {
	return &mockDroplets{mock: m}
}

func (m *mockGodo) Tags() Tags {
	return &mockTags{mock: m, tags: make(map[string]struct{})}
}

func (m *mockGodo) ReservedIPs() ReservedIPs {
	return &mockReservedIPs{mock: m}
}

func (m *mockGodo) ReservedIPV6s() ReservedIPV6s {
	return &mockReservedIPV6s{mock: m}
}

func (m *mockGodo) ReservedIPActions() ReservedIPActions {
	return &mockReservedIPActions{mock: m}
}

func (m *mockGodo) ReservedIPV6Actions() ReservedIPV6Actions {
	return &mockReservedIPV6Actions{mock: m}
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

type mockDropletActions struct {
	mock *mockGodo
}

func (m *mockDropletActions) PowerOff(
	ctx context.Context,
	dropletID int,
) (*godo.Action, *godo.Response, error) {
	m.mock.mutex.Lock()
	defer m.mock.mutex.Unlock()
	if droplet, exists := m.mock.droplets[dropletID]; exists {
		droplet.Status = "powered off"
		return nil, nil, nil
	} else {
		return nil, nil, errors.New("no such droplet")
	}
}

type mockDroplets struct {
	mock *mockGodo
}

func (m *mockDroplets) Delete(ctx context.Context, dropletID int) (*godo.Response, error) {
	m.mock.mutex.Lock()
	defer m.mock.mutex.Unlock()
	if _, exists := m.mock.droplets[dropletID]; exists {
		delete(m.mock.droplets, dropletID)
		return nil, nil
	} else {
		return nil, errors.New("no such droplet")
	}
}

func (m *mockDroplets) Get(
	ctx context.Context,
	dropletID int,
) (*godo.Droplet, *godo.Response, error) {
	m.mock.mutex.Lock()
	defer m.mock.mutex.Unlock()
	if droplet, exists := m.mock.droplets[dropletID]; exists {
		return droplet, nil, nil
	} else {
		return nil, nil, errors.New("no such droplet")
	}
}

func (m *mockDroplets) Create(
	ctx context.Context,
	req *godo.DropletCreateRequest,
) (*godo.Droplet, *godo.Response, error) {
	m.mock.mutex.Lock()
	defer m.mock.mutex.Unlock()
	region := godo.Region{Name: req.Region}
	id := int(m.mock.counterDropletID.Add(1))
	networks := &godo.Networks{
		V4: []godo.NetworkV4{
			{
				Gateway:   "2.2.2.254",
				Netmask:   "255.255.255.0",
				IPAddress: fmt.Sprintf("2.2.2.%v", id),
			},
		},
		V6: []godo.NetworkV6{},
	}
	// network.V4[0].
	droplet := &godo.Droplet{
		ID:       id,
		Name:     req.Name,
		Region:   &region,
		Tags:     req.Tags,
		Status:   "active",
		Networks: networks,
	}
	m.mock.dropletUserData[droplet.ID] = req.UserData
	m.mock.droplets[droplet.ID] = droplet
	return droplet, nil, nil
}

func (m *mockDroplets) ListByTag(
	ctx context.Context,
	tag string,
	options *godo.ListOptions,
) ([]godo.Droplet, *godo.Response, error) {
	m.mock.mutex.Lock()
	defer m.mock.mutex.Unlock()
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
	tags map[string]struct{}
}

func (m *mockTags) Create(
	ctx context.Context,
	req *godo.TagCreateRequest,
) (*godo.Tag, *godo.Response, error) {
	valid := regexp.MustCompile(`^[a-zA-Z0-9_\-\:]+$`)
	if !valid.MatchString(req.Name) {
		return nil, nil, errors.New("invalid tag name")
	}
	if _, exists := m.tags[req.Name]; exists {
		return nil, nil, errors.New("tag name already exists")
	}
	m.tags[req.Name] = struct{}{}
	return nil, nil, nil
}

func (m *mockTags) TagResources(
	ctx context.Context,
	tag string,
	req *godo.TagResourcesRequest,
) (*godo.Response, error) {
	m.mock.mutex.Lock()
	defer m.mock.mutex.Unlock()
	if len(req.Resources) != 1 {
		return nil, errors.New("expected exactly one resource")
	}
	res := req.Resources[0]
	if res.Type != "droplet" {
		return nil, errors.New("only support droplets for now")
	}
	dropletID, err := strconv.Atoi(res.ID)
	if err != nil {
		return nil, errors.New("droplet ID is not an integer")
	}
	if droplet, exists := m.mock.droplets[dropletID]; exists {
		droplet.Tags = append(droplet.Tags, tag)
		return nil, nil
	} else {
		return nil, errors.New("droplet does not exist")
	}
}

func (m *mockTags) UntagResources(
	ctx context.Context,
	tag string,
	req *godo.UntagResourcesRequest,
) (*godo.Response, error) {
	m.mock.mutex.Lock()
	defer m.mock.mutex.Unlock()
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

type mockReservedIPActions struct {
	mock *mockGodo
}

func (m *mockReservedIPActions) Assign(
	ctx context.Context,
	ip string,
	dropletID int,
) (*godo.Action, *godo.Response, error) {
	m.mock.mutex.Lock()
	defer m.mock.mutex.Unlock()
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
	m.mock.mutex.Lock()
	defer m.mock.mutex.Unlock()
	return m.mock.reservedIPv6s, nil, nil
}

func (m *mockReservedIPV6s) Create(
	ctx context.Context,
	req *godo.ReservedIPV6CreateRequest,
) (*godo.ReservedIPV6, *godo.Response, error) {
	m.mock.mutex.Lock()
	defer m.mock.mutex.Unlock()
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
	m.mock.mutex.Lock()
	defer m.mock.mutex.Unlock()
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

func createMockGodo() *mockGodo {
	return &mockGodo{
		reservedIPv4s:   make([]godo.ReservedIP, 0, 20),
		reservedIPv6s:   make([]godo.ReservedIPV6, 0, 20),
		droplets:        make(map[int]*godo.Droplet),
		dropletUserData: make(map[int]string),
		dropletTags:     make(map[int][]string),
		mutex:           new(sync.Mutex),
	}
}
