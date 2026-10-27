package probe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Generic interpretation of vendor endpoints found in binaries: no per-vendor
// knowledge, only the shapes release metadata usually takes.

var versionKeys = []string{"tag_name", "version", "latest_version", "latestversion", "current_version", "currentversion", "shortversionstring", "latest", "name"}
var dateKeys = []string{"releasedate", "published_at", "release_date", "date", "updated", "updated_at"}
var looksLikeVersion = regexp.MustCompile(`^v?\d+\.\d+(?:\.\d+)*`)

// jsonVersion walks a JSON document for the first version-looking value under
// a conventional key, breadth-first so top-level fields win.
func jsonVersion(body []byte) (version, released string) {
	var root any
	if json.Unmarshal(body, &root) != nil {
		return "", ""
	}
	queue := []any{root}
	for len(queue) > 0 && (version == "" || released == "") {
		node := queue[0]
		queue = queue[1:]
		switch n := node.(type) {
		case map[string]any:
			lower := map[string]any{}
			for k, v := range n {
				lower[strings.ToLower(k)] = v
			}
			if version == "" {
				for _, k := range versionKeys {
					if s, ok := lower[k].(string); ok && looksLikeVersion.MatchString(strings.TrimSpace(s)) {
						version = strings.TrimSpace(s)
						break
					}
				}
			}
			if released == "" {
				for _, k := range dateKeys {
					if s, ok := lower[k].(string); ok && s != "" {
						released = s
						break
					}
				}
			}
			keys := make([]string, 0, len(n))
			for k := range n {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				queue = append(queue, n[k])
			}
		case []any:
			queue = append(queue, n...)
		}
	}
	return version, released
}

var xmlVersionAttr = regexp.MustCompile(`(?i)\b(?:app)?version(?:string)?\s*=\s*"(v?\d+\.\d+(?:\.\d+)*)"`)
var xmlVersionElem = regexp.MustCompile(`(?i)<(?:[a-z]+:)?(?:latest|version|current|release)[a-z]*[^>]*>\s*(v?\d+\.\d+(?:\.\d+)*)\s*<`)

// xmlVersion tries a Sparkle appcast first, then any version-named attribute
// or element.
func xmlVersion(body []byte) (version, released string) {
	if v, rel := latestAppcastItem(body); v != "" {
		return v, rel
	}
	if m := xmlVersionAttr.FindSubmatch(body); m != nil {
		return string(m[1]), ""
	}
	if m := xmlVersionElem.FindSubmatch(body); m != nil {
		return string(m[1]), ""
	}
	return "", ""
}

// textVersion accepts a short plain-text file whose first line is a version
// (download.virtualbox.org/virtualbox/LATEST.TXT is the archetype).
func textVersion(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) == 0 || len(s) > 64 {
		return ""
	}
	first := strings.SplitN(s, "\n", 2)[0]
	if looksLikeVersion.MatchString(strings.TrimSpace(first)) {
		return strings.TrimSpace(first)
	}
	return ""
}

var hrefPattern = regexp.MustCompile(`(?i)(?:href|src)\s*=\s*["']([^"']+\.(?:dmg|pkg|zip))["']`)

// htmlInstallerVersion finds the highest-versioned Mac installer link whose
// filename names the product. Pages list several platforms; the token check
// keeps Windows builds of something else from being picked up.
func htmlInstallerVersion(body []byte, base string, tokens []string) (version, link string) {
	baseURL, _ := url.Parse(base)
	for _, m := range hrefPattern.FindAllSubmatch(body, -1) {
		href := string(m[1])
		if baseURL != nil {
			if ref, err := url.Parse(href); err == nil {
				href = baseURL.ResolveReference(ref).String()
			}
		}
		file := path.Base(href)
		lower := strings.ToLower(file)
		if strings.Contains(lower, "win") || strings.Contains(lower, "linux") {
			continue
		}
		if !hasAffinity("https://x/"+file, tokens) {
			continue
		}
		v, ok := versionNumbers(file)
		if !ok || len(v) < 2 {
			continue
		}
		candidate := versionDigits.FindString(file)
		if version == "" {
			version, link = candidate, href
			continue
		}
		if c, ok := compareVersions(candidate, version); ok && c > 0 {
			version, link = candidate, href
		}
	}
	return version, link
}

