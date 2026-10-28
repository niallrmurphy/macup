package inventory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareBuilds(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
		ok   bool
	}{
		{"1.2.10", "1.2.9", 1, true}, {"1.2", "1.2.0", 0, true}, {"2", "10", -1, true}, {"v2", "1", 0, false}, {"1.18446744073709551616", "1", 0, false},
	} {
		got, ok := compareBuilds(tc.a, tc.b)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s / %s = %d %v", tc.a, tc.b, got, ok)
		}
	}
}
func TestSparkleCheckAndApply(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "Example.app")
	if err := os.MkdirAll(filepath.Join(p, "Contents"), 0755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0"?><plist version="1.0"><dict><key>SUFeedURL</key><string>https://example.org/appcast.xml</string></dict></plist>`
	if err := os.WriteFile(filepath.Join(p, "Contents", "Info.plist"), []byte(plist), 0644); err != nil {
		t.Fatal(err)
	}
	app := Application{Name: "Example", Path: p, Build: "100", Version: "1.0"}
	feed := `<rss xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><enclosure sparkle:version="101" sparkle:shortVersionString="1.1"/></item><item><sparkle:channel>beta</sparkle:channel><sparkle:version>999</sparkle:version></item></channel></rss>`
	r := CheckSparkle(context.Background(), []Application{app}, func(_ context.Context, u string) ([]byte, error) {
		if u != "https://example.org/appcast.xml" {
			t.Fatal(u)
		}
		return []byte(feed), nil
	})
	if len(r.Checks) != 1 || r.Checks[0].Status != "OUTDATED" || r.Checks[0].AvailableVersion != "1.1" {
		t.Fatalf("%+v", r)
	}
	result := Result{Applications: []Application{app}, Sparkle: &r}
	Reconcile(&result)
	ApplySparkle(&result)
	if result.Entries[0].Status != "OUTDATED" || result.Entries[0].Available != "1.1" {
		t.Fatal(result.Entries)
	}
}
func TestSparkleRejectsHTTPAndErrors(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "Example.app")
	_ = os.MkdirAll(filepath.Join(p, "Contents"), 0755)
	_ = os.WriteFile(filepath.Join(p, "Contents", "Info.plist"), []byte(`<?xml version="1.0"?><plist><dict><key>SUFeedURL</key><string>http://localhost/feed</string></dict></plist>`), 0644)
	called := false
	res := CheckSparkle(context.Background(), []Application{{Path: p, Build: "1"}}, func(context.Context, string) ([]byte, error) { called = true; return nil, errors.New("no") })
	if called || res.Checks[0].Status != "UNCHECKED" || !strings.Contains(res.Checks[0].Reason, "HTTPS") {
		t.Fatal(res)
	}
}

func TestSparkleProgressReportsStartAndDone(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "Example.app")
	if err := os.MkdirAll(filepath.Join(p, "Contents"), 0755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0"?><plist><dict><key>SUFeedURL</key><string>https://example.org/feed</string></dict></plist>`
	if err := os.WriteFile(filepath.Join(p, "Contents", "Info.plist"), []byte(plist), 0644); err != nil {
		t.Fatal(err)
	}
	var events []SparkleProgress
	result := CheckSparkleWithProgress(context.Background(), []Application{{Name: "Example", Path: p, Build: "1"}},
		func(context.Context, string) ([]byte, error) {
			return []byte(`<rss xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><sparkle:version>2</sparkle:version></item></channel></rss>`), nil
		},
		func(e SparkleProgress) { events = append(events, e) })
	if len(events) != 2 || events[0].Phase != "start" || events[1].Phase != "done" || events[1].Status != "OUTDATED" || events[0].Index != 1 || events[0].Total != 1 {
		t.Fatalf("events=%+v result=%+v", events, result)
	}
}

