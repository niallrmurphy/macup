package probe

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/niallrmurphy/macup/internal/inventory"
)

func TestParsePlistScalarsIgnoresNested(t *testing.T) {
	raw := []byte(`<?xml version="1.0"?><plist version="1.0"><dict>
 <key>CFBundleName</key><string>Sample</string>
 <key>LSUIElement</key><true/>
 <key>Nested</key><dict><key>SUFeedURL</key><string>https://inner.example/</string></dict>
 <key>After</key><string>kept</string>
 </dict></plist>`)
	f := parsePlistScalars(raw)
	if f["CFBundleName"] != "Sample" || f["LSUIElement"] != "true" || f["After"] != "kept" {
		t.Fatalf("%v", f)
	}
	if _, ok := f["SUFeedURL"]; ok {
		t.Fatalf("nested key leaked: %v", f)
	}
}

func TestElectronUpdaterCandidates(t *testing.T) {
	got := electronUpdaterCandidates([]byte("provider: generic\nurl: https://updates.example.org/desktop\nupdaterCacheDirName: x\n"))
	if len(got) != 1 || got[0].URL != "https://updates.example.org/desktop/latest-mac.yml" || got[0].Score != 90 {
		t.Fatalf("%+v", got)
	}
	got = electronUpdaterCandidates([]byte("provider: github\nowner: acme\nrepo: widget\n"))
	if len(got) != 1 || got[0].URL != "https://github.com/acme/widget/releases.atom" {
		t.Fatalf("%+v", got)
	}
}

func TestPackageJSONCandidates(t *testing.T) {
	raw := []byte(`{"homepage":"https://example.org","repository":{"url":"git+https://github.com/acme/widget.git"},"build":{"publish":[{"provider":"github","owner":"acme","repo":"widget"}]}}`)
	got := packageJSONCandidates(raw)
	urls := map[string]bool{}
	for _, c := range got {
		urls[c.URL] = true
	}
	for _, want := range []string{"https://example.org", "https://github.com/acme/widget", "https://github.com/acme/widget/releases.atom"} {
		if !urls[want] {
			t.Fatalf("missing %s in %+v", want, got)
		}
	}
	if normaliseRepo("acme/widget") != "https://github.com/acme/widget" || normaliseRepo("git@github.com:a/b.git") != "https://github.com/a/b" || normaliseRepo("ftp://x") != "" {
		t.Fatal("normaliseRepo")
	}
}

func TestForgeFeedsOnlyFromTrustedSources(t *testing.T) {
	r := Report{}
	add(&r, "https://github.com/acme/widget/issues", "Info.plist:VendorURL", 65)
	add(&r, "https://github.com/sponsors/x", "Info.plist:VendorURL", 65)
	add(&r, "https://github.com/dep/lib", "executable", 55)
	add(&r, "https://github.com/dep/other-lib", "binary:libfoo.dylib", 35)
	add(&r, "https://api.github.com/repos/acme/widgetapp/releases/latest", "binary:bundle.js", 80)
	add(&r, "https://gitlab.com/inkscape/inkscape", "Info.plist:CIProjectUrl", 65)
	for _, c := range append([]Candidate{}, r.Candidates...) {
		if forgeTrusted(c, []string{"widgetapp"}) {
			deriveForgeFeeds(&r, c.URL)
		}
	}
	urls := map[string]bool{}
	for _, c := range r.Candidates {
		urls[c.URL] = true
	}
	if !urls["https://github.com/acme/widget/releases.atom"] || !urls["https://gitlab.com/inkscape/inkscape/-/tags?format=atom"] || !urls["https://github.com/acme/widgetapp/releases.atom"] {
		t.Fatalf("%+v", r.Candidates)
	}
	if urls["https://github.com/dep/lib/releases.atom"] || urls["https://github.com/sponsors/x/releases.atom"] || urls["https://github.com/dep/other-lib/releases.atom"] {
		t.Fatalf("untrusted feed derived: %+v", r.Candidates)
	}
}

