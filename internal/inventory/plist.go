package inventory

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// parsePlist reads scalar values from the top-level dict in XML plists. Complex
// values are skipped. Binary plists are converted with the macOS plutil tool.
func loadPlist(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if bytes.HasPrefix(data, []byte("bplist00")) {
		cmd := exec.Command("/usr/bin/plutil", "-convert", "xml1", "-o", "-", "--", path)
		data, err = cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("converting binary plist via plutil: %w", err)
		}
	}
	return parsePlist(data)
}

func parsePlist(data []byte) (map[string]string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	result := make(map[string]string)
	depth := 0
	dictDepth := -1
	pending := ""
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "dict" && dictDepth == -1 {
				dictDepth = depth
				continue
			}
			if dictDepth == -1 || depth != dictDepth+1 {
				continue
			}
			switch t.Name.Local {
			case "key":
				var v string
				if err := decoder.DecodeElement(&v, &t); err != nil {
					return nil, err
				}
				pending = strings.TrimSpace(v)
				depth--
			case "string", "integer", "real", "date":
				var v string
				if err := decoder.DecodeElement(&v, &t); err != nil {
					return nil, err
				}
				if pending != "" {
					result[pending] = strings.TrimSpace(v)
				}
				pending = ""
				depth--
			case "true", "false":
				if pending != "" {
					result[pending] = t.Name.Local
				}
				pending = ""
			default:
				pending = ""
			}
		case xml.EndElement:
			if t.Name.Local == "dict" && depth == dictDepth {
				dictDepth = -2
			}
			depth--
		}
	}
	if dictDepth == -1 {
		return nil, fmt.Errorf("no plist dict found")
	}
	return result, nil
}

// BundlePlist exposes an application's top-level Info.plist scalars, such as
// SUPublicEDKey, to other packages.
func BundlePlist(appPath string) (map[string]string, error) {
	return loadPlist(filepath.Join(appPath, "Contents", "Info.plist"))
}
