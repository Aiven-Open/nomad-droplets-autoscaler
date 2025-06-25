package plugin

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/go-hclog"
)

func waitForDropletState(
	ctx context.Context,
	desiredState string, dropletId int,
	droplets Droplets,
	log hclog.Logger,
) error {
	attempts := 0
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	log.Debug(
		fmt.Sprintf(
			"Waiting for droplet to become %s",
			desiredState,
		),
	)
	for {
		attempts += 1

		log.Debug(fmt.Sprintf("Checking droplet status... (attempt: %d)", attempts))
		droplet, _, err := droplets.Get(ctx, dropletId)
		if err != nil {
			return err
		}

		if droplet.Status == desiredState {
			return nil
		}

		// Wait 3 seconds
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			break
		}
	}
}