func TestMozillaUpdateURL(t *testing.T) {
	ini := []byte("[App]\nVendor=Mozilla\nName=Firefox\nVersion=131.0.2\nBuildID=20241008210941\n\n[AppUpdate]\nURL=https://aus5.mozilla.org/update/6/%PRODUCT%/%VERSION%/%BUILD_ID%/%BUILD_TARGET%/%LOCALE%/%CHANNEL%/%OS_VERSION%/%SYSTEM_CAPABILITIES%/%DISTRIBUTION%/%DISTRIBUTION_VERSION%/update.xml\n")
	got := mozillaUpdateURL(ini, "release", "arm64")
	want := "https://aus5.mozilla.org/update/6/Firefox/131.0.2/20241008210941/Darwin_aarch64-gcc3/en-US/release/default/default/default/default/update.xml"
	if got != want {
		t.Fatalf("got %s", got)
	}
	if mozillaUpdateURL([]byte("[App]\nName=X\n"), "", "arm64") != "" {
		t.Fatal("expected empty without template")
	}
	v, ok := mozillaOffered([]byte(`<updates><update type="minor" appVersion="158.0.1" buildID="1"/></updates>`))
	if !ok || v != "158.0.1" {
		t.Fatal(v, ok)
	}
	if _, ok = mozillaOffered([]byte(`<updates></updates>`)); ok {
		t.Fatal("empty response must not offer a version")
	}
}

func TestScanExecutableURLs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bin")
	blob := append([]byte{0, 1, 2}, []byte("junk https://get.example.com/appcast.xml\x00more https://example.com/privacy\x00https://cdn.example.com/%@/update\x00https://example.com/download/v2")...)
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := scanExecutableURLs(path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].URL != "https://get.example.com/appcast.xml" || got[1].URL != "https://example.com/download/v2" {
		t.Fatalf("%+v", got)
	}
	tokens := []string{"example"}
	if sc, ok := rankBundleURL("https://get.example.com/appcast.xml", tokens); !ok || sc != 80 {
		t.Fatal("affine strong", sc, ok)
	}
	if sc, ok := rankBundleURL("https://example.com/privacy", tokens); !ok || sc != 55 {
		t.Fatal("affine weak", sc, ok)
	}
	if _, ok := rankBundleURL("https://nodejs.org/download/release/v14.tar.gz", tokens); ok {
		t.Fatal("dependency noise kept")
	}
	if _, ok := rankBundleURL("https://docs.aws.amazon.com/general/latest/gr/rande.html", tokens); ok {
		t.Fatal("noise host kept")
	}
	if sc, ok := rankBundleURL("https://cdn.vendor.test/autoupdate/table.xml", tokens); !ok || sc != 60 {
		t.Fatal("strong without affinity", sc, ok)
	}
}

func TestAffinityTokens(t *testing.T) {
	r := Report{Application: inventory.Application{Name: "Task Manager TMOG", BundleID: "com.tmog.taskmanager"}, Signer: "David Plummer"}
	tokens := affinityTokens(&r)
	want := map[string]bool{"taskmanagertmog": true, "task": true, "manager": true, "tmog": true, "taskmanager": true, "david": true, "plummer": true}
	for _, tok := range tokens {
		if !want[tok] {
			t.Fatalf("unexpected token %q in %v", tok, tokens)
		}
	}
	if !hasAffinity("https://www.tmog.org/downloads/release.json", tokens) || hasAffinity("https://nodejs.org/download/", tokens) {
		t.Fatal("affinity")
	}
}

