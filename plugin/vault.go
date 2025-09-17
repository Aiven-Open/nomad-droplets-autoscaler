package plugin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/vault-client-go"
	"github.com/hashicorp/vault-client-go/schema"
)

type (
	VaultAddressGetter func() ([]byte, error)
	VaultTokenGetter   func() ([]byte, error)
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

	GetAddress() string
	GetRoleId(ctx context.Context, appRole string) (string, error)
}

type vaultProxy struct {
	mutex         *sync.Mutex
	client        *vault.Client
	logger        hclog.Logger
	address       []byte
	addressGetter VaultAddressGetter
	tokenGetter   VaultTokenGetter
	roleIdMap     map[string]string
}

func (v *vaultProxy) GetAddress() string {
	return string(v.address)
}

func defaultAddressGetter() ([]byte, error) {
	return os.ReadFile("/etc/autoscaler-secrets/vault_addr")
}

func defaultTokenGetter() ([]byte, error) {
	return os.ReadFile("/etc/autoscaler-secrets/vault_token")
}

func (v *vaultProxy) refreshToken() error {
	if v.client == nil {
		return errors.New("no vault client to refresh the token for")
	}
	token, err := v.tokenGetter()
	if err != nil {
		return fmt.Errorf("cannot read vault token from autoscaler secrets: %w", err)
	}
	if err := v.client.SetToken(string(token)); err != nil {
		return fmt.Errorf("cannot set vault token: %w", err)
	}
	return nil
}

type vaultOption func(context.Context, *vaultProxy) error

func WithStaticToken(token []byte) vaultOption {
	return func(ctx context.Context, v *vaultProxy) error {
		v.tokenGetter = func() ([]byte, error) {
			return token, nil
		}
		return nil
	}
}

func WithStaticAddress(address []byte) vaultOption {
	return func(ctx context.Context, v *vaultProxy) error {
		v.addressGetter = func() ([]byte, error) {
			return address, nil
		}
		return nil
	}
}

func NewVault(ctx context.Context, logger hclog.Logger, options ...vaultOption) (*vaultProxy, error) {
	result := &vaultProxy{logger: logger, mutex: new(sync.Mutex), addressGetter: defaultAddressGetter, tokenGetter: defaultTokenGetter, roleIdMap: make(map[string]string)}

	for _, option := range options {
		if err := option(ctx, result); err != nil {
			return nil, err
		}
	}
	var err error
	result.address, err = result.addressGetter()
	if err != nil {
		return nil, fmt.Errorf("no vault address available: %w", err)
	}
	client, err := vault.New(vault.WithAddress(string(result.address)),
		vault.WithRequestTimeout(10*time.Second))
	if err != nil {
		return nil, fmt.Errorf("could not create a vault client for address %v: %w", string(result.address), err)
	}
	result.client = client
	if err := result.refreshToken(); err != nil {
		return nil, err
	}
	self, err := client.Auth.TokenLookUpSelf(ctx)
	if err == nil && self.Data != nil {
		logger.Info("created vault client", "accessor", self.Data["accessor"], "policies", self.Data["policies"], "ttl", self.Data["ttl"])
	}
	return result, nil
}

func (v *vaultProxy) GetRoleId(ctx context.Context, appRole string) (string, error) {
	if value, exists := v.roleIdMap[appRole]; exists {
		return value, nil
	}
	v.mutex.Lock()
	defer v.mutex.Unlock()
	if err := v.refreshToken(); err != nil {
		return "", fmt.Errorf("cannot refresh the vault token: %w", err)
	}
	readRoleIdResponse, err := v.client.Auth.AppRoleReadRoleId(ctx, appRole)
	if err != nil {
		return "", fmt.Errorf("could not retrieve app role ID for %q: %w", appRole, err)
	}
	v.roleIdMap[appRole] = readRoleIdResponse.Data.RoleId
	return readRoleIdResponse.Data.RoleId, nil
}

// GenerateSecretId returns the roleID and wrapped secretID
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
	// include this to allow exercising this codepath
	// even when vault is not available
	if appRole == "mock" {
		prohibitedCharactersInTags := regexp.MustCompile(`[^a-zA-Z0-9_\-\:]+`)
		return prohibitedCharactersInTags.ReplaceAllLiteralString(fmt.Sprintf("mock-wrapped-token-for-%v-and-%v", allowedIPv4, allowedIPv6), "_"), nil
	}
	v.mutex.Lock()
	defer v.mutex.Unlock()
	if err := v.refreshToken(); err != nil {
		return "", fmt.Errorf("cannot refresh the vault token: %w", err)
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
		return "", fmt.Errorf("unable to write a secret with bound CIDRs (%q) for approle %q: %w", cidrs, appRole, err)
	}
	wrapped := resp.WrapInfo.Token
	return wrapped, nil
}
