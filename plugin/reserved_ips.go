package plugin

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/coder/quartz"
	"github.com/digitalocean/godo"
	"github.com/hashicorp/go-hclog"
)

type PrereservedIP struct {
	expiryTime time.Time
	reservedIP *godo.ReservedIP
}

func (p *PrereservedIP) String() string {
	return fmt.Sprintf("expiry time: %v; IP: %v", p.expiryTime, p.reservedIP.IP)
}

type PrereservedIPV6 struct {
	expiryTime time.Time
	reservedIP *godo.ReservedIPV6
}

type ReservedAddressesPool struct {
	mutex               *sync.RWMutex
	clock               quartz.Clock
	reservedIPs         ReservedIPs
	reservedIPActions   ReservedIPActions
	reservedIPV6s       ReservedIPV6s
	reservedIPV6Actions ReservedIPV6Actions

	logger             hclog.Logger
	rateLimiter        *rateLimiter
	rateLimiterOptions []rateLimiterOption

	prereservedIPs   map[string]PrereservedIP
	prereservedIPV6s map[string]PrereservedIPV6
}

// type Client interface{}

type reservedAddressesPoolOption func(*ReservedAddressesPool)

func WithDigitalOceanWrapper(wrapper DigitalOceanWrapper) reservedAddressesPoolOption {
	return func(r *ReservedAddressesPool) {
		r.reservedIPs = wrapper.ReservedIPs()
		r.reservedIPActions = wrapper.ReservedIPActions()

		r.reservedIPV6s = wrapper.ReservedIPV6s()
		r.reservedIPV6Actions = wrapper.ReservedIPV6Actions()
	}
}

func WithClient(
	reservedIPs ReservedIPs,
	reservedIPActions ReservedIPActions,
	reservedIPV6s ReservedIPV6s,
	reservedIPV6Actions ReservedIPV6Actions,
) reservedAddressesPoolOption {
	return func(r *ReservedAddressesPool) {
		r.reservedIPs = reservedIPs
		r.reservedIPActions = reservedIPActions

		r.reservedIPV6s = reservedIPV6s
		r.reservedIPV6Actions = reservedIPV6Actions
	}
}

func WithRateLimiterOption(o rateLimiterOption) reservedAddressesPoolOption {
	return func(r *ReservedAddressesPool) {
		r.rateLimiterOptions = append(r.rateLimiterOptions, o)
	}
}

func WithClock(c quartz.Clock) reservedAddressesPoolOption {
	return func(r *ReservedAddressesPool) {
		r.clock = c
	}
}

func CreateReservedAddressesPool(
	logger hclog.Logger,
	options ...reservedAddressesPoolOption,
) *ReservedAddressesPool {
	result := &ReservedAddressesPool{
		logger: logger.With("domain", "reserved IP address management"),
		clock:  quartz.NewReal(),
		mutex:  new(sync.RWMutex),
		// Note: In addition to the standard rate limiting, only 12 reserved IPs may be created per 60 seconds.
		rateLimiterOptions: make([]rateLimiterOption, 0),

		prereservedIPs:   make(map[string]PrereservedIP),
		prereservedIPV6s: make(map[string]PrereservedIPV6),
	}
	for _, option := range options {
		option(result)
	}
	result.rateLimiter = NewRateLimiter(12, 5*time.Second, true, result.rateLimiterOptions...)
	return result
}

func (r *ReservedAddressesPool) getReservedIPs(
	ctx context.Context,
) (map[string]*godo.ReservedIP, error) {
	ips, _, err := r.reservedIPs.List(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot enumerate reserved IPs: %w", err)
	}
	reservations := make(map[string]*godo.ReservedIP)
	for _, ip := range ips {
		reservations[ip.IP] = &ip
	}
	return reservations, nil
}

func (r *ReservedAddressesPool) getReservedIPV6s(
	ctx context.Context,
) (map[string]*godo.ReservedIPV6, error) {
	ipV6s, _, err := r.reservedIPV6s.List(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot enumerate reserved IPV6s: %w", err)
	}
	reservationsV6 := make(map[string]*godo.ReservedIPV6)
	for _, ip := range ipV6s {
		reservationsV6[ip.IP] = &ip
	}
	return reservationsV6, nil
}

