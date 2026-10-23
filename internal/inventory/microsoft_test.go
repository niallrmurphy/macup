package inventory

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseMicrosoftUpdates(t *testing.T) {
	updates, err := parseMicrosoftUpdates([]byte(`{"Updates":[{"Application ID":"MSWD2019","Application Name":"Microsoft Word","Available Version":"16.114.2"}]}`))
	if err != nil || len(updates) != 1 || updates[0].ID != "MSWD2019" || updates[0].AvailableVersion != "16.114.2" {
		t.Fatalf("unexpected: %+v %v", updates, err)
	}
	for _, data := range []string{`{}`, `{"Updates":"oops"}`, `{"Updates":[{"Application Name":"Word"}]}`} {
		if _, err := parseMicrosoftUpdates([]byte(data)); err == nil {
			t.Fatalf("accepted invalid: %s", data)
		}
	}
}
func TestMicrosoftScanAndApply(t *testing.T) {
	cmd := func(context.Context) ([]byte, error) { return []byte("mock plist"), nil }
	convert := func(_ context.Context, data []byte) ([]byte, error) {
		if string(data) != "mock plist" {
			t.Fatal("unexpected data")
		}
		return []byte(`{"Updates":[{"Application ID":"MSWD2019","Application Name":"Microsoft Word","Available Version":"16.114.2"},{"Application ID":"MSau04","Application Name":"Microsoft AutoUpdate","Available Version":"4.82"}]}`), nil
	}
	result := ScanMicrosoftWith(context.Background(), cmd, convert)
	if !result.Available || len(result.Updates) != 2 {
		t.Fatalf("bad result: %+v", result)
	}
	r := Result{Microsoft: &result, Entries: []Entry{{Name: "Microsoft Word", Path: "/Applications/Microsoft Word.app", Installed: "16.114.1", Status: "UNCHECKED", Sources: []string{"apps"}}}}
	ApplyMicrosoft(&r)
	if len(r.Entries) != 2 || r.Entries[0].Status != "OUTDATED" || r.Entries[0].UpdateSource != "microsoft-autoupdate" {
		t.Fatalf("bad entries: %+v", r.Entries)
	}
	if r.Entries[1].Sources[0] != "microsoft-autoupdate" {
		t.Fatalf("missing orphan: %+v", r.Entries)
	}
}
func TestMicrosoftUnknownOutput(t *testing.T) {
	r := ScanMicrosoftWith(context.Background(), func(context.Context) ([]byte, error) { return []byte("x"), nil }, func(context.Context, []byte) ([]byte, error) { return []byte(`{"unexpected":[]}`), nil })
	if r.Available || len(r.Warnings) == 0 || !strings.Contains(r.Warnings[0], "missing Updates") {
		t.Fatalf("expected warning: %+v", r)
	}
	r = ScanMicrosoftWith(context.Background(), func(context.Context) ([]byte, error) { return nil, errors.New("permission denied") }, nil)
	if r.Available || len(r.Warnings) == 0 {
		t.Fatalf("expected command warning: %+v", r)
	}
}
func TestMicrosoftAmbiguousNames(t *testing.T) {
	m := MicrosoftResult{Available: true, Updates: []MicrosoftUpdate{{ID: "MSWD2019", Name: "Microsoft Word", AvailableVersion: "17"}}}
	r := Result{Microsoft: &m, Entries: []Entry{{Name: "Microsoft Word", Path: "/Applications/A.app", Sources: []string{"apps"}}, {Name: "Microsoft Word", Path: "/Applications/B.app", Sources: []string{"apps"}}}}
	ApplyMicrosoft(&r)
	if len(r.Entries) != 3 || r.Entries[0].Available != "" || r.Entries[1].Available != "" {
		t.Fatalf("ambiguous matching: %+v", r.Entries)
	}
}

func TestMicrosoftPlainTextNoUpdates(t *testing.T) {
	texts := []string{
		"\x1b[2K Checking for updates...\r\x1b[2K Update Assistant: Checking\r\x1b[2K No updates available\r\x1b[2K Update Assistant: Idle",
		"Checking for updates...\nNo updates available\n",
	}
	for _, text := range texts {
		r := ScanMicrosoftWith(context.Background(), func(context.Context) ([]byte, error) { return []byte(text), nil },
			func(context.Context, []byte) ([]byte, error) { return nil, errors.New("not a plist") })
		if !r.Available || len(r.Updates) != 0 || len(r.Warnings) != 0 {
			t.Fatalf("bad success: %+v", r)
		}
	}
	for _, text := range []string{"Update Assistant: Checking", "No updates available\nError: permission denied", "No updates available\nUpdates are available", ""} {
		if microsoftNoUpdatesText([]byte(text)) {
			t.Fatalf("false no-updates detection: %q", text)
		}
	}
}

func TestMicrosoftProviderCheckStates(t *testing.T) {
	noUpdates := ScanMicrosoftWith(context.Background(),
		func(context.Context) ([]byte, error) { return []byte("No updates available"), nil },
		func(context.Context, []byte) ([]byte, error) { return nil, errors.New("not plist") })
	if noUpdates.CheckStatus != "checked" || noUpdates.ResponseFormat != "text" || !noUpdates.Available {
		t.Fatalf("unexpected no-updates: %+v", noUpdates)
	}
	r := Result{Microsoft: &noUpdates, Entries: []Entry{{Name: "Microsoft Word", Status: "UNCHECKED", Sources: []string{"apps"}, Path: "/Applications/Microsoft Word.app"}}}
	ApplyMicrosoft(&r)
	if r.Entries[0].Status != "CURRENT" {
		t.Fatalf("MAU zero updates should mark core Office current: %+v", r.Entries[0])
	}
	failed := ScanMicrosoftWith(context.Background(), func(context.Context) ([]byte, error) { return []byte("garbage"), nil }, func(context.Context, []byte) ([]byte, error) { return nil, errors.New("bad") })
	if failed.CheckStatus != "failed" || failed.Available {
		t.Fatalf("failure: %+v", failed)
	}
	missing := ScanMicrosoftWith(context.Background(), func(context.Context) ([]byte, error) { return nil, errors.New("permission denied") }, nil)
	if missing.CheckStatus != "failed" {
		t.Fatalf("command failure: %+v", missing)
	}
}

func TestMicrosoftCurrentCoverage(t *testing.T) {
	m := MicrosoftResult{Available: true, CheckStatus: "checked"}
	r := Result{Microsoft: &m, Entries: []Entry{
		{Name: "Microsoft Word", BundleID: "com.microsoft.Word", Path: "/Applications/Microsoft Word.app", Status: "UNCHECKED", Sources: []string{"apps"}},
		{Name: "Microsoft Teams", Path: "/Applications/Microsoft Teams.app", Status: "UNCHECKED", Sources: []string{"apps"}},
		{Name: "Microsoft Edge", Path: "/Applications/Microsoft Edge.app", Status: "UNCHECKED", Sources: []string{"apps"}},
		{Name: "Microsoft Word", BundleID: "org.example.Word", Path: "/Applications/fake.app", Status: "UNCHECKED", Sources: []string{"apps"}},
	}}
	ApplyMicrosoft(&r)
	if r.Entries[0].Status != "CURRENT" || r.Entries[0].UpdateSource != "microsoft-autoupdate" {
		t.Fatalf("Office not checked: %+v", r.Entries[0])
	}
	for _, e := range r.Entries[1:] {
		if e.Status != "UNCHECKED" {
			t.Fatalf("out-of-scope marked current: %+v", e)
		}
	}
}
