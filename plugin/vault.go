package plugin

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"time"

	"github.com/hashicorp/vault-client-go"
	"github.com/hashicorp/vault-client-go/schema"
)

type VaultProxy interface {
	// GenerateSecretId creates a new vault secretID for the approle which can only be accessed from the specified IP addresses.
	// Returns the wrapping token to be used to retrieve the SecretID
	GenerateSecretId(
		ctx context.Context,
		appRole string,
		allowedIPv4, allowedIPv6 string,
		secretValidity, wrapperValidity time.Duration,
	) (string, error)
}

type vaultProxy struct {
	client *vault.Client
}

func NewVault() (*vaultProxy, error) {
	client, err := vault.New(vault.WithEnvironment())
	if err != nil {
		return nil, err
	}
	return &vaultProxy{client: client}, nil
}

func (v *vaultProxy) GenerateSecretId(
	ctx context.Context,
	appRole string,
	allowedIPv4, allowedIPv6 string,
	secretValidity, wrapperValidity time.Duration,
) (string, error) {
	if allowedIPv4 == "" && allowedIPv6 == "" {
		return "", fmt.Errorf("at least one authorised IP address must be provided")
	}
	cidrs := make([]string, 0, 2)
	if allowedIPv4 != "" {
		cidrs = append(
			cidrs,
			(&net.IPNet{
				IP:   net.ParseIP(allowedIPv4),
				Mask: net.CIDRMask(32, 32),
			}).String(),
		)
	}
	if allowedIPv6 != "" {
		cidrs = append(
			cidrs,
			(&net.IPNet{
				IP:   net.ParseIP(allowedIPv6),
				Mask: net.CIDRMask(128, 128),
			}).String(),
		)
	}
	// temporarily include this to allow exercising this codepath
	// even when vault is not available
	if appRole == "mock" {
		prohibitedCharactersInTags := regexp.MustCompile(`[^a-zA-Z0-9_\-\:]+`)
		return prohibitedCharactersInTags.ReplaceAllLiteralString(fmt.Sprintf("mock-wrapped-token-for-%v-and-%v", allowedIPv4, allowedIPv6), "_"), nil
	}
	resp, err := v.client.Auth.AppRoleWriteSecretId(
		ctx,
		appRole,
		schema.AppRoleWriteSecretIdRequest{
			CidrList:        cidrs,
			NumUses:         1,
			TokenBoundCidrs: cidrs,
			Ttl:             fmt.Sprintf("%.f", secretValidity.Seconds()),
		},
		vault.WithResponseWrapping(wrapperValidity),
	)
	if err != nil {
		return "", fmt.Errorf("unable to write a secret with bound CIDRs (%q): %w", cidrs, err)
	}
	wrapped := resp.WrapInfo.Token
	return wrapped, nil
}
