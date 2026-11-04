package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niallrmurphy/macup/internal/inventory"
)

func TestPlanRoutes(t *testing.T) {
	r := inventory.Result{
		Entries: []inventory.Entry{
			{Name: "node", Status: "OUTDATED", Sources: []string{"brew-formula"}, Installed: "1", Available: "2"},
			{Name: "Zed", Status: "OUTDATED", Sources: []string{"apps", "brew-cask"}, Package: "zed", Path: "/Applications/Zed.app"},
			{Name: "Banktivity", Status: "OUTDATED", Sources: []string{"apps", "appstore"}, Detail: "id:1480779512"},
			{Name: "Microsoft Outlook", Status: "OUTDATED", Sources: []string{"apps", "microsoft"}, UpdateSource: "microsoft-autoupdate"},
			{Name: "Arc", Status: "OUTDATED", Sources: []string{"apps", "sparkle"}, UpdateSource: "sparkle", Path: "/Applications/NotInstalled-Test.app"},
			{Name: "Lightroom", Status: "OUTDATED", Sources: []string{"apps", "adobe"}, UpdateSource: "adobe-catalog"},
			{Name: "calibre", Status: "OTHER CHANNEL", Sources: []string{"apps", "lookup"}, UpdateSource: "homebrew-cask", Package: "calibre", Path: "/Applications/calibre.app"},
			{Name: "Zotero", Status: "OTHER CHANNEL", Sources: []string{"apps", "lookup"}, UpdateSource: "homebrew-cask", Package: "zotero", Path: "/Users/me/Applications/Zotero.app"},
			{Name: "Kobo", Status: "OUTDATED", Sources: []string{"apps", "lookup"}, UpdateSource: "homebrew-cask", Path: "/Applications/Kobo.app"},
			{Name: "Magnet", Status: "OTHER CHANNEL", Sources: []string{"apps", "lookup"}, UpdateSource: "itunes", Path: "/Applications/Magnet.app", Detail: "id:441258766 in the App Store; this copy did not come from there"},
			{Name: "Fine", Status: "CURRENT", Sources: []string{"apps"}},
		},
		Microsoft: &inventory.MicrosoftResult{Updates: []inventory.MicrosoftUpdate{{ID: "OPIM2019", Name: "Microsoft Outlook", AvailableVersion: "16.114.2"}}},
		Sparkle:   &inventory.SparkleResult{Checks: []inventory.SparkleCheck{{Path: "/Applications/NotInstalled-Test.app", Status: "OUTDATED", EnclosureURL: "https://releases.arc.net/Arc.zip", EdSignature: "sig", EnclosureLength: 10}}},
	}
	plan := Plan(r)
	if len(plan) != 10 {
		t.Fatalf("%d actions", len(plan))
	}
	byName := map[string]Action{}
	for _, a := range plan {
		byName[a.Name] = a
	}
	if a := byName["node"]; a.Route != RouteBrew || strings.Join(a.Command, " ") != "brew upgrade --formula node" {
		t.Fatalf("%+v", a)
	}
	if a := byName["Zed"]; strings.Join(a.Command, " ") != "brew upgrade --cask zed" {
		t.Fatalf("%+v", a)
	}
	if a := byName["Banktivity"]; a.Route != RouteMas || strings.Join(a.Command, " ") != "mas upgrade 1480779512" {
		t.Fatalf("%+v", a)
	}
	if a := byName["Microsoft Outlook"]; a.Route != RouteMAU || a.Command[len(a.Command)-1] != "OPIM2019" {
		t.Fatalf("%+v", a)
	}
	if a := byName["Arc"]; a.Route != RouteSparkle || a.URL != "https://releases.arc.net/Arc.zip" || a.Length != 10 || !strings.Contains(a.Reason, "SUPublicEDKey") {
		t.Fatalf("missing public key must be flagged: %+v", a)
	}
	if a := byName["Lightroom"]; a.Route != RouteAdvice {
		t.Fatalf("%+v", a)
	}
	if a := byName["calibre"]; a.Route != RouteAdopt || strings.Join(a.Command, " ") != "brew install --cask --force calibre" || a.Reason == "" {
		t.Fatalf("%+v", a)
	}
	if a := byName["Zotero"]; a.Route != RouteAdopt || strings.Join(a.Command, " ") != "brew install --cask --appdir=/Users/me/Applications --force zotero" {
		t.Fatalf("app outside /Applications must be replaced in place: %+v", a)
	}
	if a := byName["Kobo"]; a.Route != RouteAdvice || len(a.Command) != 0 || !strings.Contains(a.Reason, "no single cask ships") {
		t.Fatalf("no token, no adoption: %+v", a)
	}
	if a := byName["Magnet"]; a.Route != RouteAdvice || len(a.Command) != 0 || a.URL != "https://apps.apple.com/app/id441258766" || !strings.Contains(a.Reason, "sandboxed App Store build") || !strings.HasSuffix(a.Reason, a.URL) {
		t.Fatalf("App Store switch is explained, never run: %+v", a)
	}
	if plan[0].Route != RouteBrew || plan[len(plan)-1].Route != RouteAdvice || routeRank(RouteAdopt) >= routeRank(RouteAdvice) || routeRank(RouteAdopt) <= routeRank(RouteSparkle) {
		t.Fatalf("plan should be ordered by route, adoptions just before manual: %+v", plan)
	}
	if f := Filter(plan, []string{"outlook", "ZED"}); len(f) != 2 {
		t.Fatalf("%+v", f)
	}
	var b bytes.Buffer
	if err := PrintPlan(&b, plan); err != nil || !strings.Contains(b.String(), "10 actions: 2 brew, 1 mas, 1 msupdate, 0 Apple, 1 sparkle downloads, 2 cask adoptions, 3 manual") {
		t.Fatal(b.String(), err)
	}
	if !strings.Contains(b.String(), "brew install --cask --force calibre [replaces the app; runs only with --adopt]") {
		t.Fatal(b.String())
	}
}

