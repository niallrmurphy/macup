package probe

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
)

// readPlistScalars returns top-level string and boolean values of a plist,
// converting binary plists through plutil. It never evaluates nested structures.
func readPlistScalars(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) > 2<<20 {
		return nil, errors.New("plist too large")
	}
	if bytes.HasPrefix(raw, []byte("bplist00")) {
		raw, err = runCommand(context.Background(), "/usr/bin/plutil", "-convert", "xml1", "-o", "-", "--", path)
		if err != nil {
			return nil, fmt.Errorf("convert plist: %w", err)
		}
	}
	return parsePlistScalars(raw), nil
}

func parsePlistScalars(raw []byte) map[string]string {
	fields := map[string]string{}
	d := xml.NewDecoder(bytes.NewReader(raw))
	key := ""
	depth := 0
	for {
		tok, e := d.Token()
		if e == io.EOF || e != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "plist":
				continue
			case "key":
				var s string
				if d.DecodeElement(&s, &t) == nil && depth == 1 {
					key = s
				}
			case "string", "integer", "real", "date":
				var s string
				if d.DecodeElement(&s, &t) == nil && key != "" && depth == 1 {
					fields[key] = s
				}
				key = ""
			case "true", "false":
				if key != "" && depth == 1 {
					fields[key] = t.Name.Local
				}
				key = ""
				d.Skip()
			default:
				// dict, array, data: descend; only dict depth 1 keys are scalars we keep.
				depth++
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "plist", "key", "string", "integer", "real", "date", "true", "false":
			default:
				depth--
			}
		}
	}
	return fields
}
