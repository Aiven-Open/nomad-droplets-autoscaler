package plugin

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"
)

/*
// WriteValueToFileOnBoot will write the provided string to a file during droplet cloud-init
func WriteValueToFileOnBoot(originalUserData, filename, value string) (string, error) {
	// this is very hacky, it's just a placeholder
	if len(originalUserData) > 0 && originalUserData[0] != '#' {
		// mime multipart
		return "", fmt.Errorf("MIME multipart not (yet) supported")
	}
	var builder strings.Builder
	builder.WriteString(originalUserData)
	builder.WriteString(fmt.Sprintf(`\n
#cloud-boothook
#!/bin/sh
echo "%v" > "%v"`, strconv.Quote(value), strconv.Quote(filename)))
	return builder.String(), nil
}
*/

// PrependShellScriptToUserData will prepend a cloud-boothook section to the
// existing user data, which may be empty, in mime-multipart format, a
// bare shell command, or using the cloud-config-archive format
func PrependShellScriptToUserData(originalUserData, script string) (string, error) {
	originalUserData = strings.TrimSpace(originalUserData)

	// empty original data
	if len(originalUserData) == 0 {
		return script, nil
	}

	// MIME multipart
	if strings.HasPrefix(originalUserData, "Content-Type:") {
		sections := strings.SplitN(originalUserData, "\n", 2)
		mediaType, params, err := mime.ParseMediaType(sections[0][len("Content-Type:"):])
		if err == nil && strings.HasPrefix(mediaType, "multipart/") {
			mr := multipart.NewReader(strings.NewReader(originalUserData), params["boundary"])
			if newUserData, err := prependToMimeUserData(mr, params["boundary"], script); err == nil {
				return newUserData, nil
			} else {
				return "", fmt.Errorf("could not prepend to mime user data: %w", err)
			}
		}
	}

	// raw shell script, so promote to MIME multipart
	if strings.HasPrefix(originalUserData, "#!") {
		if newUserData, err := prependToMimeUserData(nil, "ahy6Vaphi9tiesoo", script, originalUserData); err == nil {
			return newUserData, nil
		} else {
			return "", fmt.Errorf("could not prepend to mime user data: %w", err)
		}
	}

	// cloud config archive, so just prepend another cloud-boothook
	if strings.HasPrefix(originalUserData, "#cloud-config-archive\n") {
		sections := strings.SplitN(originalUserData, "\n", 2)
		if indentation, err := inferYamlIndentation(sections[1]); err == nil {
			return fmt.Sprintf(`#cloud-config-archive
%vtype: "text/cloud-boothook"
%vcontent: |
%v%v%v
%v
`, strings.Replace(indentation, " ", "-", 1), indentation, indentation, indentation, strings.ReplaceAll(script, "\n", fmt.Sprintf("\n%v%v", indentation, indentation)), sections[1]), nil
		} else {
			return "", fmt.Errorf("cannot infer the yaml indentation level: %w", err)
		}
	}
	return "", errors.New("unrecognised user data format")
}

func inferYamlIndentation(yaml string) (string, error) {
	// a bit of a hack. Count the spaces after the first `-` and add one.
	if len(yaml) < 2 {
		return "", errors.New("yaml document is too small")
	}
	if yaml[0] != byte('-') {
		return "", fmt.Errorf("yaml must start with -, found %q", yaml[:10])
	}
	if yaml[1] != ' ' {
		return "", errors.New("yaml must start with - followed by at least one space")
	}
	for i, character := range yaml {
		if i > 1 && character != ' ' {
			return strings.Repeat(" ", i), nil
		}
	}
	return "", errors.New("could not find a non-space character after the initial '-'")
}

func prependToMimeUserData(
	reader *multipart.Reader,
	boundary string,
	scripts ...string,
) (string, error) {
	var parts int
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.SetBoundary(boundary); err != nil {
		return "", fmt.Errorf("invalid boundary %q: %w", boundary, err)
	}
	for _, script := range scripts {
		parts = parts + 1
		header := textproto.MIMEHeader{}
		header.Add("Content-Type", `text/cloud-boothook; charset="us-ascii"`)
		header.Add("MIME-Version", "1.0")
		header.Add("Content-Transfer-Encoding", "7bit")
		header.Add(
			"Content-Disposition",
			fmt.Sprintf(`attachment; filename="autoscaler-added-part-%v"`, parts),
		)
		part, err := writer.CreatePart(header)
		if err != nil {
			return "", fmt.Errorf("could not create new MIME part: %w", err)
		}
		if _, err := part.Write([]byte(strings.ReplaceAll(script, "\n", "\r\n"))); err != nil {
			return "", fmt.Errorf("could not write new MIME part: %w", err)
		}
	}
	if reader != nil {
		for {
			p, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			parts = parts + 1
			if err != nil {
				return "", fmt.Errorf("unable to read part of the MIME multipart message: %w", err)
			}
			slurp, err := io.ReadAll(p)
			if err != nil {
				return "", fmt.Errorf("could not read MIME part: %w", err)
			}
			part, err := writer.CreatePart(p.Header)
			if err != nil {
				return "", fmt.Errorf("could not create MIME part: %w", err)
			}
			if _, err := part.Write(bytes.ReplaceAll(slurp, []byte("\n"), []byte("\r\n"))); err != nil {
				return "", fmt.Errorf("could not write MIME part: %w", err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	return strings.ReplaceAll(fmt.Sprintf(`Content-Type: multipart/mixed; boundary="%v"
MIME-Version: 1.0
Number-Attachments: %v

`, boundary, parts), "\n", "\r\n") + buf.String(), nil
}
