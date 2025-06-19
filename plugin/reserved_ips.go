package plugin

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/digitalocean/godo"
	"github.com/hashicorp/go-hclog"
)

type (
	IP   string
	IPv6 string
)

type ReservedIPs interface {
	List(context.Context, *godo.ListOptions) ([]godo.ReservedIP, *godo.Response, error)
	Create(
		context.Context,
		*godo.ReservedIPCreateRequest,
	) (*godo.ReservedIP, *godo.Response, error)
}

type ReservedIPActions interface {
	Assign(context.Context, string, int) (*godo.Action, *godo.Response, error)
}

type ReservedIPV6Actions interface {
	Assign(context.Context, string, int) (*godo.Action, *godo.Response, error)
}

type ReservedIPV6s interface {
	List(context.Context, *godo.ListOptions) ([]godo.ReservedIPV6, *godo.Response, error)
	Create(
		context.Context,
		*godo.ReservedIPV6CreateRequest,
	) (*godo.ReservedIPV6, *godo.Response, error)
}

type Droplets interface {
	ListByTag(context.Context, string, *godo.ListOptions) ([]godo.Droplet, *godo.Response, error)
}

type Tags interface {
	UntagResources(context.Context, string, *godo.UntagResourcesRequest) (*godo.Response, error)
}

type ReservedAddressesPool struct {
	mutex               *sync.RWMutex
	reservedIPs         ReservedIPs
	reservedIPActions   ReservedIPActions
	reservedIPV6s       ReservedIPV6s
	reservedIPV6Actions ReservedIPV6Actions
	droplets            Droplets
	tags                Tags
	logger              hclog.Logger
	rateLimiter         *rateLimiter
	rateLimiterOptions  []rateLimiterOption
}

// type Client interface{}

type reservedAddressesPoolOption func(*ReservedAddressesPool)

func WithGodoClient(client *godo.Client) reservedAddressesPoolOption {
	return func(r *ReservedAddressesPool) {
		r.reservedIPs = client.ReservedIPs
		r.reservedIPActions = client.ReservedIPActions

		r.reservedIPV6s = client.ReservedIPV6s
		r.reservedIPV6Actions = client.ReservedIPV6Actions

		r.droplets = client.Droplets
		r.tags = client.Tags
	}
}

func WithClient(
	reservedIPs ReservedIPs,
	reservedIPActions ReservedIPActions,
	reservedIPV6s ReservedIPV6s,
	reservedIPV6Actions ReservedIPV6Actions,
	droplets Droplets,
	tags Tags,
) reservedAddressesPoolOption {
	return func(r *ReservedAddressesPool) {
		r.reservedIPs = reservedIPs
		r.reservedIPActions = reservedIPActions

		r.reservedIPV6s = reservedIPV6s
		r.reservedIPV6Actions = reservedIPV6Actions

		r.droplets = droplets
		r.tags = tags
	}
}

func WithRateLimiterOption(o rateLimiterOption) reservedAddressesPoolOption {
	return func(r *ReservedAddressesPool) {
		r.rateLimiterOptions = append(r.rateLimiterOptions, o)
	}
}

func CreateReservedAddressesPool(
	logger hclog.Logger,
	options ...reservedAddressesPoolOption,
) *ReservedAddressesPool {
	result := &ReservedAddressesPool{
		logger: logger.With("domain", "reserved IP address management"),
		mutex:  new(sync.RWMutex),
		// Note: In addition to the standard rate limiting, only 12 reserved IPs may be created per 60 seconds.
		rateLimiterOptions: make([]rateLimiterOption, 0),
	}
	for _, option := range options {
		option(result)
	}
	result.rateLimiter = NewRateLimiter(12, 5*time.Second, true, result.rateLimiterOptions...)
	return result
}

func (r *ReservedAddressesPool) getReservedIPs(
	ctx context.Context,
) (map[IP]*godo.ReservedIP, error) {
	ips, _, err := r.reservedIPs.List(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot enumerate reserved IPs: %w", err)
	}
	reservations := make(map[IP]*godo.ReservedIP)
	for _, ip := range ips {
		reservations[IP(ip.IP)] = &ip
	}
	return reservations, nil
}

