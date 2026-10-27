package probe

import (
	"path/filepath"
	"regexp"
	"strings"
)

var helperName = regexp.MustCompile(`(?i)\b(uninstall(er)?|helper|readme|license|url handler|diagnostics|user ?agent|crash ?reporter|launcher)\b`)
var pwaPrefixes = []string{"com.google.Chrome.app.", "com.google.drivefs.shortcuts.", "com.microsoft.edgemac.app.", "com.brave.Browser.app.", "com.vivaldi.Vivaldi.app.", "org.chromium.Chromium.app."}
var selfFamilies = []string{"sparkle", "squirrel", "electron-updater", "keystone", "mozilla", "paddle", "devmate"}

// classify decides who is responsible for updating the bundle. The order is
// most specific first; a helper nested inside a vendor suite is a helper even
// if the vendor runs an updater.
func classify(r *Report, fields map[string]string) {
	name := r.Application.Name
	bid := r.Application.BundleID
	switch {
	case r.hasFamily("mas-receipt"):
		r.Management = &Management{"appstore", "App Store receipt present"}
	case r.Signer == "Apple Mac OS Application Signing":
		r.Management = &Management{"appstore", "signed by App Store"}
	case fields["CrAppModeShortcutURL"] != "" || hasPrefixAny(bid, pwaPrefixes):
		r.Management = &Management{"pwa", "web app shim installed and updated by its parent browser"}
	case helperName.MatchString(name) || helperName.MatchString(strings.TrimSuffix(filepath.Base(r.Application.Path), ".app")):
		r.Management = &Management{"helper", "helper or uninstaller bundle; updated with its parent product"}
	case fields["LSUIElement"] == "true" && nestedUnderVendorDir(r.Application.Path):
		r.Management = &Management{"helper", "background agent nested under " + filepath.Base(filepath.Dir(r.Application.Path))}
	case r.hasFamily("setapp"):
		r.Management = &Management{"setapp", "Setapp framework present"}
	}
	if r.Management != nil {
		return
	}
	for _, f := range r.Families {
		if strings.HasPrefix(f, "launch-agent:") {
			r.Management = &Management{"vendor", "vendor updater job " + strings.TrimPrefix(f, "launch-agent:")}
			return
		}
	}
	for _, f := range selfFamilies {
		if r.hasFamily(f) {
			r.Management = &Management{"self", "embedded updater: " + f}
			return
		}
	}
}

func hasPrefixAny(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// nestedUnderVendorDir is true for /Applications/Vendor Suite/Thing.app style paths.
func nestedUnderVendorDir(path string) bool {
	parent := filepath.Dir(path)
	return !strings.HasSuffix(parent, "/Applications") && !strings.HasSuffix(parent, "/Utilities")
}
