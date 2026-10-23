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
	if strings.Count(s, "SOURCE") != 1 {
		t.Fatal("multiple tables")
	}
}