func TestGenericInterpreters(t *testing.T) {
	v, rel := jsonVersion([]byte(`{"schemaVersion":1,"platform":"macOS","version":"1.1.0","build":"2026","releaseNotes":"x"}`))
	if v != "1.1.0" || rel != "" {
		t.Fatal(v, rel)
	}
	v, rel = jsonVersion([]byte(`{"html_url":"x","tag_name":"v0.1.0-beta.6","published_at":"2017-01-01T00:00:00Z","assets":[{"name":"DBGlass-0.1.0.dmg"}]}`))
	if v != "v0.1.0-beta.6" || rel != "2017-01-01T00:00:00Z" {
		t.Fatal(v, rel)
	}
	if v, _ = jsonVersion([]byte(`{"name":"not a version"}`)); v != "" {
		t.Fatal(v)
	}
	if v, _ = xmlVersion([]byte(`<updates><update appVersion="158.0.1"/></updates>`)); v != "158.0.1" {
		t.Fatal(v)
	}
	if v, _ = xmlVersion([]byte(`<root><latest>2.4.1</latest></root>`)); v != "2.4.1" {
		t.Fatal(v)
	}
	if v := textVersion([]byte("7.2.22\n")); v != "7.2.22" {
		t.Fatal(v)
	}
	if v := textVersion([]byte("<html>7.2.22</html>")); v != "" {
		t.Fatal(v)
	}
	page := []byte(`<a href="2.1/oscar20_2.1.0-Intel.dmg">x</a> <a href="https://www.sleepfiles.com/OSCAR/2.1/OSCAR-2.1.0-ARM.dmg">y</a> <a href="/OSCAR/1.5/OSCAR-1.5.1-win64.exe">w</a> <a href="/other/Thing-9.9.9.dmg">z</a>`)
	v, link := htmlInstallerVersion(page, "https://www.sleepfiles.com/OSCAR/", []string{"oscar"})
	if v != "2.1.0" || !strings.HasPrefix(link, "https://www.sleepfiles.com/OSCAR/2.1/") {
		t.Fatal(v, link)
	}
	if !directoryLike("https://download.virtualbox.org/virtualbox/") || !directoryLike("https://update.virtualbox.org") || directoryLike("https://x.example/release.json") || directoryLike("https://x.example/q?x=1") || directoryLike("https://github.com/a/b/wiki/Page") {
		t.Fatal("directoryLike")
	}
}

func TestAsarFile(t *testing.T) {
	content := []byte(`{"name":"x","homepage":"https://x.example"}`)
	header := map[string]any{"files": map[string]any{"package.json": map[string]any{"size": len(content), "offset": "0"}}}
	hj, _ := json.Marshal(header)
	// Pickle layout: uint32(4), uint32(header size), uint32(string size), uint32(json length), json, padding to 4.
	pad := (4 - len(hj)%4) % 4
	stringSize := uint32(len(hj) + pad)
	headerSize := stringSize + 8
	var buf []byte
	buf = binary.LittleEndian.AppendUint32(buf, 4)
	buf = binary.LittleEndian.AppendUint32(buf, headerSize)
	buf = binary.LittleEndian.AppendUint32(buf, stringSize+4)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(hj)))
	buf = append(buf, hj...)
	buf = append(buf, make([]byte, pad)...)
	buf = append(buf, make([]byte, 8+int(headerSize)-len(buf))...)
	buf = append(buf, content...)
	path := filepath.Join(t.TempDir(), "app.asar")
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := asarFile(path, "package.json", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("got %q", got)
	}
	if _, err = asarFile(path, "missing", 1<<20); err == nil {
		t.Fatal("expected missing entry error")
	}
}

func TestVersionComparison(t *testing.T) {
	cases := []struct{ installed, remote, want string }{
		{"1.2.3", "1.2.4", "newer"},
		{"1.2.3", "v1.2.3", "same"},
		{"1.10", "1.9", "older"},
		{"2191", "26.1.10", "unknown"},
		{"145.1.87", "1.98.60", "unknown"},
		{"1.6.7", "1.14.5", "newer"},
		{"abc", "1.0", "unknown"},
		{"0.44.11", "3.27.22", "newer"},
	}
	for _, c := range cases {
		if got := comparison(c.installed, c.remote); got != c.want {
			t.Errorf("%s vs %s: got %s want %s", c.installed, c.remote, got, c.want)
		}
	}
}