func TestExecuteAdoptNeedsFlag(t *testing.T) {
	plan := []Action{
		{Name: "d", Route: RouteAdopt, Command: []string{"brew-does-not-exist", "install", "--cask", "--force", "d"}},
		{Name: "e", Route: RouteAdopt, Command: []string{"brew-does-not-exist", "install", "--cask", "--appdir=/Users/me/Applications", "--force", "e"}},
		{Name: "f", Route: RouteAdopt, Command: []string{"brew-does-not-exist", "install", "--cask", "--force", "f"}},
	}
	var out, errb bytes.Buffer
	outcomes := Execute(context.Background(), plan, Options{Out: &out, Err: &errb})
	for _, o := range outcomes {
		if o.Result != "skipped" {
			t.Fatalf("adoption must not run without --adopt: %+v", o)
		}
	}
	if !strings.Contains(out.String(), "skip   d: brew-does-not-exist install --cask --force d (re-run with --adopt") {
		t.Fatal(out.String())
	}
	s := Summarise(outcomes, inventory.Result{}, nil, nil)
	if strings.Join(s.Skipped, ",") != "d (re-run with --adopt),e (re-run with --adopt),f (re-run with --adopt)" {
		t.Fatalf("%+v", s.Skipped)
	}

	out.Reset()
	outcomes = Execute(context.Background(), plan, Options{Adopt: true, Out: &out, Err: &errb})
	if !strings.Contains(out.String(), "run    2 Homebrew cask adoptions: brew-does-not-exist install --cask --force d f") || !strings.Contains(out.String(), "--appdir=/Users/me/Applications --force e\n") {
		t.Fatalf("adoptions with the same prefix batch, different appdirs do not:\n%s", out.String())
	}
	for _, o := range outcomes {
		if o.Result != "failed" {
			t.Fatalf("missing brew should fail every adoption: %+v", o)
		}
	}
	after := inventory.Result{}
	if s := Summarise(outcomes, after, map[string]string{}, map[string]string{"d": "1.0"}); len(s.Dependencies) != 0 {
		t.Fatalf("an adopted cask is planned, not a dependency: %+v", s.Dependencies)
	}
}

func TestVerifyEdDSA(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("a disk image")
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data))
	key := base64.StdEncoding.EncodeToString(pub)
	if err := verifyEdDSA(data, sig, key); err != nil {
		t.Fatal(err)
	}
	if err := verifyEdDSA([]byte("tampered"), sig, key); err == nil {
		t.Fatal("tampered data verified")
	}
	if err := verifyEdDSA(data, sig, base64.StdEncoding.EncodeToString(pub[:16])); err == nil {
		t.Fatal("short key accepted")
	}
	if err := verifyEdDSA(data, "not base64!", key); err == nil {
		t.Fatal("bad signature encoding accepted")
	}
}

