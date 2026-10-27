package probe

import (
	"bytes"
	"encoding/xml"
	"errors"
	"regexp"
	"strings"
)

type atomFeed struct {
	Entries []struct {
		Title   string `xml:"title"`
		Updated string `xml:"updated"`
		ID      string `xml:"id"`
		Links   []struct {
			Rel  string `xml:"rel,attr"`
			Href string `xml:"href,attr"`
		} `xml:"link"`
	} `xml:"entry"`
}

// latestAtomEntry returns the first entry of a GitHub or GitLab feed. Feeds are
// newest-first; pre-release tags are left for the caller to judge.
func latestAtomEntry(body []byte) (title, updated, link string, err error) {
	var f atomFeed
	if err = xml.Unmarshal(body, &f); err != nil {
		return "", "", "", err
	}
	if len(f.Entries) == 0 {
		return "", "", "", errors.New("feed has no entries")
	}
	// Feeds are newest-first; prefer the newest entry that does not look like a pre-release.
	e := f.Entries[0]
	for _, candidate := range f.Entries {
		if !prerelease.MatchString(candidate.Title) && !prerelease.MatchString(candidate.ID) {
			e = candidate
			break
		}
	}
	for _, l := range e.Links {
		if l.Rel == "" || l.Rel == "alternate" {
			link = l.Href
			break
		}
	}
	title = strings.TrimSpace(e.Title)
	// Titles are free text ("4.0.4 release"); the tag at the end of the link or ID is exact.
	for _, ref := range []string{link, e.ID} {
		if i := strings.LastIndex(ref, "/"); i >= 0 && i+1 < len(ref) {
			if tag := ref[i+1:]; looksLikeVersion.MatchString(strings.TrimLeft(tag, "vV")) || strings.ContainsAny(tag, "0123456789") {
				title = tag
				break
			}
		}
	}
	for {
		next := underscoreTag.ReplaceAllString(title, "$1.$2")
		if next == title {
			break
		}
		title = next
	}
	return title, strings.TrimSpace(e.Updated), link, nil
}

type rssFeed struct {
	Items []struct {
		Title   string `xml:"title"`
		Link    string `xml:"link"`
		PubDate string `xml:"pubDate"`
	} `xml:"channel>item"`
}

// underscoreTag rewrites INKSCAPE_1_4_4 style tags so digits compare as 1.4.4.
var underscoreTag = regexp.MustCompile(`(\d)_(\d)`)
var prerelease = regexp.MustCompile(`(?i)[-_.](alpha|beta|rc|pre|nightly|dev|canary|snapshot|test)[-_.0-9]*\b|\b(alpha|beta|nightly)\b`)
var installerExt = regexp.MustCompile(`(?i)\.(dmg|pkg|zip|tar\.gz|tbz|xz)$`)

// latestSourceForgeFile picks the newest Mac-looking installer from the files RSS.
func latestSourceForgeFile(body []byte) (version, released, link string, err error) {
	var f rssFeed
	if err = xml.Unmarshal(body, &f); err != nil {
		return "", "", "", err
	}
	for _, it := range f.Items {
		t := strings.TrimSpace(it.Title)
		if !installerExt.MatchString(t) {
			continue
		}
		lower := strings.ToLower(t)
		if strings.Contains(lower, "win") || strings.Contains(lower, ".exe") || strings.Contains(lower, "linux") || strings.Contains(lower, ".deb") || strings.Contains(lower, ".rpm") {
			continue
		}
		if v, ok := versionNumbers(t); ok && len(v) >= 2 {
			return versionDigits.FindString(t), strings.TrimSpace(it.PubDate), it.Link, nil
		}
	}
	return "", "", "", errors.New("no versioned installer in feed")
}

var ausUpdate = regexp.MustCompile(`<update\b[^>]*\bappVersion="([^"]+)"`)

// mozillaOffered reads the application update server's answer for one exact build.
func mozillaOffered(body []byte) (string, bool) {
	if m := ausUpdate.FindSubmatch(body); m != nil {
		return string(m[1]), true
	}
	return "", false
}

// electronLatest parses electron-updater's latest-mac.yml.
func electronLatest(body []byte) (version, released string) {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "version:"):
			version = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "version:")), `"'`)
		case strings.HasPrefix(line, "releaseDate:"):
			released = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "releaseDate:")), `"'`)
		}
	}
	return version, released
}

type appcast struct {
	Items []struct {
		Title     string `xml:"title"`
		PubDate   string `xml:"pubDate"`
		Short     string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle shortVersionString"`
		Version   string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle version"`
		Enclosure struct {
			Short   string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle shortVersionString,attr"`
			Version string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle version,attr"`
		} `xml:"enclosure"`
	} `xml:"channel>item"`
}

// latestAppcastItem returns the highest short version in a Sparkle appcast.
func latestAppcastItem(body []byte) (version, released string) {
	var a appcast
	if xml.Unmarshal(bytes.TrimSpace(body), &a) != nil {
		return "", ""
	}
	for _, it := range a.Items {
		v := firstNonEmpty(it.Short, it.Enclosure.Short, it.Version, it.Enclosure.Version)
		if v == "" {
			if m := versionDigits.FindString(it.Title); m != "" {
				v = m
			}
		}
		if v == "" {
			continue
		}
		if version == "" {
			version, released = v, strings.TrimSpace(it.PubDate)
			continue
		}
		if c, ok := compareVersions(v, version); ok && c > 0 {
			version, released = v, strings.TrimSpace(it.PubDate)
		}
	}
	return version, released
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
