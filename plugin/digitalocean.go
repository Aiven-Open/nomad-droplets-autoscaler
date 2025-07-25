package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/digitalocean/godo"
	"github.com/google/uuid"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/api"
)

const (
	defaultRetryInterval = 10 * time.Second
	defaultRetryLimit    = 15
)

type dropletTemplate struct {
	createReservedAddresses     bool
	ipv6                        bool
	name                        string
	region                      string
	reserveIPv4Addresses        bool
	reserveIPv6Addresses        bool
	secureIntroductionAppRole   string
	secureIntroductionTagPrefix string
	secretValidity              time.Duration
	wrappedSecretValidity       time.Duration
	secureIntroductionFilename  string
	size                        string
	snapshotID                  int
	sshKeys                     []string
	tags                        []string
	userData                    string
	vpc                         string
}

func (t *TargetPlugin) scaleOut(
	ctx context.Context,
	desired, diff int64,
	template *dropletTemplate,
	config map[string]string,
) error {
	log := t.logger.With("action", "scale_out")

	log.Debug("creating DigitalOcean droplets", "template", fmt.Sprintf("%+v", template))

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	wg := &sync.WaitGroup{}
	var prereservedIPV4s []string
	var prereservedIPV6s []string
	var err error
	if template.reserveIPv4Addresses {
		prereservedIPV4s, err = t.reservedAddressesPool.PrereserveIPs(
			ctx,
			int(diff),
			template.region,
			template.createReservedAddresses,
			5*time.Minute,
		)
		if err != nil {
			return fmt.Errorf("cannot pre-reserve %v IPv4 addresses: %w", diff, err)
		}
	}
	if template.reserveIPv6Addresses {
		prereservedIPV6s, err = t.reservedAddressesPool.PrereserveIPV6s(
			ctx,
			int(diff),
			template.region,
			template.createReservedAddresses,
			5*time.Minute,
		)
		if err != nil {
			return fmt.Errorf("cannot pre-reserve %v IPv6 addresses: %w", diff, err)
		}
	}
	errorChannel := make(chan error)

	for i := int64(0); i < diff; i++ {
		wg.Add(1)
		// create each droplet concurrently. If there is a problem,
		// return the error via the channel.
		go func(i int) {
			err := (func() error {
				defer wg.Done()
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
					if err == nil {
						// file was found at this location, so use its content
						createRequest.UserData = string(content)
					} else {
						// assume the string contains the user data
						createRequest.UserData = template.userData
					}
				}

				if template.secureIntroductionAppRole != "" &&
					template.secureIntroductionFilename != "" {
					var allowedIPv4 string
					var allowedIPv6 string
					if template.reserveIPv4Addresses {
						allowedIPv4 = prereservedIPV4s[i]
					}
					if template.reserveIPv4Addresses {
						allowedIPv6 = prereservedIPV6s[i]
					}

					createRequest.UserData, err = generateUserDataForSecureIntroduction(
						ctx,
						log.With("droplet scale-out index", i),
						createRequest.UserData,
						allowedIPv4,
						allowedIPv6,
						template,
						t.vault,
					)
					if err != nil {
						return err
					}
				}

				droplet, _, err := t.client.Droplets().Create(ctx, createRequest)
				if err != nil {
					return fmt.Errorf("failed to scale out DigitalOcean droplets: %w", err)
				}
				log := log.With("droplet ID", strconv.Itoa(droplet.ID))
				log.Info("Created droplet")
				if template.reserveIPv4Addresses {
					if err := t.reservedAddressesPool.AssignIPv4(ctx, droplet.ID, prereservedIPV4s[i]); err != nil {
						return fmt.Errorf(
							"failed to assign static IPv4 to droplet %v: %w",
							droplet.ID,
							err,
						)
					}
				}
				if template.reserveIPv6Addresses {
					if err := t.reservedAddressesPool.AssignIPv6(ctx, droplet.ID, prereservedIPV6s[i]); err != nil {
						return fmt.Errorf(
							"failed to assign static IPv6 to droplet %v: %w",
							droplet.ID,
							err,
						)
					}
				}

				if template.secureIntroductionAppRole != "" &&
					template.secureIntroductionTagPrefix != "" {
					if err := generateTagForSecureIntroduction(ctx, log, template, droplet.ID, template.ipv6, t.vault, t.client.Droplets(), t.client.Tags()); err != nil {
						return err
					}
				}
				return nil
			})()
			if err != nil {
				log.Error("failed to create droplet",
					"scale-out index", i,
					"error", err)
				errorChannel <- err
			}
		}(int(i))
	}
	go func() {
		wg.Wait()
		close(errorChannel)
	}()
	errorList := make([]error, 0)
	for err := range errorChannel {
		errorList = append(errorList, err)
	}
	if len(errorList) > 0 {
		return errors.Join(errorList...)
	}

	log.Debug("successfully created DigitalOcean droplets")

	if err := t.ensureDropletsAreStable(ctx, template, desired); err != nil {
		return fmt.Errorf("failed to confirm scale out DigitalOcean droplets: %w", err)
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
		return fmt.Errorf("failed to perform pre-scale Nomad scale in tasks: %w", err)
	}

	// Grab the instanceIDs
	instanceIDs := make(map[string]struct{})

	for _, node := range ids {
		instanceIDs[node.RemoteResourceID] = struct{}{}
	}

	// Create a logger for this action to pre-populate useful information we
	// would like on all log lines.
	log := t.logger.With("action", "scale_in", "tag", template.name, "instances", ids)

	log.Debug("deleting DigitalOcean droplets")

	if err := t.deleteDroplets(ctx, template.name, instanceIDs); err != nil {
		return fmt.Errorf("failed to delete instances: %w", err)
	}

	log.Debug("successfully started deletion process")

	if err := t.ensureDropletsAreStable(ctx, template, desired); err != nil {
		return fmt.Errorf("failed to confirm scale in DigitalOcean droplets: %w", err)
	}

	log.Debug("scale in DigitalOcean droplets confirmed")

	// Run any post scale in tasks that are desired.
	if err := t.clusterUtils.RunPostScaleInTasks(ctx, config, ids); err != nil {
		return fmt.Errorf("failed to perform post-scale Nomad scale in tasks: %w", err)
	}

	if tagPrefix := template.secureIntroductionTagPrefix; tagPrefix != "" {
		go cleanUpUnusedTags(ctx, log, t.client, template.secureIntroductionTagPrefix)
	}

	return nil
}

