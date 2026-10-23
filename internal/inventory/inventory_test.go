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
