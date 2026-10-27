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