// cleanUpUnusedTags will delete unused tags starting with the provided prefix.
func cleanUpUnusedTags(ctx context.Context, logger hclog.Logger, client DigitalOceanWrapper, tagPrefix string) {
	// record all known tags
	initialTags := make([]string, 0, 100)
	for tag, err := range Unpaginate(ctx, client.Tags().List, godo.ListOptions{}) {
		if err != nil {
			logger.Error("cannot retrieve tags", "error", err)
			return
		}
		if !strings.HasPrefix(tag.Name, tagPrefix) {
			continue
		}
		initialTags = append(initialTags, tag.Name)
	}

	// wait a minute. This avoids any race conditions where a tag was created
	// but at the time had not yet been assigned to a droplet.
	if err := Sleep(ctx, time.Minute); err != nil {
		return
	}

	for tag, err := range Unpaginate(ctx, client.Tags().List, godo.ListOptions{}) {
		if err != nil {
			logger.Error("cannot retrieve tags", "error", err)
			return
		}
		if !strings.HasPrefix(tag.Name, tagPrefix) {
			continue
		}
		if res := tag.Resources; res != nil && res.Count > 0 {
			logger.Info("not cleaning up tag as it's still in use", "tag name", tag.Name)
			continue
		}
		if !slices.Contains(initialTags, tag.Name) {
			logger.Info("not cleaning up tag as it was created very recently", "tag name", tag.Name)
			continue
		}
		logger.Debug("cleaning up tag as it's unused", "tag name", tag.Name)
		if _, err := client.Tags().Delete(ctx, tag.Name); err != nil {
			logger.Error("cannot delete the tag", "tag name", tag.Name, "error", err)
		}
	}
}

func (t *TargetPlugin) ensureDropletsAreStable(
	ctx context.Context,
	template *dropletTemplate,
	desired int64,
) error {
	return retry(
		ctx,
		t.logger,
		defaultRetryInterval,
		defaultRetryLimit,
		func(ctx context.Context, cancel context.CancelCauseFunc) error {
			_, active, err := t.countDroplets(ctx, template)
			if desired == active {
				return nil
			}
			if err != nil {
				cancel(err)
				return err
			} else {
				return errors.New("waiting for droplets to become stable")
			}
		},
	)
}