func TestQuarantineMatching(t *testing.T) {
	rows := []quarantineRow{
		{ID: "AAA", TS: 679140556, Agent: "Firefox", URL: "https://vendor.example/OSCAR/OSCAR-1.4.0.dmg", Origin: "https://vendor.example/OSCAR/"},
		{ID: "BBB", TS: 1, Agent: "Chrome", URL: "https://other.example/archive.zip"},
		{ID: "CCC", TS: 2, Agent: "Chrome", URL: "https://other.example/Widget%20Pro-2.zip"},
	}
	got := matchQuarantine(rows, "aaa", "Something Else", "Something Else.app")
	if len(got) != 1 || got[0].Source != "quarantine-db:event" || got[0].Time != "2022-07-10T10:09:16Z" || got[0].Referrer == "" {
		t.Fatalf("%+v", got)
	}
	got = matchQuarantine(rows, "", "Widget Pro", "Widget Pro.app")
	if len(got) != 1 || got[0].Source != "quarantine-db:name" || got[0].URL != rows[2].URL {
		t.Fatalf("%+v", got)
	}
	if got = matchQuarantine(rows, "", "Zip", "Zip.app"); len(got) != 0 {
		t.Fatalf("short names must not match: %+v", got)
	}
	if q := quotedStrings("(\n    \"https://a.example/x.dmg\",\n    \"https://a.example/\"\n)"); len(q) != 2 || q[0] != "https://a.example/x.dmg" {
		t.Fatalf("%v", q)
	}
}

func TestVendorLaunchAgents(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"com.google.keystone.agent.plist", "com.google.other.plist", "com.apple.updater.plist", "us.zoom.updater.plist", "com.microsoft.update.agent.plist", "com.microsoft.OneDriveStandaloneUpdater.plist"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := launchDirs
	launchDirs = func() []string { return []string{dir} }
	defer func() { launchDirs = old }()
	if got := vendorLaunchAgents("com.google.Chrome"); len(got) != 1 || got[0] != "com.google.keystone.agent" {
		t.Fatalf("%v", got)
	}
	if got := vendorLaunchAgents("com.apple.Safari"); len(got) != 0 {
		t.Fatalf("apple ignored: %v", got)
	}
	if got := vendorLaunchAgents("us.zoom.xos"); len(got) != 1 {
		t.Fatalf("%v", got)
	}
	if got := vendorLaunchAgents("com.microsoft.OneDrive"); len(got) != 2 || got[0] != "com.microsoft.OneDriveStandaloneUpdater" {
		t.Fatalf("product-named agent should sort first: %v", got)
	}
	if got := vendorLaunchAgents("com.microsoft.Word"); len(got) != 2 || got[0] != "com.microsoft.update.agent" {
		t.Fatalf("vendor-wide agent should outrank another product's: %v", got)
	}
}

func TestCodeSignerParsing(t *testing.T) {
	old := combinedOutput
	defer func() { combinedOutput = old }()
	combinedOutput = func(context.Context, string, ...string) []byte {
		return []byte("Identifier=com.cocoatech.PathFinder\nAuthority=Developer ID Application: Distant Future Corp (5GM4WX237V)\nAuthority=Developer ID Certification Authority\nTeamIdentifier=5GM4WX237V\n")
	}
	s, team := codeSigner(context.Background(), "/x")
	if s != "Distant Future Corp" || team != "5GM4WX237V" {
		t.Fatal(s, team)
	}
	combinedOutput = func(context.Context, string, ...string) []byte {
		return []byte("Authority=Apple Mac OS Application Signing\nTeamIdentifier=not set\n")
	}
	if s, team = codeSigner(context.Background(), "/x"); s != "Apple Mac OS Application Signing" || team != "" {
		t.Fatal(s, team)
	}
}

func TestClassify(t *testing.T) {
	app := func(name, bid, path string) Report {
		return Report{Application: inventory.Application{Name: name, BundleID: bid, Path: path}}
	}
	r := app("Pages", "com.apple.iWork.Pages", "/Applications/Pages.app")
	r.addFamily("mas-receipt")
	classify(&r, nil)
	if r.Management == nil || r.Management.Kind != "appstore" {
		t.Fatalf("%+v", r.Management)
	}
	r = app("Google Docs", "com.google.drivefs.shortcuts.docs", "/Applications/Google Docs.app")
	classify(&r, nil)
	if r.Management == nil || r.Management.Kind != "pwa" {
		t.Fatalf("%+v", r.Management)
	}
	r = app("Creative Cloud Uninstaller", "com.adobe.x", "/Applications/Utilities/Adobe/Creative Cloud Uninstaller.app")
	classify(&r, nil)
	if r.Management == nil || r.Management.Kind != "helper" {
		t.Fatalf("%+v", r.Management)
	}
	r = app("Word", "com.microsoft.Word", "/Applications/Microsoft Word.app")
	r.addFamily("launch-agent:com.microsoft.update.agent")
	classify(&r, nil)
	if r.Management == nil || r.Management.Kind != "vendor" {
		t.Fatalf("%+v", r.Management)
	}
	r = app("Code", "com.microsoft.VSCode", "/Applications/Visual Studio Code.app")
	r.addFamily("squirrel")
	r.addFamily("launch-agent:com.microsoft.update.agent")
	classify(&r, nil)
	if r.Management == nil || r.Management.Kind != "self" {
		t.Fatalf("embedded updater should win over a vendor-wide agent: %+v", r.Management)
	}
	r = app("Editor", "org.example.editor", "/Applications/Editor.app")
	r.addFamily("sparkle")
	classify(&r, nil)
	if r.Management == nil || r.Management.Kind != "self" {
		t.Fatalf("%+v", r.Management)
	}
	r = app("Mystery", "org.example.mystery", "/Applications/Mystery.app")
	classify(&r, nil)
	if r.Management != nil {
		t.Fatalf("%+v", r.Management)
	}
}

