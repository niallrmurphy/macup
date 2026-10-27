package output

import (
	"bytes"
	"github.com/niallrmurphy/macup/internal/inventory"
	"strings"
	"testing"
)

func TestUnifiedTable(t *testing.T) {
	r := inventory.Result{Applications: []inventory.Application{{Name: "Example", Version: "1", Path: "/Applications/Example.app"}}, Homebrew: &inventory.HomebrewResult{Packages: []inventory.BrewPackage{{Name: "node", Kind: "formula", Installed: "22", Outdated: true, Current: "24"}}}, AppStore: &inventory.AppStoreResult{Packages: []inventory.AppStorePackage{{ID: "42", Name: "Xcode", Installed: "16"}}}}
	var b bytes.Buffer
	if err := Table(&b, r); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	for _, want := range []string{"SOURCE", "appstore", "brew-formula", "Example", "OUTDATED", "3 entries"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
	if strings.Count(s, "SOURCE") != 2 {
		t.Fatal("multiple tables")
	}
}

func TestUpdateSourceColumn(t *testing.T) {
	r := inventory.Result{Entries: []inventory.Entry{
		{Name: "App with Sparkle", Installed: "1", Available: "2", Status: "OUTDATED", Sources: []string{"apps", "sparkle"}, UpdateSource: "sparkle"},
		{Name: "Formula", Installed: "1", Available: "2", Status: "OUTDATED", Sources: []string{"brew-formula"}, UpdateSource: "homebrew"},
	}}
	var b bytes.Buffer
	if err := Table(&b, r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"UPDATE SOURCE", "sparkle", "homebrew"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q: %s", want, b.String())
		}
	}
}

func TestMicrosoftProviderSummary(t *testing.T) {
	r := inventory.Result{Microsoft: &inventory.MicrosoftResult{Available: true, CheckStatus: "checked", ResponseFormat: "text", Updates: []inventory.MicrosoftUpdate{}}, Entries: []inventory.Entry{{Name: "Microsoft Word", Status: "UNCHECKED", Sources: []string{"apps"}}}}
	var b bytes.Buffer
	if err := Table(&b, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "provider (microsoft): checked; 0 updates reported (text response)") {
		t.Fatalf("missing provider status: %s", b.String())
	}
	if !strings.Contains(b.String(), "UNCHECKED") {
		t.Fatalf("incorrect app-level status: %s", b.String())
	}
}

func TestAvailableVersionLabels(t *testing.T) {
	for _, tc := range []struct{ version, status, want string }{
		{"", "CURRENT", "—"}, {"", "UNCHECKED", "?"}, {"", "UNKNOWN", "?"}, {"1.2", "OUTDATED", "1.2"},
	} {
		if got := availableLabel(tc.version, tc.status); got != tc.want {
			t.Errorf("%q/%q = %q, want %q", tc.version, tc.status, got, tc.want)
		}
	}
}

func TestTableHidesCurrentByDefault(t *testing.T) {
	r := inventory.Result{Entries: []inventory.Entry{
		{Name: "Stale", Installed: "1", Available: "2", Status: "OUTDATED", Sources: []string{"apps"}},
		{Name: "Fine", Installed: "2", Status: "CURRENT", Sources: []string{"brew-formula"}},
		{Name: "Unknown", Installed: "3", Status: "UNCHECKED", Sources: []string{"apps"}},
	}}
	var b bytes.Buffer
	if err := TableWith(&b, r, TableOptions{}); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	if strings.Contains(s, "Fine") || !strings.Contains(s, "Stale") || !strings.Contains(s, "Unknown") || !strings.Contains(s, "2 entries shown; 1 current entries hidden") {
		t.Fatal(s)
	}
	b.Reset()
	if err := TableWith(&b, r, TableOptions{ShowAll: true}); err != nil {
		t.Fatal(err)
	}
	if s = b.String(); !strings.Contains(s, "Fine") || !strings.Contains(s, "3 entries") {
		t.Fatal(s)
	}
}

func TestTableStatusFilter(t *testing.T) {
	r := inventory.Result{Entries: []inventory.Entry{
		{Name: "Stale", Status: "OUTDATED", Sources: []string{"apps"}},
		{Name: "Fine", Status: "CURRENT", Sources: []string{"apps"}},
		{Name: "Mystery", Status: "UNCHECKED", Sources: []string{"apps"}},
	}}
	var b bytes.Buffer
	if err := TableWith(&b, r, TableOptions{Statuses: []string{"unchecked", " current"}}); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	if strings.Contains(s, "Stale") || !strings.Contains(s, "Fine") || !strings.Contains(s, "Mystery") || !strings.Contains(s, "2 entries shown with status UNCHECKED, CURRENT; 1 hidden") {
		t.Fatal(s)
	}
}
