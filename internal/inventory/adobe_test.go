package inventory

import (
	"testing"
)

func TestParseAdobeCatalogue(t *testing.T) {
	data := []byte(`{"channel":[{"products":{"product":[{"id":"LTRM","version":"15.6","displayName":"Lightroom Classic","platforms":{"platform":[{"id":"macuniversal"}]}},{"id":"PHSP","version":"27.11","platforms":{"platform":[{"id":"macuniversal"}]}}]}}]}`)
	got, err := parseAdobeCatalogue(data)
	if err != nil || len(got) != 2 || got[0].Version != "15.6" {
		t.Fatalf("got %#v err %v", got, err)
	}
	if _, err := parseAdobeCatalogue([]byte(`{"channel":[]}`)); err == nil {
		t.Fatal("accepted empty channel")
	}
}
func TestApplyAdobe(t *testing.T) {
	cases := []struct {
		name, app, installed, sap       string
		catalogue                       []AdobeProduct
		expectedStatus, expectedVersion string
	}{
		{"same major", "Adobe Lightroom Classic", "15.1.1", "LTRM", []AdobeProduct{{ID: "LTRM", Version: "15.6", Platforms: []string{"macuniversal"}}, {ID: "LTRM", Version: "15.5", Platforms: []string{"macuniversal"}}}, "OUTDATED", "15.6"},
		{"new major", "Adobe Photoshop 2023", "24.1.1", "PHSP", []AdobeProduct{{ID: "PHSP", Version: "27.11", Platforms: []string{"macuniversal"}}}, "UPGRADE AVAILABLE", "27.11"},
		{"exact only does not prove current", "Adobe Lightroom Classic", "15.1.1", "LTRM", []AdobeProduct{{ID: "LTRM", Version: "15.1.1", Platforms: []string{"macuniversal"}}}, "UNKNOWN", ""},
		{"other platforms", "Adobe Lightroom Classic", "15.1.1", "LTRM", []AdobeProduct{{ID: "LTRM", Version: "15.6", Platforms: []string{"win64"}}}, "UNCHECKED", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Result{Entries: []Entry{{Name: tc.app, Installed: tc.installed, Status: "UNCHECKED", Path: "/Applications/test.app"}}, Adobe: &AdobeResult{CheckStatus: "checked", Installed: []AdobeInstalled{{SAPCode: tc.sap, Version: tc.installed}}, Products: tc.catalogue}}
			ApplyAdobe(&r)
			e := r.Entries[0]
			if e.Status != tc.expectedStatus || e.Available != tc.expectedVersion {
				t.Fatalf("got %+v", e)
			}
		})
	}
}
func TestAdobeDoesNotOverrideOrGuess(t *testing.T) {
	r := Result{Entries: []Entry{{Name: "Adobe Lightroom Classic", Installed: "15.1.1", Path: "/Applications/LR.app", Status: "OUTDATED", UpdateSource: "sparkle", Available: "15.2"}, {Name: "Adobe Photoshop 2023", Installed: "24.1.1", Path: "/Applications/Photoshop.app", Status: "UNCHECKED"}}, Adobe: &AdobeResult{CheckStatus: "checked", Installed: []AdobeInstalled{{SAPCode: "LTRM", Version: "15.1.1"}, {SAPCode: "PHSP", Version: "24.1.2"}}, Products: []AdobeProduct{{ID: "LTRM", Version: "15.6", Platforms: []string{"macuniversal"}}, {ID: "PHSP", Version: "27.11", Platforms: []string{"macuniversal"}}}}}
	ApplyAdobe(&r)
	if r.Entries[0].Available != "15.2" || r.Entries[1].Status != "UNCHECKED" {
		t.Fatalf("incorrect claims: %+v", r.Entries)
	}
}

func TestAdobeComponentVersionMatching(t *testing.T) {
	cases := []struct {
		code, bundle, pkg string
		want              bool
	}{
		{"COSY", "7.8.10.1", "7.8.10", true},
		{"CCXP", "7.5.0-8", "7.5.0", true},
		{"CCXP", "7.5.0-rc1", "7.5.0", false},
		{"COSY", "7.8.100.1", "7.8.10", false},
		{"PHSP", "24.1.1.9", "24.1.1", false},
		{"LTRM", "15.1.1", "15.1.1", true},
	}
	for _, tc := range cases {
		if got := adobeInstalledMatchesBundle(tc.code, tc.bundle, tc.pkg); got != tc.want {
			t.Errorf("%s bundle=%s pkg=%s: got %v want %v", tc.code, tc.bundle, tc.pkg, got, tc.want)
		}
	}
}

func TestAdobeAdditionalComponents(t *testing.T) {
	r := Result{
		Entries: []Entry{
			{Name: "Adobe Content Synchronizer", Installed: "7.8.10.1", Path: "/Applications/Utilities/Adobe Sync/CoreSync/Core Sync.app", Status: "UNCHECKED"},
			{Name: "CCXProcess", Installed: "7.5.0-8", Path: "/Applications/Utilities/Adobe Creative Cloud Experience/CCXProcess/CCXProcess.app", Status: "UNCHECKED"},
			{Name: "Adobe DNG Converter", Installed: "18.1.1 (2422)", Path: "/Applications/Adobe DNG Converter.app", Status: "UNCHECKED"},
		},
		Adobe: &AdobeResult{CheckStatus: "checked",
			Installed: []AdobeInstalled{{SAPCode: "COSY", Version: "7.8.10"}, {SAPCode: "CCXP", Version: "7.5.0"}},
			Products: []AdobeProduct{
				{ID: "COSY", Version: "7.8.11", Platforms: []string{"macuniversal"}},
				{ID: "CCXP", Version: "7.5.0", Platforms: []string{"macuniversal"}},
			},
		},
	}
	ApplyAdobe(&r)
	if e := r.Entries[0]; e.Status != "OUTDATED" || e.Available != "7.8.11" {
		t.Fatalf("COSY: %+v", e)
	}
	if e := r.Entries[1]; e.Status != "UNKNOWN" || e.UpdateSource != "adobe-catalog" {
		t.Fatalf("CCXP: %+v", e)
	}
	if e := r.Entries[2]; e.Status != "UNCHECKED" {
		t.Fatalf("DNG guessed: %+v", e)
	}
}
