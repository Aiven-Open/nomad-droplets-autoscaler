package plugin

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/go-hclog"
)

func shutdownDroplet(
	ctx context.Context,
	dropletId int,
	droplets Droplets,
	dropletActions DropletActions,
	log hclog.Logger,
) error {
	// Gracefully power off the droplet.
	log.Debug("Gracefully shutting down droplet...")
	_, _, err := dropletActions.PowerOff(ctx, dropletId)
	if err != nil {
		return fmt.Errorf("error shutting down droplet: %s", err)
	}

	ctxWaitForDropletState, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	err = waitForDropletState(ctxWaitForDropletState, "off", dropletId, droplets, log)
	if err != nil {
		log.Warn("Timeout while waiting to for droplet to become 'off'", "error", err)
	}

	log.Debug("Deleting Droplet...")
	_, err = droplets.Delete(ctx, dropletId)
	if err != nil {
		return fmt.Errorf("error deleting droplet: %s", err)
	}

	return nil
}