func TestExecuteSkipsUnverifiableWithoutFlag(t *testing.T) {
	var out, errb bytes.Buffer
	plan := []Action{{Name: "X", Route: RouteSparkle, URL: "https://example.org/x.dmg", Reason: "enclosure cannot be signature-verified"}}
	outcomes := Execute(context.Background(), plan, Options{FetchOnly: true, Out: &out, Err: &errb})
	if len(outcomes) != 1 || outcomes[0].Result != "skipped" || !strings.Contains(out.String(), "skip   X") {
		t.Fatal(outcomes, out.String())
	}
}

func TestSummariseDistinguishesStagedFromInstalled(t *testing.T) {
	outcomes := []Outcome{
		{Action: Action{Name: "iTerm2", Route: RouteSparkle, Available: "3.7.6"}, Result: "opened"},
		{Action: Action{Name: "BetterDisplay", Route: RouteSparkle, Available: "3.9"}, Result: "opened"},
		{Action: Action{Name: "node", Route: RouteBrew, Command: []string{"brew", "upgrade", "--formula", "node"}}, Result: "ran"},
		{Action: Action{Name: "Mirage", Route: RouteSparkle}, Result: "failed", Err: errors.New("signature does not match")},
		{Action: Action{Name: "Kobo", Route: RouteAdvice}, Result: "skipped"},
	}
	after := inventory.Result{Entries: []inventory.Entry{
		{Name: "iTerm2", Status: "OUTDATED", Available: "3.7.6"},
		{Name: "BetterDisplay", Status: "CURRENT"},
		{Name: "node", Status: "CURRENT"},
		{Name: "ai-pim-utils", Status: "OUTDATED", Available: "2.0"},
	}}
	before := map[string]string{"node": "26.12.1", "openssl@3": "3.6.3", "gone": "1"}
	afterBrew := map[string]string{"node": "26.12.1_1", "openssl@3": "3.5.9", "python@3.15": "3.15.2"}
	s := Summarise(outcomes, after, before, afterBrew)
	if strings.Join(s.Updated, ",") != "BetterDisplay,node" {
		t.Fatalf("updated: %v", s.Updated)
	}
	if strings.Join(s.Staged, ",") != "iTerm2 3.7.6" {
		t.Fatalf("staged: %v", s.Staged)
	}
	if len(s.Failed) != 1 || !strings.Contains(s.Failed[0], "Mirage") || strings.Join(s.Skipped, ",") != "Kobo" {
		t.Fatalf("%+v", s)
	}
	if strings.Join(s.NewlyOut, ",") != "ai-pim-utils 2.0" {
		t.Fatalf("newly: %v", s.NewlyOut)
	}
	if strings.Join(s.Dependencies, ",") != "gone (removed),openssl@3 3.6.3 -> 3.5.9,python@3.15 3.15.2 (new)" {
		t.Fatalf("deps: %v", s.Dependencies)
	}
	var b bytes.Buffer
	s.Print(&b)
	if !strings.Contains(b.String(), "not yet installed") {
		t.Fatal(b.String())
	}
}

func TestExecuteBatchesHomebrew(t *testing.T) {
	var out, errb bytes.Buffer
	plan := []Action{
		{Name: "a", Route: RouteBrew, Command: []string{"brew-does-not-exist", "upgrade", "--formula", "a"}},
		{Name: "b", Route: RouteBrew, Command: []string{"brew-does-not-exist", "upgrade", "--formula", "b"}},
		{Name: "c", Route: RouteBrew, Command: []string{"brew-does-not-exist", "upgrade", "--cask", "c"}},
	}
	outcomes := Execute(context.Background(), plan, Options{Out: &out, Err: &errb})
	if len(outcomes) != 3 || !strings.Contains(out.String(), "upgrade --formula a b") || !strings.Contains(out.String(), "upgrade --cask c") {
		t.Fatal(out.String())
	}
	for _, o := range outcomes {
		if o.Result != "failed" {
			t.Fatalf("missing brew should fail every batched action: %+v", o)
		}
	}
}

func TestNamesApp(t *testing.T) {
	if !namesApp("Mirage%20Host-1.3.1.dmg", "Mirage Host") || namesApp("Mirage%20Beacon-1.3.1-1020181.dmg", "Mirage Host") || !namesApp("iTerm2-3_7_6.zip", "iTerm2") {
		t.Fatal("namesApp")
	}
}

