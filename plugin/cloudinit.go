package plugin

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml"
)

type CloudConfigPart struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type CloudConfigArchive struct {
	Parts []CloudConfigPart `json:"cloud-config-archive"`
}

func NewCloudConfigArchive(parts ...CloudConfigPart) *CloudConfigArchive {
	result := &CloudConfigArchive{Parts: make([]CloudConfigPart, 0, 3)}
	result.Parts = append(result.Parts, parts...)
	return result
}

func ParseCloudConfigArchive(data string) (*CloudConfigArchive, error) {
	result := &CloudConfigArchive{}
	err := yaml.Unmarshal(
		[]byte(
			"---\ncloud-config-archive:\n  "+strings.ReplaceAll(
				data,
				"\n",
				"\n  ",
			),
		),
		result,
	)
	return result, err
}

func (c *CloudConfigArchive) String() string {
	blankLine := regexp.MustCompile(`(?m)^\s*$`)
	var result strings.Builder
	_, _ = result.WriteString("#cloud-config-archive\n")
	for _, part := range c.Parts {
		_, _ = result.WriteString(fmt.Sprintf("- type: %v\n", part.Type))
		_, _ = result.WriteString(
			strings.TrimRight(blankLine.ReplaceAllLiteralString(
				fmt.Sprintf(
					"  content: |\n    %v\n",
					strings.ReplaceAll(part.Content, "\n", "\n    "),
				),
				"",
			), "\n") + "\n",
		)
	}
	return result.String()
}

// PrependShellScriptToUserData will prepend a cloud-boothook section to the
// existing user data, which may be empty, a
// bare shell command, or using the cloud-config-archive format
func PrependShellScriptToUserData(originalUserData, script string) (string, error) {
	originalUserData = strings.TrimSpace(originalUserData)
	cca := NewCloudConfigArchive(CloudConfigPart{Type: "text/x-shellscript", Content: script})

	// empty original data
	if len(originalUserData) == 0 {
		return cca.String(), nil
	}

	// MIME multipart
	if strings.HasPrefix(originalUserData, "Content-Type:") {
		return "", errors.New("MIME multipart is not supported")
	}

	// raw shell script, so append to cloud config archive
	if strings.HasPrefix(originalUserData, "#!") {
		cca.Parts = append(
			cca.Parts,
			CloudConfigPart{Type: "text/x-shellscript", Content: originalUserData},
		)
		return cca.String(), nil
	}

	// cloud config archive, so just prepend another script
	if strings.HasPrefix(originalUserData, "#cloud-config-archive\n") {
		sections := strings.SplitN(originalUserData, "\n", 2)
		originalCca, err := ParseCloudConfigArchive(sections[1])
		if err != nil {
			return "", fmt.Errorf("unable to parse original cloud-config-archive: %w", err)
		}
		cca.Parts = append(cca.Parts, originalCca.Parts...)
		return cca.String(), nil
	}
	return "", errors.New("unrecognised user data format")
}
