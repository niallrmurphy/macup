package probe

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverFromPlist(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Sample.app")
	if err := os.MkdirAll(filepath.Join(dir, "Contents"), 0755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0"?><plist version="1.0"><dict>
 <key>CFBundleName</key><string>Sample</string>
 <key>CFBundleIdentifier</key><string>org.example.sample</string>
 <key>CFBundleShortVersionString</key><string>1.2.3</string>
 <key>SUFeedURL</key><string>https://updates.example.org/appcast.xml</string>
 <key>VendorURL</key><string>https://example.org/releases</string>
 </dict></plist>`
	if err := os.WriteFile(filepath.Join(dir, "Contents", "Info.plist"), []byte(plist), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Application.Version != "1.2.3" || len(r.Candidates) != 2 {
		t.Fatalf("%+v", r)
	}
	if r.Candidates[0].Score != 90 {
		t.Fatalf("not prioritised: %+v", r.Candidates)
	}
}
func TestRejectUnsafeURLs(t *testing.T) {
	for _, s := range []string{"http://example.com/", "https://127.0.0.1/private", "https://user:pass@example.org/"} {
		r := Report{}
		add(&r, s, "test", 10)
		if strings.HasPrefix(s, "https://127") { // Public-address checks are performed at fetch time.
			continue
		}
		if len(r.Candidates) != 0 {
			t.Fatalf("unsafe candidate %s", s)
		}
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.1.1", "::1"} {
		if publicIP(net.ParseIP(ip)) {
			t.Fatalf("accepted %s", ip)
		}
	}
}
func TestHTMLSnippets(t *testing.T) {
	text := htmlText(`<html><script>latest version 9.9</script><p>Version 2.1 released</p></html>`)
	if strings.Contains(text, "9.9") || !strings.Contains(text, "Version 2.1") {
		t.Fatal(text)
	}
}

func TestSearchResultsOnlyAddHTTPSCandidates(t *testing.T) {
	r := Report{Candidates: []Candidate{}}
	body := []byte(`{"web":{"results":[{"url":"https://vendor.example/releases","title":"Release Notes"},{"url":"http://unsafe.example/","title":"Download"},{"url":"https://vendor.example/releases","title":"Duplicate"}]}}`)
	if err := addBraveResults(&r, body); err != nil {
		t.Fatal(err)
	}
	if len(r.Candidates) != 1 || r.Candidates[0].Source != "search:brave" {
		t.Fatalf("%+v", r.Candidates)
	}
}
func TestSearchResultsInvalidJSON(t *testing.T) {
	r := Report{}
	if err := addBraveResults(&r, []byte("not json")); err == nil {
		t.Fatal("expected error")
	}
}
