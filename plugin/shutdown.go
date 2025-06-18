package plugin

import (
	"context"
	"fmt"
	"time"

	"github.com/digitalocean/godo"
	"github.com/hashicorp/go-hclog"
)

func shutdownDroplet(
	ctx context.Context,
	dropletId int,
	client *godo.Client,
	log hclog.Logger,
) error {
	// Gracefully power off the droplet.
	log.Debug("Gracefully shutting down droplet...")
	_, _, err := client.DropletActions.PowerOff(ctx, dropletId)
	if err != nil {
		return fmt.Errorf("error shutting down droplet: %s", err)
	}

	ctxWaitForDropletState, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	err = waitForDropletState(ctxWaitForDropletState, "off", dropletId, client, log)
	if err != nil {
		log.Warn("Timeout while waiting to for droplet to become 'off'", "error", err)
	}

	log.Debug("Deleting Droplet...")
	_, err = client.Droplets.Delete(ctx, dropletId)
	if err != nil {
		return fmt.Errorf("error deleting droplet: %s", err)
	}

	return nil
}
