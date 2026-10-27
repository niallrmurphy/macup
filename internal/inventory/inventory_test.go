package inventory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadBundle(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "Sample.app")
	app, err := ReadBundle(path)
	if err != nil {
		t.Fatal(err)
	}
	if app.Name != "Sample App" || app.BundleID != "org.example.sample" || app.Version != "2.3.4" || app.Build != "234" {
		t.Fatalf("wrong bundle: %+v", app)
	}
}
func TestScanNestedAndDedup(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join("..", "..", "testdata", "Sample.app", "Contents", "Info.plist")
	appPath := filepath.Join(root, "Subdir", "Demo.app", "Contents")
	if err := os.MkdirAll(appPath, 0755); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appPath, "Info.plist"), bytes, 0644); err != nil {
		t.Fatal(err)
	}
	r := Scan(context.Background(), []string{root, filepath.Join(root, "Subdir")})
	if len(r.Applications) != 1 || len(r.Warnings) != 0 {
		t.Fatalf("scan: %+v", r)
	}
}
func TestMissingRootsAndBadBundle(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Broken.app"), 0755); err != nil {
		t.Fatal(err)
	}
	r := Scan(context.Background(), []string{root, filepath.Join(root, "doesnotexist")})
	if len(r.Applications) != 0 || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0].Message, "Info.plist") {
		t.Fatalf("unexpected: %+v", r)
	}
}
func TestParsePlistNestedValuesDontOverwrite(t *testing.T) {
	data := []byte(`<plist><dict><key>CFBundleName</key><string>Original</string><key>nested</key><dict><key>CFBundleName</key><string>Fake</string></dict><key>CFBundleVersion</key><integer>7</integer></dict></plist>`)
	m, err := parsePlist(data)
	if err != nil || m["CFBundleName"] != "Original" || m["CFBundleVersion"] != "7" {
		t.Fatalf("parse %v: %v", m, err)
	}
}

func TestReadBundleIOSWrapper(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "Carcassonne.app")
	inner := filepath.Join(app, "Wrapper", "carcassonne.app")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	info := `<?xml version="1.0"?><plist version="1.0"><dict>
 <key>CFBundleDisplayName</key><string>Carcassonne</string>
 <key>CFBundleIdentifier</key><string>com.example.carcassonne</string>
 <key>CFBundleShortVersionString</key><string>1.10.2967</string>
 <key>CFBundleVersion</key><string>2967</string>
 <key>DTPlatformName</key><string>iphoneos</string>
 </dict></plist>`
	meta := `<?xml version="1.0"?><plist version="1.0"><dict><key>itemId</key><integer>1199912736</integer></dict></plist>`
	if err := os.WriteFile(filepath.Join(inner, "Info.plist"), []byte(info), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Wrapper", "iTunesMetadata.plist"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := ReadBundle(app)
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "Carcassonne" || a.Platform != "ios" || a.AppStoreID != "1199912736" || a.Version != "1.10.2967" || a.Path != app {
		t.Fatalf("%+v", a)
	}
	if _, err = ReadBundle(filepath.Join(root, "Missing.app")); err == nil {
		t.Fatal("expected error for missing bundle")
	}
}

