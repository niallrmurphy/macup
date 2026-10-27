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
