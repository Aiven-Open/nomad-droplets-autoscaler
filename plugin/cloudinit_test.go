package plugin_test

import (
	"strings"
	"testing"

	"github.com/Aiven-Open/nomad-droplets-autoscaler/plugin"
	"github.com/stretchr/testify/require"
)

const ShellScript = `#!/bin/bash
echo "Hello, world"`

func TestEmpty(t *testing.T) {
	result, err := plugin.PrependShellScriptToUserData("   ", ShellScript)
	require.NoError(t, err)
	require.Equal(t, ShellScript, result)
}

func TestCloudConfigArchive(t *testing.T) {
	result, err := plugin.PrependShellScriptToUserData(`#cloud-config-archive
- type: "text/cloud-boothook"
  content: |
    #!/bin/sh
    echo "this is from a boothook." > /var/tmp/boothook.txt
- type: "text/cloud-config"
  content: |
    bootcmd:
    - echo "this is from a cloud-config." > /var/tmp/bootcmd.txt
`, ShellScript)
	require.NoError(t, err)
	require.Equal(t, `#cloud-config-archive
- type: "text/cloud-boothook"
  content: |
    #!/bin/bash
    echo "Hello, world"
- type: "text/cloud-boothook"
  content: |
    #!/bin/sh
    echo "this is from a boothook." > /var/tmp/boothook.txt
- type: "text/cloud-config"
  content: |
    bootcmd:
    - echo "this is from a cloud-config." > /var/tmp/bootcmd.txt
`, result)
}

func TestCloudConfigArchiveDifferentIndentationLevel(t *testing.T) {
	result, err := plugin.PrependShellScriptToUserData(`#cloud-config-archive
-   type: "text/cloud-boothook"
    content: |
        #!/bin/sh
        echo "this is from a boothook." > /var/tmp/boothook.txt
-   type: "text/cloud-config"
    content: |
        bootcmd:
        - echo "this is from a cloud-config." > /var/tmp/bootcmd.txt
`, ShellScript)
	require.NoError(t, err)
	require.Equal(t, `#cloud-config-archive
-   type: "text/cloud-boothook"
    content: |
        #!/bin/bash
        echo "Hello, world"
-   type: "text/cloud-boothook"
    content: |
        #!/bin/sh
        echo "this is from a boothook." > /var/tmp/boothook.txt
-   type: "text/cloud-config"
    content: |
        bootcmd:
        - echo "this is from a cloud-config." > /var/tmp/bootcmd.txt
`, result)
}

func TestShellScript(t *testing.T) {
	result, err := plugin.PrependShellScriptToUserData(`#!/bin/sh
shutdown -h 10
`, ShellScript)
	require.NoError(t, err)
	require.Equal(t, strings.ReplaceAll(`Content-Type: multipart/mixed; boundary="ahy6Vaphi9tiesoo"
MIME-Version: 1.0
Number-Attachments: 2

--ahy6Vaphi9tiesoo
Content-Disposition: attachment; filename="autoscaler-added-part-1"
Content-Transfer-Encoding: 7bit
Content-Type: text/cloud-boothook; charset="us-ascii"
Mime-Version: 1.0

#!/bin/bash
echo "Hello, world"
--ahy6Vaphi9tiesoo
Content-Disposition: attachment; filename="autoscaler-added-part-2"
Content-Transfer-Encoding: 7bit
Content-Type: text/cloud-boothook; charset="us-ascii"
Mime-Version: 1.0

#!/bin/sh
shutdown -h 10
--ahy6Vaphi9tiesoo--
`, "\n", "\r\n"), result)
}

func TestMultipartMime(t *testing.T) {
	result, err := plugin.PrependShellScriptToUserData(
		`Content-Type: multipart/mixed; boundary="===============2389165605550749110=="
MIME-Version: 1.0
Number-Attachments: 2

--===============2389165605550749110==
Content-Type: text/cloud-boothook; charset="us-ascii"
MIME-Version: 1.0
Content-Transfer-Encoding: 7bit
Content-Disposition: attachment; filename="part-001"

#!/bin/sh
echo "this is from a boothook." > /var/tmp/boothook.txt

--===============2389165605550749110==
Content-Type: text/cloud-config; charset="us-ascii"
MIME-Version: 1.0
Content-Transfer-Encoding: 7bit
Content-Disposition: attachment; filename="part-002"

bootcmd:
- echo "this is from a cloud-config." > /var/tmp/bootcmd.txt
--===============2389165605550749110==--
`,
		ShellScript,
	)
	require.NoError(t, err)
	require.Equal(
		t,
		strings.ReplaceAll(
			`Content-Type: multipart/mixed; boundary="===============2389165605550749110=="
MIME-Version: 1.0
Number-Attachments: 3

--===============2389165605550749110==
Content-Disposition: attachment; filename="autoscaler-added-part-1"
Content-Transfer-Encoding: 7bit
Content-Type: text/cloud-boothook; charset="us-ascii"
Mime-Version: 1.0

#!/bin/bash
echo "Hello, world"
--===============2389165605550749110==
Content-Disposition: attachment; filename="part-001"
Content-Transfer-Encoding: 7bit
Content-Type: text/cloud-boothook; charset="us-ascii"
Mime-Version: 1.0

#!/bin/sh
echo "this is from a boothook." > /var/tmp/boothook.txt

--===============2389165605550749110==
Content-Disposition: attachment; filename="part-002"
Content-Transfer-Encoding: 7bit
Content-Type: text/cloud-config; charset="us-ascii"
Mime-Version: 1.0

bootcmd:
- echo "this is from a cloud-config." > /var/tmp/bootcmd.txt
--===============2389165605550749110==--
`,
			"\n",
			"\r\n",
		),
		result,
	)
}