func (r *ReservedAddressesPool) getReservedIPV6s(
	ctx context.Context,
) (map[IPv6]*godo.ReservedIPV6, error) {
	ipV6s, _, err := r.reservedIPV6s.List(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot enumerate reserved IPV6s: %w", err)
	}
	reservationsV6 := make(map[IPv6]*godo.ReservedIPV6)
	for _, ip := range ipV6s {
		reservationsV6[IPv6(ip.IP)] = &ip
	}
	return reservationsV6, nil
}

func (r *ReservedAddressesPool) getDropletsWithTag(
	ctx context.Context,
	tag string,
) ([]godo.Droplet, error) {
	result := make([]godo.Droplet, 0, 5)
	opt := &godo.ListOptions{}
	for {
		droplets, resp, err := r.droplets.ListByTag(ctx, tag, opt)
		if err != nil {
			return nil, err
		}

		// append the current page's droplets to our list
		result = append(result, droplets...)

		// if we are at the last page, break out the for loop
		if resp.Links == nil || resp.Links.IsLastPage() {
			break
		}

		page, err := resp.Links.CurrentPage()
		if err != nil {
			return nil, err
		}

		// set the page we want for the next request
		opt.Page = page + 1
	}

	return result, nil
}

// AssignMissingIPv4 will search for any droplets which do not
// yet have associated reserved IPv4 addresses, and if any
// are found, they will be assigned some.
// If there are not enough unassigned reserved IP addresses
// and createIfRequired is false, returns an error.
// It is assumed that only droplets associated with the provided
// tag need to be checked.
func (r *ReservedAddressesPool) AssignMissingIPv4(
	ctx context.Context,
	createIfRequired bool,
	tag string,
) error {
	// ensure only one instance of this function is running at a time
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// work out which droplets currently have IPv4 reservations, and
	// which unassigned reserved addresses we have
	reservedV4s, err := r.getReservedIPs(ctx)
	if err != nil {
		return err
	}
	availableIPv4s := make([]*godo.ReservedIP, 0, 10)
	dropletsWithReservedIPv4s := make(map[int]struct{})
	for _, reserved := range reservedV4s {
		if droplet := reserved.Droplet; droplet == nil {
			availableIPv4s = append(availableIPv4s, reserved)
		} else {
			dropletsWithReservedIPv4s[droplet.ID] = struct{}{}
		}
	}

	// droplets are assigned a special tag when they require
	// (and don't yet have) reserved IP addresses. Find these.
	droplets, err := r.getDropletsWithTag(ctx, tag)
	if err != nil {
		return err
	}

	for _, droplet := range droplets {
		log := r.logger.With("action", "assign", "droplet ID", droplet.ID)

		// if this droplet doesn't already have a reserved IPv4 address, try to assign it one
		if _, exists := dropletsWithReservedIPv4s[droplet.ID]; !exists {
			// there is at least one free IPv4 address, so assign it
			if len(availableIPv4s) > 0 {
				if _, _, err := r.reservedIPActions.Assign(ctx, availableIPv4s[0].IP, droplet.ID); err != nil {
					return fmt.Errorf(
						"cannot assign IPv4 %v to droplet %v: %w",
						availableIPv4s[0],
						droplet.ID,
						err)
				}
				log.Info("assigned reserved IPv4 address", "IPv4 address", availableIPv4s[0].IP)
				availableIPv4s = availableIPv4s[1:]
			} else {
				if createIfRequired {
					r.rateLimiter.Consume(ctx)
					if reservedV4, _, err := r.reservedIPs.Create(ctx, &godo.ReservedIPCreateRequest{DropletID: droplet.ID}); err != nil {
						return fmt.Errorf(
							"cannot create a new IPv4 address for droplet %v: %w",
							droplet.Name,
							err,
						)
					} else {
						log.Info("assigned (new) reserved IP addresses", "IPv4 address", reservedV4.IP)
					}
				} else {
					return fmt.Errorf("insufficient reserved IPv4 addresses")
				}
			}
		}

		// remove the tag from the droplet
		if _, err := r.tags.UntagResources(
			ctx,
			tag,
			&godo.UntagResourcesRequest{
				Resources: []godo.Resource{
					{ID: fmt.Sprintf("%v", droplet.ID), Type: godo.DropletResourceType},
				},
			},
		); err != nil {
			// this is not fatal; it is just inefficient if it can't be removed.
			log.Warn("could not remove the reserved IPv4 address required tag")
		}
	}

	return nil
}