// interpretGeneric fetches a candidate and extracts a version by content type.
func interpretGeneric(ctx context.Context, raw, installed string, tokens []string) (*Lookup, error) {
	body, ct, err := fetchBytes(ctx, raw, "application/json, application/xml, text/xml, text/plain, text/html, */*", 2<<20)
	if err != nil {
		return nil, err
	}
	l := &Lookup{Source: "generic", URL: raw}
	trimmed := strings.TrimSpace(string(body))
	lowerURL := strings.ToLower(raw)
	switch {
	case strings.Contains(ct, "json") || strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "["):
		l.Version, l.Released = jsonVersion(body)
		l.Source = "generic:json"
	case strings.Contains(ct, "xml") || strings.HasPrefix(trimmed, "<?xml") || strings.HasSuffix(lowerURL, ".xml"):
		l.Version, l.Released = xmlVersion(body)
		l.Source = "generic:xml"
	case strings.HasSuffix(lowerURL, ".yml") || strings.HasSuffix(lowerURL, ".yaml"):
		l.Version, l.Released = electronLatest(body)
		l.Source = "generic:yaml"
	case strings.Contains(ct, "html"):
		var link string
		l.Version, link = htmlInstallerVersion(body, raw, tokens)
		if link != "" {
			l.URL = link
		}
		l.Source = "generic:html"
	default:
		l.Version = textVersion(body)
		l.Source = "generic:text"
	}
	if l.Version == "" {
		return nil, nil
	}
	l.Comparison = comparison(installed, l.Version)
	return l, nil
}

// Well-known version files tried beside directory-like vendor URLs, such as
// https://download.virtualbox.org/virtualbox/ -> .../LATEST.TXT.
var wellKnownFiles = []string{"LATEST.TXT", "latest.txt", "version.txt", "appcast.xml", "latest-mac.yml"}

// directoryLike accepts only explicit directory URLs: a trailing slash, or a
// bare host. Page-like paths without an extension are far more common than
// directories and would waste the request budget.
func directoryLike(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return u.Path == "" || strings.HasSuffix(u.Path, "/")
}

var installerExtURL = regexp.MustCompile(`(?i)\.(dmg|pkg|zip)$`)

// headDownload issues a HEAD for a stable download URL and reads the version
// from the redirect target or the served filename, when either carries one.
func headDownload(ctx context.Context, raw, installed string) (*Lookup, error) {
	u, err := parseHTTPS(raw)
	if err != nil {
		return nil, err
	}
	if err = publicHost(ctx, u.Hostname()); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "macup-probe/0.1")
	res, err := safeClient().Do(req)
	if err != nil {
		return nil, err
	}
	res.Body.Close()
	l := &Lookup{Source: "download-head", URL: raw, Released: res.Header.Get("Last-Modified")}
	name := ""
	if loc := res.Header.Get("Location"); loc != "" {
		name = path.Base(loc)
		l.URL = loc
	} else if cd := res.Header.Get("Content-Disposition"); cd != "" {
		if i := strings.Index(strings.ToLower(cd), "filename="); i >= 0 {
			name = strings.Trim(cd[i+len("filename="):], `"; `)
		}
	}
	if v, ok := versionNumbers(name); ok && len(v) >= 2 {
		l.Version = versionDigits.FindString(name)
		l.Comparison = comparison(installed, l.Version)
		return l, nil
	}
	if l.Released == "" {
		return nil, nil
	}
	l.Comparison = "unknown"
	l.Note = "stable download URL; compare Last-Modified with the install date"
	return l, nil
}
