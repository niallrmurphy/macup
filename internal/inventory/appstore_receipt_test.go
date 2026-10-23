package inventory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAnnotateAppStoreIDs(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "Example.app")
	receipt := filepath.Join(app, "Contents", "_MASReceipt", "receipt")
	if err := os.MkdirAll(filepath.Dir(receipt), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receipt, []byte("receipt bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	r := Result{Applications: []Application{{Name: "Example", Path: app}}}
	AnnotateAppStoreIDsWith(context.Background(), &r, func(_ context.Context, _ string) (string, error) { return "123456\n", nil })
	if r.Applications[0].AppStoreID != "123456" {
		t.Fatalf("missing ID: %+v", r.Applications[0])
	}
	for _, invalid := range []string{"(null)", "123oops", "0", ""} {
		r.Applications[0].AppStoreID = ""
		AnnotateAppStoreIDsWith(context.Background(), &r, func(_ context.Context, _ string) (string, error) { return invalid, nil })
		if r.Applications[0].AppStoreID != "" {
			t.Fatalf("accepted invalid id %q", invalid)
		}
	}
	os.Remove(receipt)
	r.Applications[0].AppStoreID = ""
	called := false
	AnnotateAppStoreIDsWith(context.Background(), &r, func(_ context.Context, _ string) (string, error) { called = true; return "123456", nil })
	if called || r.Applications[0].AppStoreID != "" {
		t.Fatal("receipt-less app was attributed")
	}
}
func TestReconcileAppStoreReceipt(t *testing.T) {
	r := Result{Applications: []Application{{Name: "Example", Path: "/Applications/Example.app", Version: "1.0", AppStoreID: "123"}}, AppStore: &AppStoreResult{Packages: []AppStorePackage{{ID: "123", Name: "Example", Installed: "1.0", AvailableVersion: "1.1", Outdated: true}}}}
	Reconcile(&r)
	if len(r.Entries) != 1 || len(r.Entries[0].Sources) != 2 || r.Entries[0].Status != "OUTDATED" {
		t.Fatalf("not reconciled: %+v", r.Entries)
	}
	r.Applications[0].AppStoreID = "456"
	Reconcile(&r)
	if len(r.Entries) != 2 {
		t.Fatalf("incorrect merge: %+v", r.Entries)
	}
	r.Applications[0].AppStoreID = "123"
	r.AppStore.Packages = append(r.AppStore.Packages, r.AppStore.Packages[0])
	Reconcile(&r)
	if len(r.Entries) != 3 {
		t.Fatalf("ambiguous mas IDs should not merge: %+v", r.Entries)
	}
}
func TestReconcileAppStoreNoCrossManagerMerge(t *testing.T) {
	r := Result{Applications: []Application{{Name: "Example", Path: "/Applications/Example.app", AppStoreID: "123"}}, Homebrew: &HomebrewResult{Packages: []BrewPackage{{Kind: "cask", Name: "example", AppPaths: []string{"/Applications/Example.app"}}}}, AppStore: &AppStoreResult{Packages: []AppStorePackage{{ID: "123", Name: "Example"}}}}
	Reconcile(&r)
	if len(r.Entries) != 2 {
		t.Fatalf("contradictory manager merge: %+v", r.Entries)
	}
}
