package plugin

import (
	"fmt"
	"strings"
)

// PrependShellScriptToUserData will prepend a cloud-boothook section to the
// existing user data, which may be empty, a
// bare shell command, or using the cloud-config-archive format
func PrependShellScriptToUserData(originalUserData, script string) (string, error) {
	originalUserData = strings.TrimSpace(originalUserData)
	if originalUserData != "" && !strings.HasPrefix(originalUserData, "#!/bin/bash\n") {
		return "", fmt.Errorf("only bash scripts are supported, not ones starting with %v", originalUserData[:20])
	}
	script = strings.TrimSpace(script)
	if !strings.HasPrefix(script, "#!/bin/bash\n") {
		return "", fmt.Errorf("only bash scripts are supported in the injected script, not ones starting with %v", script[:20])
	}

	// the shebang only is treated as a non-comment on the first line, so simply concatenating
	// these files is good enough
	return fmt.Sprintf("%v\n%v\n", script, originalUserData), nil
}
