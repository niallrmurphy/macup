package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
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
	if failed := Execute(nil, plan, Options{FetchOnly: true, Out: &out, Err: &errb}); failed != 0 || !strings.Contains(out.String(), "skip   X") {
		t.Fatal(failed, out.String())
	}
}
