package probe

import (
	"net/url"
	"regexp"
	"strings"
)

// Generic labels that say nothing about the vendor.
var genericTokens = map[string]bool{
	"com": true, "org": true, "net": true, "app": true, "apps": true, "mac": true, "macos": true, "osx": true,
	"inc": true, "ltd": true, "llc": true, "corp": true, "corporation": true, "gmbh": true, "limited": true, "private": true,
	"software": true, "technology": true, "technologies": true, "america": true, "usa": true, "europe": true,
	"desktop": true, "client": true, "launcher": true, "product": true, "electron": true, "todesktop": true,
	"install4j": true, "installbuilder": true, "helper": true, "free": true, "pro": true, "lite": true, "trial": true,
	"www": true, "the": true, "and": true, "for": true, "team": true, "studio": true, "video": true, "communications": true,
}

var wordSplit = regexp.MustCompile(`[^A-Za-z0-9]+`)

// affinityTokens are the vendor- or product-specific words we can trust to
// tie a URL to this application: the app name, bundle identifier labels, and
// the signing organisation. Each is lowercased and at least four characters.
func affinityTokens(r *Report) []string {
	seen := map[string]bool{}
	var out []string
	consider := func(s string) {
		s = strings.ToLower(s)
		if len(s) < 4 || genericTokens[s] || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	consider(squash(r.Application.Name))
	for _, w := range wordSplit.Split(r.Application.Name, -1) {
		consider(w)
	}
	for _, label := range strings.Split(r.Application.BundleID, ".") {
		consider(wordSplit.ReplaceAllString(label, ""))
	}
	for _, w := range wordSplit.Split(r.Signer, -1) {
		consider(w)
	}
	return out
}

// hasAffinity reports whether the URL's host or path names the vendor or product.
func hasAffinity(raw string, tokens []string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	path := strings.ToLower(u.Path)
	for _, t := range tokens {
		if strings.Contains(host, t) || strings.Contains(path, t) {
			return true
		}
	}
	return false
}

// Third-party hosts that appear in nearly every binary and never describe the
// application's own updates. Consulted only when a URL has no affinity.
var noiseHosts = []string{
	"nodejs.org", "v8.dev", "oasis-open.org", "sparkle-project.org", "andymatuschak.org", "khronos.org",
	"w3.org", "apple.com", "aws.amazon.com", "amazonwebservices.com", "microsoft.com", "mono-project.com",
	"unsplash.com", "google.com", "googleapis.com", "matroska.org", "fedoraproject.org", "adobe.com",
	"chromium.org", "mozilla.org", "electronjs.org", "npmjs.com", "python.org", "qt.io", "gnu.org",
	"openssl.org", "ietf.org", "wikipedia.org", "youtube.com", "twitter.com", "facebook.com", "skype.com",
	"trouter.io", "sentry.io", "bugsplat.com", "crashlytics.com", "amplitude.com", "mixpanel.com",
}

func noisyHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return true
	}
	host := strings.ToLower(u.Hostname())
	for _, n := range noiseHosts {
		if host == n || strings.HasSuffix(host, "."+n) {
			return true
		}
	}
	return false
}
