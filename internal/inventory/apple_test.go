package inventory

import (
	"context"
	"errors"
	"testing"
)

const softwareUpdateListing = `Software Update Tool

Finding available software
Software Update found the following new or updated software:
* Label: macOS Sequoia 15.2-24C101
	Title: macOS Sequoia 15.2, Version: 15.2, Size: 3345442KiB, Recommended: YES, Action: restart, 
* Label: Command Line Tools for Xcode-16.2
	Title: Command Line Tools for Xcode, Version: 16.2, Size: 713258KiB, Recommended: YES, 
`

func TestParseAppleUpdates(t *testing.T) {
	u, err := parseAppleUpdates([]byte(softwareUpdateListing))
	if err != nil || len(u) != 2 {
		t.Fatalf("%+v %v", u, err)
	}
	if u[0].Label != "macOS Sequoia 15.2-24C101" || u[0].Title != "macOS Sequoia 15.2" || u[0].Version != "15.2" || !u[0].Restart || !u[0].Recommended || u[0].Size != "3345442KiB" {
		t.Fatalf("%+v", u[0])
	}
	if u[1].Label != "Command Line Tools for Xcode-16.2" || u[1].Version != "16.2" || u[1].Restart {
		t.Fatalf("%+v", u[1])
	}
	none, err := parseAppleUpdates([]byte("Software Update Tool\n\nFinding available software\nNo new software available.\n"))
	if err != nil || len(none) != 0 {
		t.Fatalf("%+v %v", none, err)
	}
	if _, err := parseAppleUpdates([]byte("something unexpected")); err == nil {
		t.Fatal("unknown output must not read as no updates")
	}
}

func TestAppleScanAndApply(t *testing.T) {
	res := ScanAppleWith(context.Background(), func(context.Context) ([]byte, error) { return []byte(softwareUpdateListing), nil })
	if !res.Available || res.CheckStatus != "checked" || len(res.Updates) != 2 {
		t.Fatalf("%+v", res)
	}
	r := Result{Apple: &res}
	ApplyApple(&r)
	if len(r.Entries) != 2 || r.Entries[1].Status != "OUTDATED" || r.Entries[1].UpdateSource != "softwareupdate" || r.Entries[1].Detail != "label:Command Line Tools for Xcode-16.2" || r.Entries[1].Available != "16.2" {
		t.Fatalf("%+v", r.Entries)
	}
	failed := ScanAppleWith(context.Background(), func(context.Context) ([]byte, error) { return nil, errors.New("boom") })
	if failed.Available || failed.CheckStatus != "failed" || len(failed.Warnings) == 0 {
		t.Fatalf("%+v", failed)
	}
	r = Result{Apple: &failed}
	ApplyApple(&r)
	if len(r.Entries) != 0 {
		t.Fatal("a failed check must not add entries")
	}
}
