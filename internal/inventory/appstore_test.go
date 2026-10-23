package inventory

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAppStoreScan(t *testing.T) {
	fake := func(_ context.Context, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "list":
			return []byte("497799835 Xcode (16.3)\n123456789 A Thing (2.0)\n"), nil
		case "outdated":
			return []byte("497799835 Xcode (16.3 -> 16.4)\n"), nil
		}
		return nil, errors.New("unknown")
	}
	r := ScanAppStoreWith(context.Background(), fake)
	if !r.Available || len(r.Packages) != 2 || len(r.Warnings) != 0 {
		t.Fatalf("unexpected: %+v", r)
	}
	if !r.Packages[1].Outdated || r.Packages[1].AvailableVersion != "16.4" {
		t.Fatalf("unexpected outdated: %+v", r.Packages[1])
	}
}
