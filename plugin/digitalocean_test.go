package plugin

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
)

func TestScaleOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second*5)
	defer cancel()
	mock := createMockGodo()
	config := map[string]string{
		"name":        "mydropletname",
		"region":      "lon1",
		"size":        "s1",
		"snapshot_id": "12345",
		"token":       "t0ken",
		"vpc_uuid":    uuid.New().String(),
		"tags":        "foo,bar,baz",
	}
	tp := &TargetPlugin{
		ctx:    ctx,
		config: config,
		logger: hclog.NewNullLogger(),
		client: mock,
		vault:  nil,
	}
	template := Must(tp.createDropletTemplate(config))
	err := tp.scaleOut(ctx, 3, 3, template, config)
	require.NoError(t, err)
	require.Len(t, mock.dropletUserData, 3)
}

func TestScaleOutWithSecureIntroductionInTag(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second*5)
	defer cancel()
	mock := createMockGodo()
	config := map[string]string{
		"name":                           "mydropletname",
		"region":                         "lon1",
		"size":                           "s1",
		"snapshot_id":                    "12345",
		"token":                          "t0ken",
		"vpc_uuid":                       uuid.New().String(),
		"tags":                           "foo,bar,baz",
		"secure_introduction_approle":    "droplet-approle",
		"secure_introduction_tag_prefix": "banana-",
	}
	tp := &TargetPlugin{
		ctx:    ctx,
		config: config,
		logger: hclog.Default(),
		client: mock,
		vault:  &mockVaultProxy{},
	}
	template := Must(tp.createDropletTemplate(config))
	err := tp.scaleOut(ctx, 3, 3, template, config)
	require.NoError(t, err)
	require.Len(t, mock.dropletUserData, 3)
	require.Equal(t, strings.ReplaceAll(`#cloud-config-archive
- type: text/x-shellscript
  content: |
    #!/bin/sh

    TAGS_TEMPFILE=@mktemp -s@
    for I in @seq 1 20@ ; do
        curl -o "$TAGS_TEMPFILE" http://169.254.169.254/metadata/v1/tags && break
        sleep 3
    done
    if [ -f "$TAGS_TEMPFILE" ] ; then
        sed -n 's#banana-##p' < "$TAGS_TEMPFILE" > "/run/secure-introduction"
    fi
    rm "$TAGS_TEMPFILE"
`, "@", "`"), mock.dropletUserData[1])
	// "abcd" is the mock request-wrapped SecretID; "banana-" is the configured prefix
	require.Contains(t, mock.droplets[1].Tags, "banana-abcd")
}