func TestPlanAppleRoute(t *testing.T) {
	r := inventory.Result{
		Entries: []inventory.Entry{
			{Name: "Command Line Tools for Xcode", Status: "OUTDATED", Available: "16.2", Sources: []string{"softwareupdate"}, UpdateSource: "softwareupdate", Detail: "label:Command Line Tools for Xcode-16.2"},
			{Name: "macOS Sequoia 15.2", Status: "OUTDATED", Available: "15.2", Sources: []string{"softwareupdate"}, UpdateSource: "softwareupdate", Detail: "label:macOS Sequoia 15.2-24C101"},
			{Name: "Mystery", Status: "OUTDATED", Sources: []string{"softwareupdate"}, UpdateSource: "softwareupdate", Detail: "label:gone"},
		},
		Apple: &inventory.AppleResult{Updates: []inventory.AppleUpdate{
			{Label: "Command Line Tools for Xcode-16.2", Title: "Command Line Tools for Xcode"},
			{Label: "macOS Sequoia 15.2-24C101", Title: "macOS Sequoia 15.2", Restart: true},
		}},
	}
	plan := Plan(r)
	byName := map[string]Action{}
	for _, a := range plan {
		byName[a.Name] = a
	}
	clt := byName["Command Line Tools for Xcode"]
	if clt.Route != RouteApple || clt.Restart || strings.Join(clt.Command, "|") != "sudo|softwareupdate|--install|Command Line Tools for Xcode-16.2" {
		t.Fatalf("%+v", clt)
	}
	if a := byName["macOS Sequoia 15.2"]; a.Route != RouteApple || !a.Restart {
		t.Fatalf("%+v", a)
	}
	if a := byName["Mystery"]; a.Route != RouteAdvice || len(a.Command) != 0 {
		t.Fatalf("%+v", a)
	}
	if routeRank(RouteApple) <= routeRank(RouteMAU) || routeRank(RouteApple) >= routeRank(RouteSparkle) {
		t.Fatal("Apple should rank between msupdate and sparkle-fetch")
	}
	var b bytes.Buffer
	if err := PrintPlan(&b, plan); err != nil || !strings.Contains(b.String(), "[needs a restart; runs only with --apple-restart]") || !strings.Contains(b.String(), "2 Apple") {
		t.Fatal(b.String(), err)
	}
}

func TestExecuteAppleNeedsRestartFlagAndBatches(t *testing.T) {
	var out, errb bytes.Buffer
	plan := []Action{
		{Name: "CLT", Route: RouteApple, Command: []string{"sudo-does-not-exist", "softwareupdate", "--install", "CLT-16.2"}},
		{Name: "Safari", Route: RouteApple, Command: []string{"sudo-does-not-exist", "softwareupdate", "--install", "Safari-18.2"}},
		{Name: "macOS", Route: RouteApple, Restart: true, Command: []string{"sudo-does-not-exist", "softwareupdate", "--install", "macOS-15.2"}},
		{Name: "empty", Route: RouteApple},
	}
	outcomes := Execute(context.Background(), plan, Options{Out: &out, Err: &errb})
	res := map[string]string{}
	for _, o := range outcomes {
		res[o.Action.Name] = o.Result
	}
	if res["macOS"] != "skipped" || res["CLT"] != "failed" || res["Safari"] != "failed" || res["empty"] != "failed" {
		t.Fatalf("%v\n%s", res, out.String())
	}
	if !strings.Contains(out.String(), "--install CLT-16.2 Safari-18.2") || !strings.Contains(out.String(), "2 Apple updates") || strings.Contains(out.String(), "run    1 Apple") {
		t.Fatal(out.String())
	}
	if !strings.Contains(out.String(), "--apple-restart") {
		t.Fatal(out.String())
	}
	out.Reset()
	outcomes = Execute(context.Background(), plan[:3], Options{Out: &out, Err: &errb, AppleRestart: true})
	for _, o := range outcomes {
		if o.Result == "skipped" {
			t.Fatalf("--apple-restart should run the restart update too: %+v", o)
		}
	}
	if !strings.Contains(out.String(), "--install CLT-16.2 Safari-18.2 macOS-15.2") {
		t.Fatal(out.String())
	}
}