func TestParseITunes(t *testing.T) {
	l, err := parseITunes([]byte(`{"resultCount":1,"results":[{"trackName":"Magnet","version":"3.0.7","currentVersionReleaseDate":"2025-04-12T02:54:13Z","sellerUrl":"https://magnet.crowdcafe.com","trackViewUrl":"https://apps.apple.com/x"}]}`), "3.0.6")
	if err != nil || l == nil || l.Version != "3.0.7" || l.Comparison != "newer" || l.Note != "https://magnet.crowdcafe.com" {
		t.Fatalf("%+v %v", l, err)
	}
	if l, err = parseITunes([]byte(`{"resultCount":0,"results":[]}`), "1"); err != nil || l != nil {
		t.Fatalf("%+v %v", l, err)
	}
}

func TestCaskIndexMatching(t *testing.T) {
	raw := []byte(`[
	 {"token":"path-finder","name":["Path Finder"],"version":"26.1.10","url":"https://get.cocoatech.com/PathFinder.dmg","artifacts":[{"app":["Path Finder.app"]},{"zap":[{"trash":["x"]}]}]},
	 {"token":"wondershare","version":"18.0.0","artifacts":[{"app":["Wondershare.app"]},{"uninstall":[{"quit":["com.wondershare.x","com.google.Chrome"]}]}]},
	 {"token":"zoom","version":"6.0.0","url":"https://zoom.us/client/latest/Zoom.pkg","artifacts":[{"pkg":["Zoom.pkg"]},{"uninstall":[{"quit":"us.zoom.xos"}]}]},
	 {"token":"dropbox","version":"274.4","artifacts":[{"app":["Dropbox.app"]},{"uninstall":[{"quit":"com.getdropbox.dropbox"}]}]},
	 {"token":"dropbox-beta","version":"275.1","artifacts":[{"app":["Dropbox.app"]},{"uninstall":[{"quit":"com.getdropbox.dropbox.beta"}]}]}
	]`)
	idx, err := buildCaskIndex(raw)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := idx.match(inventory.Application{BundleID: "com.cocoatech.PathFinder", Path: "/Applications/Path Finder.app"})
	if c == nil || c.Token != "path-finder" {
		t.Fatalf("%+v", c)
	}
	if c, _ = idx.match(inventory.Application{BundleID: "com.google.Chrome", Path: "/Applications/Google Chrome.app"}); c != nil {
		t.Fatalf("Chrome must not match a cask that merely quits it: %+v", c)
	}
	if c, _ = idx.match(inventory.Application{BundleID: "us.zoom.xos", Path: "/Applications/zoom.us.app"}); c == nil || c.Token != "zoom" {
		t.Fatalf("pkg cask by bundle: %+v", c)
	}
	if c, _ = idx.match(inventory.Application{BundleID: "com.getdropbox.dropbox", Path: "/Applications/Dropbox.app"}); c == nil || c.Token != "dropbox" {
		t.Fatalf("bundle should break app-name tie: %+v", c)
	}
}

