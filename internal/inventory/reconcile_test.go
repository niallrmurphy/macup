package inventory

import "testing"

func TestReconcileExactTarget(t *testing.T) {
	r := Result{Applications: []Application{{Name: "Firefox", Version: "140", Path: "/Applications/Firefox.app", BundleID: "org.mozilla.firefox"}}, Homebrew: &HomebrewResult{Packages: []BrewPackage{{Name: "firefox", Kind: "cask", Installed: "140", Current: "141", Outdated: true, AppPaths: []string{"/Applications/Firefox.app"}}}}}
	Reconcile(&r)
	if len(r.Entries) != 1 || len(r.Entries[0].Sources) != 2 || r.Entries[0].Status != "OUTDATED" || r.Entries[0].Package != "firefox" {
		t.Fatalf("bad match: %+v", r.Entries)
	}
}
func TestReconcileDoesNotGuess(t *testing.T) {
	for _, paths := range [][]string{nil, {"/Applications/Other.app"}, {"/Applications/Firefox.app", "/Applications/Helper.app"}} {
		r := Result{Applications: []Application{{Name: "Firefox", Path: "/Applications/Firefox.app"}}, Homebrew: &HomebrewResult{Packages: []BrewPackage{{Name: "firefox", Kind: "cask", AppPaths: paths}}}}
		Reconcile(&r)
		if len(r.Entries) != 2 {
			t.Fatalf("unsafe match for %v", paths)
		}
	}
}
func TestReconcileAmbiguousCasks(t *testing.T) {
	r := Result{Applications: []Application{{Name: "Firefox", Path: "/Applications/Firefox.app"}}, Homebrew: &HomebrewResult{Packages: []BrewPackage{{Name: "one", Kind: "cask", AppPaths: []string{"/Applications/Firefox.app"}}, {Name: "two", Kind: "cask", AppPaths: []string{"/Applications/Firefox.app"}}}}}
	Reconcile(&r)
	if len(r.Entries) != 3 {
		t.Fatalf("ambiguous cask merged: %+v", r.Entries)
	}
}
func TestParseInstalledCaskPaths(t *testing.T) {
	raw := []byte(`{"casks":[{"token":"firefox","artifacts":[{"app":["Firefox.app"],"target":"/Applications/Firefox.app"},{"pkg":["thing.pkg"],"target":"/tmp/pkg"},{"app":["Other.app"],"target":"relative.app"}]}]}`)
	paths, err := parseInstalledCaskPaths(raw)
	if err != nil || len(paths["firefox"]) != 1 || paths["firefox"][0] != "/Applications/Firefox.app" {
		t.Fatalf("paths=%v err=%v", paths, err)
	}
}