func TestFetchSparkleReusesStagedEnclosure(t *testing.T) {
	root := t.TempDir()
	stagingDir = func() string { return root }
	t.Cleanup(func() { stagingDir = StagingDir })

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("a disk image")
	a := Action{
		Name: "Thing", Available: "2.0", Route: RouteSparkle,
		// Unreachable on purpose: a reuse must not touch the network.
		URL:       "https://127.0.0.1:1/Thing.dmg",
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data)),
		PublicKey: base64.StdEncoding.EncodeToString(pub),
		Length:    int64(len(data)),
	}
	dir := filepath.Join(root, "Thing-2.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "Thing.dmg")
	if err := os.WriteFile(staged, data, 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	outcomes := Execute(context.Background(), []Action{a}, Options{FetchOnly: true, Out: &out, Err: &errb})
	if len(outcomes) != 1 || outcomes[0].Result != "staged" || outcomes[0].Err != nil {
		t.Fatal(outcomes, out.String())
	}
	if !strings.Contains(out.String(), "reuse  Thing: "+staged) || strings.Contains(out.String(), "fetch  Thing") {
		t.Fatalf("expected reuse without fetch, got:\n%s", out.String())
	}
	if raw, err := os.ReadFile(filepath.Join(dir, manifestName)); err != nil || !strings.Contains(string(raw), `"available":"2.0"`) {
		t.Fatalf("manifest not written on reuse: %v %s", err, raw)
	}

	// A staged file that no longer verifies is discarded and fetched afresh;
	// here the fetch fails because nothing listens, which is the point.
	if err := os.WriteFile(staged, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.Length = int64(len("tampered"))
	out.Reset()
	outcomes = Execute(context.Background(), []Action{a}, Options{FetchOnly: true, Out: &out, Err: &errb})
	if len(outcomes) != 1 || outcomes[0].Result != "failed" {
		t.Fatal(outcomes, out.String())
	}
	if !strings.Contains(out.String(), "fetch  Thing") || strings.Contains(out.String(), "reuse  Thing") {
		t.Fatalf("expected a fetch after discarding the stale file, got:\n%s", out.String())
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("stale staged file was not removed: %v", err)
	}
}

func TestCleanStagingRetiresOnlyWhatCannotBeReused(t *testing.T) {
	root := t.TempDir()
	stagingDir = func() string { return root }
	t.Cleanup(func() { stagingDir = StagingDir })
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	stage := func(name, version string, withManifest bool, age time.Duration) string {
		dir := filepath.Join(root, unsafeName.ReplaceAllString(name+"-"+version, "_"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "x.dmg"), []byte("1234567890"), 0o644); err != nil {
			t.Fatal(err)
		}
		if withManifest {
			writeManifest(dir, Action{Name: name, Available: version, URL: "https://example.org/x.dmg"})
		}
		if err := os.Chtimes(dir, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	installed := stage("Done", "2.0", true, time.Hour)
	pending := stage("Pending", "1.5", true, time.Hour)
	superseded := stage("Moved", "1.0", true, time.Hour)
	gone := stage("Gone", "1.0", true, time.Hour)
	unknown := stage("Unknown", "1.0", true, time.Hour)
	current := stage("Current", "3.0", true, time.Hour)
	oldStray := stage("Stray", "0.1", false, 31*24*time.Hour)
	newStray := stage("Fresh", "0.1", false, time.Hour)
	if err := os.WriteFile(filepath.Join(root, "loose.dmg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	after := inventory.Result{Entries: []inventory.Entry{
		{Name: "Done", Installed: "2.0", Status: "CURRENT"},
		{Name: "Pending", Installed: "1.0", Available: "1.5", Status: "OUTDATED"},
		{Name: "Moved", Installed: "0.9", Available: "1.1", Status: "OUTDATED"},
		{Name: "Unknown", Installed: "0.9", Status: "UNKNOWN"}, // probe failed: no verdict, keep
		{Name: "Current", Installed: "3.1", Status: "CURRENT"},
	}}
	removed, freed := CleanStaging(after, now)
	want := []string{
		"Current 3.0 (now installed)",
		"Done 2.0 (now installed)",
		"Gone 1.0 (app no longer found)",
		"Moved 1.0 (superseded by 1.1)",
		unsafeName.ReplaceAllString("Stray-0.1", "_") + " (no record of what it was for, older than 30 days)",
	}
	if strings.Join(removed, "\n") != strings.Join(want, "\n") {
		t.Fatalf("removed:\n%s\nwant:\n%s", strings.Join(removed, "\n"), strings.Join(want, "\n"))
	}
	// Five 10-byte payloads plus four small manifests.
	if freed < 50 || freed > 50+4*200 {
		t.Fatalf("freed %d bytes, want 50 plus manifests", freed)
	}
	for _, dir := range []string{installed, superseded, gone, current, oldStray} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("%s should be gone: %v", dir, err)
		}
	}
	for _, dir := range []string{pending, unknown, newStray, filepath.Join(root, "loose.dmg")} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("%s should remain: %v", dir, err)
		}
	}
}