// PrereserveIPs will find and return the specified number
// of reserved IP addresses. They will be provisionally reserved,
// meaning subsequent calls to this function will not return the
// same addresses until the expiry period has elapsed
func (r *ReservedAddressesPool) PrereserveIPs(
	ctx context.Context,
	count int,
	region string,
	createIfRequired bool,
	expiry time.Duration,
) ([]string, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	addresses := make(map[string]*godo.ReservedIP)

	// work out which droplets currently have IPv4 reservations, and
	// which unassigned reserved addresses we have
	reservedV4s, err := r.getReservedIPs(ctx)
	if err != nil {
		return nil, err
	}
	for _, reserved := range reservedV4s {
		if droplet := reserved.Droplet; droplet == nil {
			if prereservation, found := r.prereservedIPs[reserved.IP]; !found ||
				r.clock.Now().After(prereservation.expiryTime) {
				addresses[reserved.IP] = reserved
				if len(addresses) == count {
					break
				}
			}
		}
	}
	for len(addresses) != count {
		if createIfRequired {
			r.rateLimiter.Consume(ctx)
			if reservedV4, _, err := r.reservedIPs.Create(ctx, &godo.ReservedIPCreateRequest{Region: region}); err != nil {
				return nil, fmt.Errorf(
					"cannot create a new IPv4 address for region %v: %w",
					region,
					err,
				)
			} else {
				r.logger.Info("created (new) reserved IP addresses", "IPv4 address", reservedV4.IP)
				addresses[reservedV4.IP] = reservedV4
			}
		} else {
			return nil, fmt.Errorf("insufficient reserved IPv4 addresses")
		}
	}

	result := make([]string, 0, count)
	for ip, reservation := range addresses {
		result = append(result, ip)
		r.prereservedIPs[ip] = PrereservedIP{
			expiryTime: r.clock.Now().Add(expiry),
			reservedIP: reservation,
		}
	}

	return result, nil
}

func (r *ReservedAddressesPool) AssignIPv4(
	ctx context.Context,
	dropletID int,
	ipv4 string,
) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	prereservation, found := r.prereservedIPs[ipv4]
	if !found || r.clock.Now().After(prereservation.expiryTime) {
		return fmt.Errorf("trying to assign a IPv4 address which was not prereserved")
	}
	defer delete(r.prereservedIPs, ipv4)

	if err := RetryOnTransientError(ctx, r.logger,
		func(ctx context.Context, cancel context.CancelCauseFunc) error {
			_, _, err := r.reservedIPActions.Assign(ctx, ipv4, dropletID)
			return err
		}); err != nil {
		return fmt.Errorf(
			"cannot assign IPv4 %v to droplet %v: %w",
			ipv4,
			dropletID,
			err)
	}
	r.logger.Info("assigned reserved IPv4 address", "IPv4 address", ipv4)

	return nil
}

// PrereserveIPV6s will find and return the specified number
// of reserved IP addresses. They will be provisionally reserved,
// meaning subsequent calls to this function will not return the
// same addresses until the expiry period has elapsed
func (r *ReservedAddressesPool) PrereserveIPV6s(
	ctx context.Context,
	count int,
	region string,
	createIfRequired bool,
	expiry time.Duration,
) ([]string, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	addresses := make(map[string]*godo.ReservedIPV6)

	// work out which droplets currently have IPv4 reservations, and
	// which unassigned reserved addresses we have
	reservedV6s, err := r.getReservedIPV6s(ctx)
	if err != nil {
		return nil, err
	}
	for _, reserved := range reservedV6s {
		if droplet := reserved.Droplet; droplet == nil {
			if prereservation, found := r.prereservedIPV6s[reserved.IP]; !found ||
				r.clock.Now().After(prereservation.expiryTime) {
				addresses[reserved.IP] = reserved
				if len(addresses) == count {
					break
				}
			}
		}
	}
	for len(addresses) != count {
		if createIfRequired {
			r.rateLimiter.Consume(ctx)
			if reservedV6, _, err := r.reservedIPV6s.Create(ctx, &godo.ReservedIPV6CreateRequest{Region: region}); err != nil {
				return nil, fmt.Errorf(
					"cannot create a new IPv6 address for region %v: %w",
					region,
					err,
				)
			} else {
				r.logger.Info("created (new) reserved IP addresses", "IPv6 address", reservedV6.IP)
				addresses[reservedV6.IP] = reservedV6
			}
		} else {
			return nil, fmt.Errorf("insufficient reserved IPv4 addresses")
		}
	}

	result := make([]string, 0, count)
	for ip, reservation := range addresses {
		result = append(result, ip)
		r.prereservedIPV6s[ip] = PrereservedIPV6{
			expiryTime: r.clock.Now().Add(expiry),
			reservedIP: reservation,
		}
	}

	return result, nil
}

func (r *ReservedAddressesPool) AssignIPv6(
	ctx context.Context,
	dropletID int,
	ipv6 string,
) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	prereservation, found := r.prereservedIPV6s[ipv6]
	if !found || r.clock.Now().After(prereservation.expiryTime) {
		return fmt.Errorf("trying to assign a IPv6 address (%v) which was not prereserved", ipv6)
	}
	defer delete(r.prereservedIPV6s, ipv6)

	if err := RetryOnTransientError(ctx, r.logger,
		func(ctx context.Context, cancel context.CancelCauseFunc) error {
			_, _, err := r.reservedIPV6Actions.Assign(ctx, ipv6, dropletID)
			return err
		}); err != nil {
		return fmt.Errorf(
			"cannot assign IPv6 %v to droplet %v: %w",
			ipv6,
			dropletID,
			err)
	}
	r.logger.Info("assigned reserved IPv6 address", "IPv6 address", ipv6, "droplet ID", dropletID)

	return nil
}
