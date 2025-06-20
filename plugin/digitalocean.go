package plugin

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/digitalocean/godo"
	"github.com/google/uuid"
	"github.com/hashicorp/nomad/api"
)

const (
	defaultRetryInterval = 10 * time.Second
	defaultRetryLimit    = 15
)

type dropletTemplate struct {
	region     string
	size       string
	vpc        string
	snapshotID int
	name       string
	sshKeys    []string
	tags       []string
	userData   string
	ipv6       bool
}

func (t *TargetPlugin) scaleOut(
	ctx context.Context,
	desired, diff int64,
	template *dropletTemplate,
	config map[string]string,
) error {
	log := t.logger.With("action", "scale_out", "tag", template.name, "count", diff)

	log.Debug("creating DigitalOcean droplets")

	for i := int64(0); i < diff; i++ {
		randomIdentifier := uuid.Must(uuid.NewRandom())
		createRequest := &godo.DropletCreateRequest{
			Name:    template.name + "-" + randomIdentifier.String(),
			Region:  template.region,
			Size:    template.size,
			VPCUUID: template.vpc,
			Image: godo.DropletCreateImage{
				ID: template.snapshotID,
			},
			Tags: template.tags,
			IPv6: template.ipv6,
		}

		if len(template.sshKeys) != 0 {
			createRequest.SSHKeys = sshKeyMap(template.sshKeys)
		}

		if len(template.userData) != 0 {
			content, err := os.ReadFile(template.userData)
			if err != nil {
				return fmt.Errorf("failed to scale out DigitalOcean droplets: %v", err)
			}
			createRequest.UserData = string(content)
		}

		_, _, err := t.client.Droplets.Create(ctx, createRequest)
		if err != nil {
			return fmt.Errorf("failed to scale out DigitalOcean droplets: %v", err)
		}
	}

	log.Debug("successfully created DigitalOcean droplets")

	if err := t.ensureDropletsAreStable(ctx, template, desired); err != nil {
		return fmt.Errorf("failed to confirm scale out DigitalOcean droplets: %v", err)
	}

	log.Debug("scale out DigitalOcean droplets confirmed")

	return nil
}

func (t *TargetPlugin) scaleIn(
	ctx context.Context,
	desired, diff int64,
	template *dropletTemplate,
	config map[string]string,
) error {
	ids, err := t.clusterUtils.RunPreScaleInTasks(ctx, config, int(diff))
	if err != nil {
		return fmt.Errorf("failed to perform pre-scale Nomad scale in tasks: %v", err)
	}

	// Grab the instanceIDs
	instanceIDs := map[string]bool{}

	for _, node := range ids {
		instanceIDs[node.RemoteResourceID] = true
	}

	// Create a logger for this action to pre-populate useful information we
	// would like on all log lines.
	log := t.logger.With("action", "scale_in", "tag", template.name, "instances", ids)

	log.Debug("deleting DigitalOcean droplets")

	if err := t.deleteDroplets(ctx, template.name, instanceIDs); err != nil {
		return fmt.Errorf("failed to delete instances: %v", err)
	}

	log.Debug("successfully started deletion process")

	if err := t.ensureDropletsAreStable(ctx, template, desired); err != nil {
		return fmt.Errorf("failed to confirm scale in DigitalOcean droplets: %v", err)
	}

	log.Debug("scale in DigitalOcean droplets confirmed")

	// Run any post scale in tasks that are desired.
	if err := t.clusterUtils.RunPostScaleInTasks(ctx, config, ids); err != nil {
		return fmt.Errorf("failed to perform post-scale Nomad scale in tasks: %v", err)
	}

	return nil
}

func (t *TargetPlugin) ensureDropletsAreStable(
	ctx context.Context,
	template *dropletTemplate,
	desired int64,
) error {
	f := func(ctx context.Context) (bool, error) {
		_, active, err := t.countDroplets(ctx, template)
		if desired == active || err != nil {
			return true, err
		} else {
			return false, fmt.Errorf("waiting for droplets to become stable")
		}
	}

	return retry(ctx, defaultRetryInterval, defaultRetryLimit, f)
}

func (t *TargetPlugin) deleteDroplets(
	ctx context.Context,
	tag string,
	instanceIDs map[string]bool,
) error {
	// create options. initially, these will be blank
	var dropletsToDelete []int
	opt := &godo.ListOptions{}
	for {
		droplets, resp, err := t.client.Droplets.ListByTag(ctx, tag, opt)
		if err != nil {
			return err
		}

		for _, d := range droplets {
			_, ok := instanceIDs[d.Name]
			if ok {
				go func(dropletId int) {
					log := t.logger.With("action", "delete", "droplet_id", strconv.Itoa(dropletId))
					err := shutdownDroplet(dropletId, t.client, log)
					if err != nil {
						log.Error("error deleting droplet", err)
					}
				}(d.ID)
				dropletsToDelete = append(dropletsToDelete, d.ID)
			}
		}

		// if we deleted all droplets or if we are at the last page, break out the for loop
		if len(dropletsToDelete) == len(instanceIDs) || resp.Links == nil ||
			resp.Links.IsLastPage() {
			break
		}

		page, err := resp.Links.CurrentPage()
		if err != nil {
			return err
		}

		// set the page we want for the next request
		opt.Page = page + 1
	}

	return nil
}

func (t *TargetPlugin) countDroplets(
	ctx context.Context,
	template *dropletTemplate,
) (int64, int64, error) {
	var total int64 = 0
	var ready int64 = 0

	opt := &godo.ListOptions{}
	for {
		droplets, resp, err := t.client.Droplets.ListByTag(ctx, template.name, opt)
		if err != nil {
			return 0, 0, err
		}

		total = total + int64(len(droplets))
		ready = ready + count(droplets, isReady)

		if resp.Links == nil || resp.Links.IsLastPage() {
			break
		}

		page, err := resp.Links.CurrentPage()
		if err != nil {
			return 0, 0, err
		}

		opt.Page = page + 1
	}

	return total, ready, nil
}

func count(droplets []godo.Droplet, predicate func(godo.Droplet) bool) int64 {
	var count int64 = 0
	for _, d := range droplets {
		if predicate(d) {
			count += 1
		}
	}
	return count
}

func isReady(droplet godo.Droplet) bool {
	return droplet.Status == "active"
}

// doDropletNodeIDMap is used to identify the DigitalOcean Droplet ID of a Nomad node using
// the relevant attribute value.
func doDropletNodeIDMap(n *api.Node) (string, error) {
	val, ok := n.Attributes["unique.hostname"]
	if !ok || val == "" {
		return "", fmt.Errorf("attribute %q not found", "unique.hostname")
	}
	return val, nil
}

func sshKeyMap(input []string) []godo.DropletCreateSSHKey {
	var result []godo.DropletCreateSSHKey

	for _, v := range input {
		result = append(result, godo.DropletCreateSSHKey{Fingerprint: v})
	}

	return result
}
