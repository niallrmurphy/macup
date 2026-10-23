package inventory

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseBrewVersions(t *testing.T) {
	p := parseBrewVersions([]byte("node 24.1.0 24.2.0\nphp 8.4.1\n"), "formula")
	if len(p) != 2 || p[0].Installed != "24.1.0, 24.2.0" || p[1].Name != "php" {
		t.Fatalf("unexpected packages: %+v", p)
	}
}
func TestScanHomebrewWith(t *testing.T) {
	fake := func(_ context.Context, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "list --formula --versions":
			return []byte("node 24.1.0\n"), nil
		case "list --cask --versions":
			return []byte("firefox 140.0\n"), nil
		case "info --json=v2 --installed":
			return []byte(`{"casks":[{"token":"firefox","artifacts":[{"app":["Firefox.app"],"target":"/Applications/Firefox.app"}]}]}`), nil
		case "outdated --json=v2":
			return []byte(`{"formulae":[{"name":"node","current_version":"24.2.0"}],"casks":[]}`), nil
		}
		return nil, errors.New("unknown command")
	}
	r := ScanHomebrewWith(context.Background(), fake)
	if !r.Available || len(r.Packages) != 2 || len(r.Warnings) != 0 {
		t.Fatalf("unexpected result: %+v", r)
	}
	if !r.Packages[1].Outdated || r.Packages[1].Current != "24.2.0" {
		t.Fatalf("unexpected formula: %+v", r.Packages[1])
	}
}
