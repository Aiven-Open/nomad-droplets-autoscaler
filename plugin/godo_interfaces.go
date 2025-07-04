package plugin

import (
	"context"
	"iter"

	"github.com/digitalocean/godo"
)

// A subset of the godo API which is available for use by this package.
// These exist to facilitate the mocking of the godo client by tests.

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
	Create(context.Context, *godo.DropletCreateRequest) (*godo.Droplet, *godo.Response, error)
	Get(context.Context, int) (*godo.Droplet, *godo.Response, error)
	Delete(context.Context, int) (*godo.Response, error)
}

type DropletActions interface {
	PowerOff(context.Context, int) (*godo.Action, *godo.Response, error)
}

type Tags interface {
	UntagResources(context.Context, string, *godo.UntagResourcesRequest) (*godo.Response, error)
	TagResources(context.Context, string, *godo.TagResourcesRequest) (*godo.Response, error)
	Create(context.Context, *godo.TagCreateRequest) (*godo.Tag, *godo.Response, error)
	List(context.Context, *godo.ListOptions) ([]godo.Tag, *godo.Response, error)
	Delete(context.Context, string) (*godo.Response, error)
}

func Unpaginate[T any](ctx context.Context, f func(ctx context.Context, opt *godo.ListOptions) ([]T, *godo.Response, error), opt godo.ListOptions) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var buffer T
		for {
			items, resp, err := f(ctx, &opt)
			if err != nil {
				yield(buffer, err)
				return
			}
			for _, item := range items {
				if !yield(item, nil) {
					return
				}
			}
			if resp.Links == nil || resp.Links.IsLastPage() {
				return
			}

			page, err := resp.Links.CurrentPage()
			if err != nil {
				yield(buffer, err)
				return
			}

			// set the page we want for the next request
			opt.Page = page + 1
		}
	}
}

type DigitalOceanWrapper interface {
	ReservedIPs() ReservedIPs
	ReservedIPV6s() ReservedIPV6s
	ReservedIPActions() ReservedIPActions
	ReservedIPV6Actions() ReservedIPV6Actions
	Droplets() Droplets
	DropletActions() DropletActions
	Tags() Tags
}

// GodoWrapper is a simple wrapper around the real godo client, implementing
// the DigitalOceanWrapper interface. This is what will be used outside of testing.

type GodoWrapper struct {
	Client *godo.Client
}

func (g *GodoWrapper) ReservedIPV6s() ReservedIPV6s {
	return g.Client.ReservedIPV6s
}

func (g *GodoWrapper) ReservedIPV6Actions() ReservedIPV6Actions {
	return g.Client.ReservedIPV6Actions
}

func (g *GodoWrapper) ReservedIPs() ReservedIPs {
	return g.Client.ReservedIPs
}

func (g *GodoWrapper) ReservedIPActions() ReservedIPActions {
	return g.Client.ReservedIPActions
}

func (g *GodoWrapper) Droplets() Droplets {
	return g.Client.Droplets
}

func (g *GodoWrapper) DropletActions() DropletActions {
	return g.Client.DropletActions
}

func (g *GodoWrapper) Tags() Tags {
	return g.Client.Tags
}