func TestSparkleMixedFeedDoesNotPoisonEligibleRelease(t *testing.T) {
	// Simulate an unknown host version (as on Linux) so the OS-gated release is indeterminate.
	old := hostVersion
	hostVersion = func(context.Context) string { return "" }
	defer func() { hostVersion = old }()
	root := t.TempDir()
	p := filepath.Join(root, "Mixed.app")
	if err := os.MkdirAll(filepath.Join(p, "Contents"), 0755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0"?><plist><dict><key>SUFeedURL</key><string>https://example.org/feed</string></dict></plist>`
	if err := os.WriteFile(filepath.Join(p, "Contents", "Info.plist"), []byte(plist), 0644); err != nil {
		t.Fatal(err)
	}
	checks := CheckSparkle(context.Background(), []Application{{Path: p, Build: "100"}}, func(context.Context, string) ([]byte, error) {
		return []byte(`<rss xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel>
 <item><sparkle:version>100</sparkle:version></item>
 <item><sparkle:channel>beta</sparkle:channel><sparkle:version>999</sparkle:version></item>
 <item><sparkle:version>900</sparkle:version><sparkle:minimumSystemVersion>999.0</sparkle:minimumSystemVersion></item>
 </channel></rss>`), nil
	})
	// On Linux an OS-gated release cannot be evaluated, so uncertainty remains.
	if len(checks.Checks) != 1 || checks.Checks[0].Status != "UNCHECKED" {
		t.Fatal(checks)
	}
}

func TestSparkleIneligibleReleases(t *testing.T) {
	for _, tc := range []struct {
		min, max, platform, host string
		allowed, known           bool
	}{
		{"12.0", "", "", "14.0", true, true},
		{"15.0", "", "", "14.0", false, true},
		{"", "13.0", "macos", "14.0", false, true},
		{"", "", "windows", "", false, true},
		{"12.0", "", "", "", false, false},
		{"", "", "", "", true, true},
	} {
		a, k := eligibleRelease(tc.min, tc.max, tc.platform, tc.host)
		if a != tc.allowed || k != tc.known {
			t.Errorf("%+v got %v %v", tc, a, k)
		}
	}
}

func TestSparkleDiagnosticsAndPreflightProgress(t *testing.T) {
	root := t.TempDir()
	makeApp := func(name, feed string) Application {
		p := filepath.Join(root, name+".app")
		if err := os.MkdirAll(filepath.Join(p, "Contents"), 0755); err != nil {
			t.Fatal(err)
		}
		plist := `<?xml version="1.0"?><plist><dict><key>SUFeedURL</key><string>` + feed + `</string></dict></plist>`
		if err := os.WriteFile(filepath.Join(p, "Contents", "Info.plist"), []byte(plist), 0644); err != nil {
			t.Fatal(err)
		}
		return Application{Name: name, Path: p, Build: "100"}
	}
	apps := []Application{makeApp("NoHTTPS", "http://example.org/x"), makeApp("Valid", "https://example.org/x")}
	var events []SparkleProgress
	r := CheckSparkleWithDiagnostics(context.Background(), apps, func(context.Context, string) ([]byte, error) {
		return []byte(`<rss xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><sparkle:version>101</sparkle:version></item><item><sparkle:channel>beta</sparkle:channel><sparkle:version>200</sparkle:version></item><item><sparkle:version>nonnumeric</sparkle:version></item></channel></rss>`), nil
	}, func(e SparkleProgress) { events = append(events, e) })
	if len(r.Checks) != 2 || len(events) != 3 || events[0].Phase != "skip" || events[1].Phase != "start" || events[2].Phase != "done" {
		t.Fatalf("checks=%+v events=%+v", r, events)
	}
	d := r.Checks[1].Diagnostics
	if d == nil || d.TotalItems != 3 || d.Comparable != 1 || d.SkippedChannel != 1 || d.UncomparableVersion != 1 {
		t.Fatalf("diagnostics=%+v", d)
	}
	if events[2].Check.Diagnostics == nil || events[2].Check.Status != "OUTDATED" {
		t.Fatalf("done event=%+v", events[2])
	}
}

func TestSparkleFieldCountersAndSanitizedSample(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "Example.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(`<?xml version="1.0"?><plist><dict><key>SUFeedURL</key><string>https://example.org/feed</string></dict></plist>`), 0644); err != nil {
		t.Fatal(err)
	}
	feed := `<rss xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><sparkle:shortVersionString>1.2</sparkle:shortVersionString><enclosure url="https://secret.example/download?token=SENSITIVE" sparkle:version="120" sparkle:shortVersionString="1.2"/></item></channel></rss>`
	dir := filepath.Join(root, "samples")
	res := CheckSparkleWithXMLSamples(context.Background(), []Application{{Name: "Example", Path: app, Build: "100"}}, func(context.Context, string) ([]byte, error) { return []byte(feed), nil }, nil, dir)
	if len(res.Checks) != 1 || res.Checks[0].Status != "OUTDATED" {
		t.Fatal(res)
	}
	d := res.Checks[0].Diagnostics
	if d.ItemShortVersions != 1 || d.EnclosureVersions != 1 || d.EnclosureShortVersions != 1 || d.EnclosureURLs != 1 {
		t.Fatalf("unexpected counts: %+v", d)
	}
	b, err := os.ReadFile(d.XMLSample)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "SENSITIVE") || strings.Contains(string(b), "https://") || !strings.Contains(string(b), "enclosure") {
		t.Fatalf("unsafe/incomplete sample %s", b)
	}
}

func TestSparkleNotFlaggedKeepsSource(t *testing.T) {
	app := Application{Name: "Example", Path: "/Applications/Example.app", Build: "2", Version: "2"}
	r := Result{Applications: []Application{app}, Sparkle: &SparkleResult{Checks: []SparkleCheck{{Path: app.Path, Status: "NOT FLAGGED"}}}}
	Reconcile(&r)
	ApplySparkle(&r)
	if len(r.Entries) != 1 || r.Entries[0].UpdateSource != "sparkle" {
		t.Fatalf("entries=%+v", r.Entries)
	}
}

func TestResolveEnclosureRelativeToFeed(t *testing.T) {
	if got := resolveEnclosure("https://pkgs.tailscale.com/stable/appcast.xml", "Tailscale-1.105.110-macos.zip"); got != "https://pkgs.tailscale.com/stable/Tailscale-1.105.110-macos.zip" {
		t.Fatal(got)
	}
	if got := resolveEnclosure("https://x.example/feed.xml", "https://cdn.example/App.dmg"); got != "https://cdn.example/App.dmg" {
		t.Fatal(got)
	}
	if got := resolveEnclosure("https://x.example/a/feed.xml", "/dl/App.zip"); got != "https://x.example/dl/App.zip" {
		t.Fatal(got)
	}
}
