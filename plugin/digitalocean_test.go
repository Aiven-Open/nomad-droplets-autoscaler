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

    TAGS_TEMPFILE=@mktemp@
    for I in @seq 1 60@ ; do
        if curl -o "$TAGS_TEMPFILE" http://169.254.169.254/metadata/v1/tags ; then
            if [ -f "$TAGS_TEMPFILE" ] ; then
                sed -n 's#banana-##p' < "$TAGS_TEMPFILE" > "/run/secure-introduction"
                if [ @wc -l < "/run/secure-introduction"@ -eq 1 ] ; then
                    rm "$TAGS_TEMPFILE"
                    exit 0
                fi
            fi
        fi
        sleep 1
    done
    exit 1
`, "@", "`"), mock.dropletUserData[1])
	// "abcd" is the mock request-wrapped SecretID; "banana-" is the configured prefix
	require.Contains(t, mock.droplets[1].Tags, "banana-abcd")
}
