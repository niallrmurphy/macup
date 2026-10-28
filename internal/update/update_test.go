package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

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
			{Name: "calibre", Status: "OUTDATED", Sources: []string{"apps", "lookup"}, UpdateSource: "homebrew-cask"},
			{Name: "Fine", Status: "CURRENT", Sources: []string{"apps"}},
		},
		Microsoft: &inventory.MicrosoftResult{Updates: []inventory.MicrosoftUpdate{{ID: "OPIM2019", Name: "Microsoft Outlook", AvailableVersion: "16.114.2"}}},
		Sparkle:   &inventory.SparkleResult{Checks: []inventory.SparkleCheck{{Path: "/Applications/NotInstalled-Test.app", Status: "OUTDATED", EnclosureURL: "https://releases.arc.net/Arc.zip", EdSignature: "sig", EnclosureLength: 10}}},
	}
	plan := Plan(r)
	if len(plan) != 7 {
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
	if a := byName["calibre"]; a.Route != RouteAdvice || !strings.Contains(a.Reason, "homebrew-cask") {
		t.Fatalf("%+v", a)
	}
	if plan[0].Route != RouteBrew || plan[len(plan)-1].Route != RouteAdvice {
		t.Fatalf("plan should be ordered by route: %+v", plan)
	}
	if f := Filter(plan, []string{"outlook", "ZED"}); len(f) != 2 {
		t.Fatalf("%+v", f)
	}
	var b bytes.Buffer
	if err := PrintPlan(&b, plan); err != nil || !strings.Contains(b.String(), "7 actions: 2 brew, 1 mas, 1 msupdate, 1 sparkle downloads, 2 manual") {
		t.Fatal(b.String(), err)
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