func TestFeedParsers(t *testing.T) {
	atom := []byte(`<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom">
	<entry><id>tag:github.com,2008:Repository/1/v2.0.0-beta.1</id><updated>2026-01-02T00:00:00Z</updated><link rel="alternate" href="https://github.com/a/b/releases/tag/v2.0.0-beta.1"/><title>v2.0.0-beta.1</title></entry>
	<entry><id>tag:github.com,2008:Repository/1/v1.9.0</id><updated>2025-12-01T00:00:00Z</updated><link rel="alternate" href="https://github.com/a/b/releases/tag/v1.9.0"/><title>v1.9.0</title></entry></feed>`)
	title, updated, link, err := latestAtomEntry(atom)
	if err != nil || title != "v1.9.0" || updated != "2025-12-01T00:00:00Z" || link != "https://github.com/a/b/releases/tag/v1.9.0" {
		t.Fatal(title, updated, link, err)
	}
	free := []byte(`<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>tag:github.com,2008:Repository/1/4.0.4</id><link rel="alternate" href="https://github.com/a/b/releases/tag/4.0.4"/><title>4.0.4 release</title><updated>u</updated></entry></feed>`)
	if title, _, _, _ = latestAtomEntry(free); title != "4.0.4" {
		t.Fatal(title)
	}
	gl := []byte(`<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>x</id><title>INKSCAPE_1_4_4</title><updated>u</updated></entry></feed>`)
	if title, _, _, _ = latestAtomEntry(gl); title != "INKSCAPE.1.4.4" && title != "INKSCAPE_1.4.4" {
		t.Fatal(title)
	}
	if comparison("1.4.4", title) != "same" {
		t.Fatal(comparison("1.4.4", title))
	}
	rss := []byte(`<rss><channel><item><title><![CDATA[/README.md]]></title></item><item><title><![CDATA[/x/3.1.0/App-3.1.0-win64.msi]]></title></item><item><title><![CDATA[/x/3.1.0/App-3.1.0.dmg]]></title><link>https://sf.example/d</link><pubDate>Mon</pubDate></item></channel></rss>`)
	v, _, l, err := latestSourceForgeFile(rss)
	if err != nil || v != "3.1.0" || l != "https://sf.example/d" {
		t.Fatal(v, l, err)
	}
	v, rel := electronLatest([]byte("version: 8.32.0\nfiles:\n  - url: x\nreleaseDate: '2026-10-20T17:43:00.437Z'\n"))
	if v != "8.32.0" || rel != "2026-10-20T17:43:00.437Z" {
		t.Fatal(v, rel)
	}
	appcast := []byte(`<rss xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel>
	<item><title>Version 1.2</title><pubDate>a</pubDate><enclosure url="x" sparkle:version="120" sparkle:shortVersionString="1.2"/></item>
	<item><title>Version 1.10</title><pubDate>b</pubDate><sparkle:shortVersionString>1.10</sparkle:shortVersionString><enclosure url="y"/></item>
	</channel></rss>`)
	if v, rel = latestAppcastItem(appcast); v != "1.10" || rel != "b" {
		t.Fatal(v, rel)
	}
}

func TestChooseAppcastPrefersArchitecture(t *testing.T) {
	cs := []Candidate{
		{URL: "https://update.example.org/vlc/sparkle/vlc-intel64.xml", Source: "binary:x.dylib", Score: 80},
		{URL: "https://update.example.org/vlc/sparkle/vlc-arm64.xml", Source: "binary:x.dylib", Score: 80},
		{URL: "https://example.org/privacy", Source: "executable", Score: 55},
	}
	got := chooseAppcast(cs)
	if runtime.GOARCH == "arm64" && got != "https://update.example.org/vlc/sparkle/vlc-arm64.xml" {
		t.Fatal(got)
	}
	if runtime.GOARCH == "amd64" && got != "https://update.example.org/vlc/sparkle/vlc-intel64.xml" {
		t.Fatal(got)
	}
	if chooseAppcast([]Candidate{{URL: "https://example.org/appcast.xml", Source: "defaults:SUFeedURL", Score: 90}}) != "https://example.org/appcast.xml" {
		t.Fatal("defaults feed")
	}
	if chooseAppcast([]Candidate{{URL: "https://example.org/releases", Source: "executable", Score: 80}}) != "" {
		t.Fatal("non-appcast URL accepted")
	}
}