// AssignMissingIPv6 will search for any droplets which do not
// yet have associated reserved IPv6 addresses, and if any
// are found, they will be assigned some.
// If there are not enough unassigned reserved IP addresses
// and createIfRequired is false, returns an error.
// It is assumed that only droplets associated with the provided
// tag need to be checked.
func (r *ReservedAddressesPool) AssignMissingIPv6(
	ctx context.Context,
	createIfRequired bool,
	tag string,
) error {
	// ensure only one instance of this function is running at a time
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// work out which droplets currently have IPv6 reservations, and
	// which unassigned reserved addresses we have
	reservedV6s, err := r.getReservedIPV6s(ctx)
	if err != nil {
		return err
	}
	availableIPv6s := make([]*godo.ReservedIPV6, 0, 10)
	dropletsWithReservedIPv6s := make(map[int]struct{})
	for _, reserved := range reservedV6s {
		if droplet := reserved.Droplet; droplet == nil {
			availableIPv6s = append(availableIPv6s, reserved)
		} else {
			dropletsWithReservedIPv6s[droplet.ID] = struct{}{}
		}
	}

	// droplets are assigned a special tag when they require
	// (and don't yet have) reserved IP addresses. Find these.
	droplets, err := r.getDropletsWithTag(ctx, tag)
	if err != nil {
		return err
	}

	for _, droplet := range droplets {
		log := r.logger.With("action", "assign", "droplet ID", droplet.ID)

		// only try to assign IPv6 static IPs if this droplet has a public IPv6 address
		if _, err := droplet.PublicIPv6(); err == nil {
			// if this droplet doesn't already have a reserved IPv6 address, try to assign it one
			if _, exists := dropletsWithReservedIPv6s[droplet.ID]; !exists {
				// there is at least one free IPv6 address, so assign it
				if len(availableIPv6s) > 0 {
					if _, _, err := r.reservedIPV6Actions.Assign(ctx, availableIPv6s[0].IP, droplet.ID); err != nil {
						return fmt.Errorf(
							"cannot assign IPv6 %v to droplet %v: %w",
							availableIPv6s[0],
							droplet.ID,
							err)
					}
					log.Info("assigned reserved IPv6 address", "IPv6 address", availableIPv6s[0].IP)
					availableIPv6s = availableIPv6s[1:]
				} else {
					if createIfRequired {
						r.rateLimiter.Consume(ctx)
						// DO doesn't yet allow creating and assigning IPv6 as a single operation
						if reservedV6, _, err := r.reservedIPV6s.Create(ctx, &godo.ReservedIPV6CreateRequest{Region: droplet.Region.Name}); err != nil {
							return fmt.Errorf(
								"cannot create a new IPv6 address for droplet %v: %w",
								droplet.Name,
								err,
							)
						} else {
							if _, _, err := r.reservedIPV6Actions.Assign(ctx, reservedV6.IP, droplet.ID); err != nil {
								return fmt.Errorf(
									"cannot assign a IPv6 address to droplet %v: %w",
									droplet.Name,
									err,
								)
							}
							log.Info("assigned (new) reserved IP addresses", "IPv6 address", reservedV6.IP)
						}
					} else {
						return fmt.Errorf("insufficient reserved IPv6 addresses")
					}
				}
			} else {
				r.logger.Info("droplet does not have a public IPv6 address, so not trying to assign a reserved one")
			}
		}

		// remove the tag from the droplet
		if _, err := r.tags.UntagResources(
			ctx,
			tag,
			&godo.UntagResourcesRequest{
				Resources: []godo.Resource{
					{ID: fmt.Sprintf("%v", droplet.ID), Type: godo.DropletResourceType},
				},
			},
		); err != nil {
			// this is not fatal; it is just inefficient if it can't be removed.
			log.Warn("could not remove the reserved IPv6 address required tag")
		}
	}

	return nil
}
