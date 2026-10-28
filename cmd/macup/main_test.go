package main

import (
	"context"
	"github.com/niallrmurphy/macup/internal/inventory"
	"github.com/niallrmurphy/macup/internal/probe"
	"os"
	"path/filepath"
	"testing"
)

func TestProviders(t *testing.T) {
	for _, tc := range []struct {
		input      string
		m, s, fail bool
	}{
		{"all", true, true, false}, {"none", false, false, false}, {"microsoft", true, false, false}, {"sparkle", false, true, false}, {"microsoft,sparkle", true, true, false}, {"bogus", false, false, true}, {"none,microsoft", false, false, true},
	} {
		p, err := parseProviders(tc.input)
		if (err != nil) != tc.fail {
			t.Fatalf("%s err %v", tc.input, err)
		}
		if err == nil && (p["microsoft"] != tc.m || p["sparkle"] != tc.s) {
			t.Fatalf("%s: %v", tc.input, p)
		}
	}
}

func TestProbeAllBatch(t *testing.T) {
	root := t.TempDir()
	makeApp := func(name string) string {
		p := filepath.Join(root, name+".app")
		if err := os.MkdirAll(filepath.Join(p, "Contents"), 0755); err != nil {
			t.Fatal(err)
		}
		plist := `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleName</key><string>` + name + `</string><key>CFBundleIdentifier</key><string>org.example.` + name + `</string><key>CFBundleShortVersionString</key><string>1.0</string></dict></plist>`
		if err := os.WriteFile(filepath.Join(p, "Contents", "Info.plist"), []byte(plist), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p1 := makeApp("One")
	p2 := makeApp("Two")
	apps := []inventory.Application{{Path: p1}, {Path: filepath.Join(root, "missing.app")}, {Path: p2}}
	got := runProbeBatch(context.Background(), apps, nil, probe.Options{}, 2)
	if got.Total != 3 || got.Succeeded != 2 || got.Failed != 1 || len(got.Results) != 3 {
		t.Fatalf("unexpected batch: %+v", got)
	}
	if got.Results[0].Path != p1 || got.Results[1].Error == "" || got.Results[2].Path != p2 {
		t.Fatalf("ordering or error handling: %+v", got.Results)
	}
}

func TestProgressSummaries(t *testing.T) {
	if got := summariseOutdated(false, 3, 1); got != "unavailable" {
		t.Fatal(got)
	}
	if got := summariseOutdated(true, 3, 1); got != "3 packages; 1 outdated" {
		t.Fatal(got)
	}
	if got := summariseCheck("checked", 2, "updates reported"); got != "checked; 2 updates reported" {
		t.Fatal(got)
	}
	if got := summariseCheck("failed", 0, "x"); got != "failed" {
		t.Fatal(got)
	}
	if got := summariseCheck("", 0, "x"); got != "unavailable" {
		t.Fatal(got)
	}
	if countBrewOutdated([]inventory.BrewPackage{{Outdated: true}, {}}) != 1 || countMasOutdated([]inventory.AppStorePackage{{Outdated: true}, {Outdated: true}}) != 2 {
		t.Fatal("outdated counts")
	}
}

func TestCacheKeys(t *testing.T) {
	a := inventory.Application{Path: "/Applications/A.app", Version: "1.0", Build: "10", BundleID: "com.microsoft.Word"}
	b := a
	b.Build = "11"
	if sparkleKey(a) == sparkleKey(b) || lookupKey(a) == lookupKey(b) {
		t.Fatal("build change must change the key")
	}
	k1 := versionsKey([]inventory.Application{a, {BundleID: "org.other", Version: "9"}}, "com.microsoft.")
	k2 := versionsKey([]inventory.Application{b}, "com.microsoft.")
	k3 := versionsKey([]inventory.Application{a, {BundleID: "org.other", Version: "10"}}, "com.microsoft.")
	if k1 == k2 || k1 != k3 {
		t.Fatal(k1, k2, k3)
	}
}

func TestProvidersIncludeLookup(t *testing.T) {
	all, err := parseProviders("all")
	if err != nil || !all["lookup"] || !all["sparkle"] {
		t.Fatalf("%v %v", all, err)
	}
	only, err := parseProviders("lookup")
	if err != nil || !only["lookup"] || only["sparkle"] {
		t.Fatalf("%v %v", only, err)
	}
	if _, err = parseProviders("none,lookup"); err == nil {
		t.Fatal("none must not combine")
	}
}

func TestVersionString(t *testing.T) {
	version, commit, date = "1.0.0", "abc1234", "2026-10-28"
	if got := versionString(); got != "macup 1.0.0 (abc1234, 2026-10-28)" {
		t.Fatal(got)
	}
	commit, date = "", ""
	if got := versionString(); got != "macup 1.0.0" {
		t.Fatal(got)
	}
}