func (t *TargetPlugin) deleteDroplets(
	ctx context.Context,
	tag string,
	instanceIDs map[string]struct{},
) error {
	// create options. initially, these will be blank
	var dropletsToDelete []int
	opt := &godo.ListOptions{}
	for {
		droplets, resp, err := t.client.Droplets().ListByTag(ctx, tag, opt)
		if err != nil {
			return err
		}

		wg := &sync.WaitGroup{}
		for _, d := range droplets {
			_, ok := instanceIDs[d.Name]
			if ok {
				wg.Add(1)
				go func(dropletId int) {
					defer wg.Done()
					log := t.logger.With("action", "delete", "droplet_id", strconv.Itoa(dropletId))
					err := shutdownDroplet(
						ctx,
						dropletId,
						t.client.Droplets(),
						t.client.DropletActions(),
						log,
					)
					if err != nil {
						log.Error("error deleting droplet", err)
					}
				}(d.ID)
				dropletsToDelete = append(dropletsToDelete, d.ID)
			}
		}
		wg.Wait()

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
		droplets, resp, err := t.client.Droplets().ListByTag(ctx, template.name, opt)
		if err != nil {
			return 0, 0, err
		}

		total = total + int64(len(droplets))
		ready = ready + countIf(droplets, isReady)

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

func generateUserDataForSecureIntroduction(
	ctx context.Context,
	logger hclog.Logger,
	userData string,
	allowedIPv4, allowedIPv6 string,
	template *dropletTemplate,
	vault VaultProxy,
) (string, error) {
	if allowedIPv4 != "" || allowedIPv6 != "" {
		// because at least one reserved IP address is being used,
		// it is possible to generate the wrapped secret before
		// the droplet is created, allowing it to be included in
		// the user-data
		wrappedSecretId, err := vault.GenerateSecretId(
			ctx,
			template.secureIntroductionAppRole,
			allowedIPv4, allowedIPv6,
			template.secretValidity, template.wrappedSecretValidity,
		)
		if err != nil {
			return "", fmt.Errorf("failed to generate wrapped secure introduction: %w", err)
		}
		shellScript := fmt.Sprintf(
			`#!/bin/sh
echo "%v" > "%v"
`,
			wrappedSecretId,
			template.secureIntroductionFilename,
		)
		result, err := PrependShellScriptToUserData(
			userData,
			shellScript,
		)
		if err == nil {
			return result, nil
		} else {
			return "", fmt.Errorf(
				"failed to insert wrapped secure introduction into user-data: %w",
				err)
		}
	} else {
		if prefix := template.secureIntroductionTagPrefix; prefix != "" {
			/*
			   It is unlikely that the user-data script will be executed before
			   the droplet's metadata has been updated with the tags containing
			   the request-wrapped SecretID - but to be sure, allow a minute of
			   retries before failing.
			*/
			shellScript := fmt.Sprintf(strings.ReplaceAll(
				`#!/bin/sh

TAGS_TEMPFILE=@mktemp@
for I in @seq 1 60@ ; do
    if curl -o "$TAGS_TEMPFILE" http://169.254.169.254/metadata/v1/tags ; then
        if [ -f "$TAGS_TEMPFILE" ] ; then
            sed -n 's#%v##p' < "$TAGS_TEMPFILE" > "%v"
            if [ @wc -l < "%v"@ -eq 1 ] ; then
                rm "$TAGS_TEMPFILE"
                exit 0
            fi
        fi
    fi
    sleep 1
done
exit 1
`, "@", "`"),
				prefix,
				template.secureIntroductionFilename,
				template.secureIntroductionFilename,
			)
			result, err := PrependShellScriptToUserData(
				userData,
				shellScript,
			)
			if err == nil {
				return result, nil
			} else {
				return "", fmt.Errorf(
					"failed to insert SecretID retrieval script into user-data: %w",
					err)
			}
		}
	}
	// no modifications
	return userData, nil
}

func generateTagForSecureIntroduction(
	ctx context.Context,
	logger hclog.Logger,
	template *dropletTemplate,
	dropletID int,
	ipv6Enabled bool,
	vault VaultProxy,
	droplets Droplets,
	tags Tags,
) error {
	var ipv6, ipv4 string

	// when a droplet is created, DO does not include any network information
	// in the response; a polling loop is required to wait for it to become available
	if err := retry(
		ctx,
		logger,
		6*time.Second,
		10,
		func(ctx context.Context, cancel context.CancelCauseFunc) error {
			droplet, _, err := droplets.Get(ctx, dropletID)
			if err != nil {
				return fmt.Errorf("cannot retrieve droplet metadata: %w", err)
			}
			if droplet.Networks == nil || len(droplet.Networks.V4) == 0 {
				return errors.New("no IPv4 network information is yet available")
			}
			ipv4 = droplet.Networks.V4[0].IPAddress
			if ipv6Enabled {
				if len(droplet.Networks.V6) == 0 {
					return errors.New("no IPv6 network information is yet available")
				}
				ipv6 = droplet.Networks.V6[0].IPAddress
			}
			return nil
		}); err != nil {
		return fmt.Errorf("could not get the droplet's IP address(es): %w", err)
	}
	logger.Info("IP addresses have been assigned", "ipv4", ipv4, "ipv6", ipv6)
	wrappedSecretId, err := vault.GenerateSecretId(
		ctx,
		template.secureIntroductionAppRole,
		ipv4, ipv6,
		template.secretValidity, template.wrappedSecretValidity,
	)
	if err != nil {
		return fmt.Errorf(
			"failed to generate wrapped secure introduction for droplet %v: %w",
			dropletID,
			err)
	}
	tagWithSecretID := fmt.Sprintf("%v%v", template.secureIntroductionTagPrefix, wrappedSecretId)
	if _, _, err = tags.Create(ctx, &godo.TagCreateRequest{Name: tagWithSecretID}); err != nil {
		return fmt.Errorf("could not create a new tag: %w", err)
	}
	// There are often conflicts if trying to set tags on a resource while another operation
	// is in progress, so this must also be retried if a 422 response is seen
	if err := RetryOnTransientError(ctx, logger, func(ctx context.Context, cancel context.CancelCauseFunc) error {
		_, err := tags.TagResources(ctx, tagWithSecretID, &godo.TagResourcesRequest{Resources: []godo.Resource{{ID: fmt.Sprintf("%v", dropletID), Type: "droplet"}}})
		return err
	}, 404); err != nil {
		return fmt.Errorf(
			"failed to tag droplet %v with wrapped secure introduction: %w",
			dropletID,
			err)
	}
	logger.Debug("Secure introduction tag has been added")
	return nil
}
