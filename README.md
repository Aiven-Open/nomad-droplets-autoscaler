# Nomad DigitalOcean Droplets Autoscaler

The `do-droplets` target plugin allows for the scaling of the Nomad cluster clients via creating and
destroying [DigitalOcean Droplets](https://www.digitalocean.com/products/droplets/).

## Requirements

- nomad autoscaler 0.3.0+
- DigitalOcean account

## Documentation

### Agent Configuration Options

To use the `do-droplets` target plugin, the agent configuration needs to be populated with the appropriate target block.
Currently, Personal Access Token (PAT) is the only method of authenticating with the API. You can manage your tokens at the DigitalOcean Control Panel [Applications Page](https://cloud.digitalocean.com/settings/applications).

```
target "do-droplets" {
  driver = "do-droplets"
  config = {
    token = "local/token"
  }
}
```

- `token` `(string: "")` - a DigitalOcean API token or a path to a file containing a token. Alternatively, this can also be specified using environment variables ordered by precedence:
  - `DIGITALOCEAN_TOKEN`
  - `DIGITALOCEAN_ACCESS_TOKEN`

### Policy Configuration Options

```hcl
check "hashistack-allocated-cpu" {
  # ...
  target "do-droplets" {
    create_reserved_addresses            = "true"
    ipv6                                 = "true"
    name                                 = "hashi-worker"
    node_class                           = "hashistack"
    node_drain_deadline                  = "5m"
    node_purge                           = "true"
    region                               = "nyc1"
    reserve_ipv4_addresses               = "false"
    reserve_ipv6_addresses               = "false"
    secure_introduction_approle          = "autoscaler-droplet"
    secure_introduction_secret_validity  = "5m"
    secure_introduction_tag_prefix       = "secure-introduction: "
    size                                 = "s-1vcpu-1gb"
    snapshot_id                          = 84589509
    tags                                 = "hashi-stack"
    user_data                            = "local/hashi-worker-user-data.sh"
  }
  # ...
}
```

- `name` `(string: <required>)` - A logical name of a Droplet "group". Every managed Droplet will be tagged with this value and its name is this value with a random suffix

- `region` `(string: <required>)` - The region to start in.

- `vpc_uuid` `(string: <required>)` - The ID of the VPC where the Droplet will be located.

- `size` `(string: <required>)` - The unique slug that indentifies the type of Droplet. You can find a list of available slugs on [DigitalOcean API documentation](https://developers.digitalocean.com/documentation/v2/#list-all-sizes).

- `snapshot_id` `(string: <required>)` - The Droplet image ID.

- `user_data` `(string: "")` - A string of the desired User Data for the Droplet or a path to a file containing the User Data

- `ssh_keys` `(string: "")` - A comma-separated list of SSH fingerprints to enable

- `tags` `(string: "")` - A comma-separated list of additional tags to be applied to the Droplets.

- `datacenter` `(string: "")` - The Nomad client [datacenter](https://www.nomadproject.io/docs/configuration#datacenter)
  identifier used to group nodes into a pool of resource. Conflicts with
  `node_class`.

- `node_class` `(string: "")` - The Nomad [client node class](https://www.nomadproject.io/docs/configuration/client#node_class)
  identifier used to group nodes into a pool of resource. Conflicts with
  `datacenter`.

- `node_drain_deadline` `(duration: "15m")` The Nomad [drain deadline](https://www.nomadproject.io/api-docs/nodes#deadline) to use when performing node draining
  actions. **Note that the default value for this setting differs from Nomad's
  default of 1h.**

- `node_drain_ignore_system_jobs` `(bool: "false")` A boolean flag used to
  control if system jobs should be stopped when performing node draining
  actions.

- `node_purge` `(bool: "false")` A boolean flag to determine whether Nomad
  clients should be [purged](https://www.nomadproject.io/api-docs/nodes#purge-node) when performing scale in
  actions.

- `ipv6` `(bool: "false")` A boolean flag to determine whether droplets should have IPv6 enabled.

- `create_reserved_addresses` `(bool: "false")` A boolean flag to determine whether reserved IP addresses should be automatically created when required.

- `reserve_ipv4_addresses` `(bool: "false")` A boolean flag to determine whether reserved IP addresses should be used for IPv4 interfaces

- `reserve_ipv6_addresses` `(bool: "false")` A boolean flag to determine whether reserved IP addresses should be used for IPv6 interfaces

- `secure_introduction_approle` `(string: "")` A vault AppRole. If defined, a secret will be generated for this role for each new droplet.
  If IPv4 and/or IPv6 reserved addresses are being used, a wrapped SecretID will be included in `user_data`.

- `secure_introduction_tag_prefix` `(string: "")` If defined (and `secure_introduction_approle` is also defined), a request-wrapped SecretID will be stored in a tag prefixed with this string

- `secure_introduction_secret_validity` `(duration: 5m)` The duration a SecretID (and its request-wrapper) is valid for, from the time it is generated.

- `secure_introduction_filename` `(string: "/run/secure-introduction")` The filename to store the unwrapped SecretID in

- `node_selector_strategy` `(string: "least_busy")` The strategy to use when
  selecting nodes for termination. Refer to the [node selector
  strategy](https://www.nomadproject.io/docs/autoscaling/internals/node-selector-strategy) documentation for more information.

### Secure Introduction

While it is possible to provide secrets via a droplet's user-data, this is not always considered sufficiently secure. Additionally, this
means every droplet is allocated the same set of secrets, making it more difficult to track the lineage of use of a secret.

To improve the situation, it's possible to use Hashicorp Vault to generate a "personalised" SecretID for a given approle. Each new droplet
receives its own SecretID, allowing better repudiation and tracking options. Going one step further, this SecretID is "request-wrapped"
before being provided to each droplet. This wrapper can be unwrapped by the Vault service only one time. Additionally, unwrapping can only
be requested from IP address(es) associated with the droplet, and only within a few minutes of its being issued.

If a `secure_introduction_approle` is provided, this feature is enabled. It is assumed that the autoscaler has both `VAULT_ADDR` and `VAULT_TOKEN`
in its environment, as the vault client will rely on these to find and authenticate with the Vault service.

If reserved IPv4/IPv6 addresses are being assigned to droplets, it is possible to anticipate the exact address(es) which will be assigned, and the
request-wrapping can be performed prior to droplet creation, allowing the wrapped SecretID to be inserted directly into the droplet's user data.
Otherwise, IP address(es) of a droplet are not known until after it is created, so the request-wrapped SecretID is unable to be included directly. Instead, it is appended to a supplied prefix and included as a tag after the droplet is created.

Whether or not reserved IP addresses are used, the modified user-data will ensure that the request-wrapped SecretID is written to a (configurable) location on the droplet. It is assumed that subsequent cloud-init stages will install the vault client, perform the unwrapping, and retrieve whatever credentials are required.