func TestApplyUpdatersOnlyRelabelsUnchecked(t *testing.T) {
	r := Result{Applications: []Application{{Name: "A", Path: "/Applications/A.app"}, {Name: "B", Path: "/Applications/B.app"}, {Name: "C", Path: "/Applications/C.app"}}}
	Reconcile(&r)
	r.Entries[1].Status = "OUTDATED"
	ApplyUpdaters(&r, func(a Application) UpdaterInfo {
		switch a.Name {
		case "A":
			return UpdaterInfo{Status: "SELF-UPDATING", Source: "squirrel"}
		case "B":
			return UpdaterInfo{Status: "MANAGED", Source: "appstore"}
		}
		return UpdaterInfo{}
	})
	if r.Entries[0].Status != "SELF-UPDATING" || r.Entries[0].UpdateSource != "squirrel" {
		t.Fatalf("%+v", r.Entries[0])
	}
	if r.Entries[1].Status != "OUTDATED" || r.Entries[1].UpdateSource != "" {
		t.Fatalf("provider verdict overridden: %+v", r.Entries[1])
	}
	if r.Entries[2].Status != "NO SOURCE" {
		t.Fatalf("%+v", r.Entries[2])
	}
	r = Result{Applications: []Application{{Name: "Game", Platform: "ios", Path: "/Applications/Game.app"}}}
	Reconcile(&r)
	ApplyUpdaters(&r, func(Application) UpdaterInfo { return UpdaterInfo{} })
	if r.Entries[0].Status != "MANAGED" || r.Entries[0].UpdateSource != "appstore" {
		t.Fatalf("iOS wrapper should be App Store managed: %+v", r.Entries[0])
	}
}

func TestSparkleFeedFinderFallback(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "Hidden.app")
	if err := os.MkdirAll(filepath.Join(p, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0"?><plist><dict><key>CFBundleVersion</key><string>100</string></dict></plist>`
	if err := os.WriteFile(filepath.Join(p, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	old := SparkleFeedFinder
	SparkleFeedFinder = func(a Application) string { return "https://example.org/hidden.xml" }
	defer func() { SparkleFeedFinder = old }()
	checks := CheckSparkle(context.Background(), []Application{{Path: p, Build: "100"}}, func(_ context.Context, feed string) ([]byte, error) {
		if feed != "https://example.org/hidden.xml" {
			t.Fatalf("unexpected feed %s", feed)
		}
		return []byte(`<rss xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><sparkle:version>200</sparkle:version></item></channel></rss>`), nil
	})
	if len(checks.Checks) != 1 || checks.Checks[0].Status != "OUTDATED" {
		t.Fatalf("%+v", checks)
	}
}

func TestApplyLookupsPriority(t *testing.T) {
	r := Result{Applications: []Application{{Name: "A", Path: "/Applications/A.app"}, {Name: "B", Path: "/Applications/B.app"}, {Name: "C", Path: "/Applications/C.app"}, {Name: "D", Path: "/Applications/D.app"}}}
	Reconcile(&r)
	r.Entries[2].Status = "OUTDATED"
	r.Entries[3].Status, r.Entries[3].UpdateSource = "MANAGED", "parent"
	ApplyLookups(&r, map[string][]LookupVerdict{
		identityPath("/Applications/A.app"): {{Source: "github-releases", Version: "9.9", Comparison: "newer"}, {Source: "homebrew-cask", Version: "2.0", Comparison: "newer"}, {Source: "itunes", Version: "1.0", Comparison: "same"}},
		identityPath("/Applications/B.app"): {{Source: "homebrew-cask", Version: "3.0", Comparison: "unknown"}},
		identityPath("/Applications/C.app"): {{Source: "homebrew-cask", Version: "5.0", Comparison: "newer"}},
		identityPath("/Applications/D.app"): {{Source: "homebrew-cask", Version: "5.0", Comparison: "newer"}},
	})
	if e := r.Entries[0]; e.Status != "OUTDATED" || e.Available != "2.0" || e.UpdateSource != "homebrew-cask" || len(e.Sources) != 2 {
		t.Fatalf("catalogue should outrank a derived feed: %+v", e)
	}
	if e := r.Entries[1]; e.Status != "NO SOURCE" || !strings.Contains(e.Detail, "homebrew-cask lists 3.0") {
		t.Fatalf("%+v", e)
	}
	if e := r.Entries[2]; e.Available != "" {
		t.Fatalf("provider verdict touched: %+v", e)
	}
	if e := r.Entries[3]; e.Status != "MANAGED" {
		t.Fatalf("helper should be skipped: %+v", e)
	}
}
