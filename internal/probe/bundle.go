package probe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Updater families recognised by their footprint inside the bundle. The
// framework identifies the protocol; the endpoint is then found in config
// files or the executable.
var frameworkFamilies = []struct{ needle, family string }{
	{"Sparkle", "sparkle"},
	{"Squirrel", "squirrel"},
	{"ShipIt", "squirrel"},
	{"Keystone", "keystone"},
	{"Setapp", "setapp"},
	{"Paddle", "paddle"},
	{"DevMate", "devmate"},
	{"Electron", "electron"},
}

func inspectBundle(ctx context.Context, r *Report, fields map[string]string) {
	path := r.Application.Path
	if entries, err := os.ReadDir(filepath.Join(path, "Contents", "Frameworks")); err == nil {
		for _, e := range entries {
			for _, f := range frameworkFamilies {
				if strings.Contains(e.Name(), f.needle) {
					r.addFamily(f.family)
				}
			}
		}
	}
	if _, err := os.Stat(filepath.Join(path, "Contents", "_MASReceipt", "receipt")); err == nil {
		r.addFamily("mas-receipt")
	}
	if _, err := os.Stat(filepath.Join(path, "Contents", "Resources", "app.asar")); err == nil {
		r.addFamily("electron")
		inspectAsar(r, filepath.Join(path, "Contents", "Resources", "app.asar"))
	}
	if raw, err := os.ReadFile(filepath.Join(path, "Contents", "Resources", "app-update.yml")); err == nil {
		r.addFamily("electron-updater")
		for _, c := range electronUpdaterCandidates(raw) {
			add(r, c.URL, c.Source, c.Score)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(path, "Contents", "Resources", "application.ini")); err == nil {
		r.addFamily("mozilla")
		channel := mozillaChannel(path)
		if u := mozillaUpdateURL(raw, channel, runtime.GOARCH); u != "" {
			add(r, u, "application.ini:AppUpdate", 90)
		}
	}
	exe := fields["CFBundleExecutable"]
	if exe == "" {
		exe = strings.TrimSuffix(filepath.Base(path), ".app")
	}
	if r.strongest() < 90 && !r.hasFamily("mas-receipt") {
		scanBundleURLs(r, path, exe)
	}
	tokens := affinityTokens(r)
	for _, c := range append([]Candidate{}, r.Candidates...) {
		if forgeTrusted(c, tokens) {
			deriveForgeFeeds(r, c.URL)
		}
	}
	if strings.HasPrefix(r.Application.BundleID, "net.sourceforge.") {
		proj := strings.TrimPrefix(r.Application.BundleID, "net.sourceforge.")
		if i := strings.Index(proj, "."); i > 0 {
			proj = proj[:i]
		}
		add(r, "https://sourceforge.net/projects/"+proj+"/rss?path=/", "derived:sourceforge", 80)
	}
	sortCandidates(r)
}

// electronUpdaterCandidates parses the flat YAML electron-builder writes.
func electronUpdaterCandidates(raw []byte) []Candidate {
	kv := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, ":"); i > 0 && !strings.HasPrefix(line, "#") {
			kv[strings.TrimSpace(line[:i])] = strings.Trim(strings.TrimSpace(line[i+1:]), `"'`)
		}
	}
	var out []Candidate
	switch kv["provider"] {
	case "github":
		if kv["owner"] != "" && kv["repo"] != "" {
			host := kv["host"]
			if host == "" {
				host = "github.com"
			}
			out = append(out, Candidate{"https://" + host + "/" + kv["owner"] + "/" + kv["repo"] + "/releases.atom", "electron-updater:github", 90})
		}
	case "generic", "s3", "spaces", "keygen", "bitbucket", "gitlab":
		if kv["url"] != "" {
			u := strings.TrimRight(kv["url"], "/")
			channel := kv["channel"]
			if channel == "" {
				channel = "latest"
			}
			out = append(out, Candidate{u + "/" + channel + "-mac.yml", "electron-updater:" + kv["provider"], 90})
		}
	default:
		if kv["url"] != "" {
			out = append(out, Candidate{kv["url"], "electron-updater", 85})
		}
	}
	return out
}

func inspectAsar(r *Report, asar string) {
	raw, err := asarFile(asar, "package.json", 1<<20)
	if err != nil {
		return
	}
	for _, c := range packageJSONCandidates(raw) {
		add(r, c.URL, c.Source, c.Score)
	}
}

// packageJSONCandidates extracts homepage, repository and publish targets.
func packageJSONCandidates(raw []byte) []Candidate {
	var pkg struct {
		Homepage   string          `json:"homepage"`
		Repository json.RawMessage `json:"repository"`
		Build      struct {
			Publish json.RawMessage `json:"publish"`
		} `json:"build"`
	}
	if json.Unmarshal(raw, &pkg) != nil {
		return nil
	}
	var out []Candidate
	if pkg.Homepage != "" {
		out = append(out, Candidate{pkg.Homepage, "asar:package.json:homepage", 60})
	}
	repo := ""
	var s string
	if json.Unmarshal(pkg.Repository, &s) == nil {
		repo = s
	} else {
		var o struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(pkg.Repository, &o) == nil {
			repo = o.URL
		}
	}
	if u := normaliseRepo(repo); u != "" {
		out = append(out, Candidate{u, "asar:package.json:repository", 70})
	}
	var publishes []json.RawMessage
	if json.Unmarshal(pkg.Build.Publish, &publishes) != nil && len(pkg.Build.Publish) > 0 {
		publishes = []json.RawMessage{pkg.Build.Publish}
	}
	for _, p := range publishes {
		var o struct{ Provider, Owner, Repo, URL string }
		if json.Unmarshal(p, &o) != nil {
			continue
		}
		switch {
		case o.Provider == "github" && o.Owner != "" && o.Repo != "":
			out = append(out, Candidate{"https://github.com/" + o.Owner + "/" + o.Repo + "/releases.atom", "asar:package.json:publish", 85})
		case o.URL != "":
			out = append(out, Candidate{o.URL, "asar:package.json:publish", 80})
		}
	}
	return out
}

// normaliseRepo turns npm-style repository shorthands into an HTTPS URL.
func normaliseRepo(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "git+")
	s = strings.TrimSuffix(s, ".git")
	switch {
	case s == "":
		return ""
	case strings.HasPrefix(s, "github:"):
		return "https://github.com/" + strings.TrimPrefix(s, "github:")
	case strings.HasPrefix(s, "gitlab:"):
		return "https://gitlab.com/" + strings.TrimPrefix(s, "gitlab:")
	case strings.HasPrefix(s, "git@github.com:"):
		return "https://github.com/" + strings.TrimPrefix(s, "git@github.com:")
	case strings.HasPrefix(s, "ssh://git@github.com/"):
		return "https://github.com/" + strings.TrimPrefix(s, "ssh://git@github.com/")
	case strings.HasPrefix(s, "https://"):
		return s
	case regexp.MustCompile(`^[\w.-]+/[\w.-]+$`).MatchString(s):
		return "https://github.com/" + s
	}
	return ""
}

var forgeHosts = map[string]bool{"github.com": true, "gitlab.com": true, "api.github.com": true}
var forgeReserved = map[string]bool{"sponsors": true, "features": true, "orgs": true, "apps": true, "marketplace": true, "site": true, "login": true, "settings": true, "explore": true, "topics": true, "about": true, "pricing": true, "groups": true, "users": true, "help": true}

// forgeRepo returns host, owner and repo for GitHub or GitLab project URLs.
func forgeRepo(raw string) (host, owner, repo string, ok bool) {
	u, err := parseHTTPS(raw)
	if err != nil || !forgeHosts[u.Hostname()] {
		return "", "", "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	host = u.Hostname()
	if host == "api.github.com" {
		// api.github.com/repos/{owner}/{repo}/...
		if len(parts) < 3 || parts[0] != "repos" {
			return "", "", "", false
		}
		parts = parts[1:]
		host = "github.com"
	}
	if len(parts) < 2 || forgeReserved[parts[0]] || parts[1] == "" || strings.HasPrefix(parts[1], "-") {
		return "", "", "", false
	}
	return host, parts[0], strings.TrimSuffix(parts[1], ".git"), true
}

// forgeTrusted limits feed derivation to evidence that names this product's
// own project. Executables embed URLs of every dependency they link.
func forgeTrusted(c Candidate, tokens []string) bool {
	switch {
	case c.Source == "executable", strings.HasPrefix(c.Source, "binary:"):
		// Binaries embed URLs of every dependency they link; the repo must name the product.
		return hasAffinity(c.URL, tokens)
	case strings.HasPrefix(c.Source, "lookup:"):
		return false
	case strings.HasPrefix(c.Source, "Info.plist:"):
		return c.Score >= 65
	}
	return true
}

// deriveForgeFeeds adds canonical release feeds for any forge project named in evidence.
func deriveForgeFeeds(r *Report, raw string) {
	host, owner, repo, ok := forgeRepo(raw)
	if !ok {
		return
	}
	switch host {
	case "github.com":
		add(r, "https://github.com/"+owner+"/"+repo+"/releases.atom", "derived:github-releases", 85)
	case "gitlab.com":
		add(r, "https://gitlab.com/"+owner+"/"+repo+"/-/tags?format=atom", "derived:gitlab-tags", 80)
	}
}

// mozillaUpdateURL expands the AppUpdate template from application.ini.
func mozillaUpdateURL(raw []byte, channel, arch string) string {
	section := ""
	vals := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]")
			continue
		}
		if i := strings.Index(line, "="); i > 0 && !strings.HasPrefix(line, ";") {
			vals[section+"."+strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
		}
	}
	tmpl := vals["AppUpdate.URL"]
	if tmpl == "" || vals["App.Name"] == "" || vals["App.Version"] == "" {
		return ""
	}
	target := "Darwin_x86_64-gcc3"
	if arch == "arm64" {
		target = "Darwin_aarch64-gcc3"
	}
	if channel == "" {
		channel = "release"
	}
	rep := strings.NewReplacer(
		"%PRODUCT%", vals["App.Name"],
		"%VERSION%", vals["App.Version"],
		"%BUILD_ID%", vals["App.BuildID"],
		"%BUILD_TARGET%", target,
		"%LOCALE%", "en-US",
		"%CHANNEL%", channel,
		"%OS_VERSION%", "default",
		"%SYSTEM_CAPABILITIES%", "default",
		"%DISTRIBUTION%", "default",
		"%DISTRIBUTION_VERSION%", "default",
	)
	out := rep.Replace(tmpl)
	if strings.Contains(out, "%") {
		return ""
	}
	return out
}

var channelPref = regexp.MustCompile(`app\.update\.channel"\s*,\s*"([^"]+)"`)

func mozillaChannel(path string) string {
	raw, err := os.ReadFile(filepath.Join(path, "Contents", "Resources", "defaults", "pref", "channel-prefs.js"))
	if err != nil {
		return ""
	}
	if m := channelPref.FindSubmatch(raw); m != nil {
		return string(m[1])
	}
	return ""
}

var urlByte = [256]bool{}

func init() {
	for _, c := range []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~:/?#[]@!$&()*+,;=%") {
		urlByte[c] = true
	}
}

var updateKeywords = regexp.MustCompile(`(?i)(appcast|sparkle|update|release|changelog|version|download|latest|github\.com|gitlab\.com|sourceforge\.net)`)
var strongKeywords = regexp.MustCompile(`(?i)(appcast|sparkle|update|versions?\.(xml|json|txt)|release\.json|latest\.(txt|json|yml)|/releases)`)
var dependencyPaths = regexp.MustCompile(`(?i)\.(dtd|rpm|deb|tar\.gz|tgz|h)$`)

// Bundle-wide scan limits: update endpoints live in dylibs, plug-ins and
// JavaScript bundles as often as in the main executable, but some bundles
// hold gigabytes of binaries.
const (
	scanMaxFiles   = 300
	scanMaxTotal   = 384 << 20
	scanMaxPerFile = 64 << 20
	scanMaxURLs    = 20
)

var scanDirs = []string{"Contents/MacOS", "Contents/Frameworks", "Contents/PlugIns", "Contents/Library", "Contents/Helpers", "Contents/Resources"}
var scanTextExt = map[string]bool{".dylib": true, ".so": true, ".js": true, ".json": true, ".xml": true, ".yml": true, ".yaml": true, ".ini": true, ".cfg": true, ".txt": true, ".strings": true}

// scanBundleURLs streams the main executable and then other binaries and
// text resources, ranking URLs by whether their host or path names the
// vendor or product. Nothing is executed.
func scanBundleURLs(r *Report, path, exe string) {
	tokens := affinityTokens(r)
	main := filepath.Join(path, "Contents", "MacOS", exe)
	files := []string{main}
	var total int64
	for _, dir := range scanDirs {
		base := filepath.Join(path, filepath.FromSlash(dir))
		filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
			if err != nil || len(files) >= scanMaxFiles {
				return filepath.SkipDir
			}
			if d.IsDir() {
				if strings.Count(strings.TrimPrefix(p, base), string(os.PathSeparator)) > 5 {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 || p == main {
				return nil
			}
			info, e := d.Info()
			if e != nil || info.Size() < 1024 {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(p))
			if !scanTextExt[ext] && info.Mode()&0o111 == 0 && (ext != "" || !strings.Contains(p, "/Frameworks/")) {
				return nil
			}
			files = append(files, p)
			return nil
		})
	}
	seen := map[string]bool{}
	found := 0
	for _, f := range files {
		if total >= scanMaxTotal || found >= scanMaxURLs {
			break
		}
		urls, err := scanExecutableURLs(f, scanMaxPerFile)
		if err != nil {
			continue
		}
		if info, e := os.Stat(f); e == nil {
			total += info.Size()
		}
		source := "executable"
		if f != main {
			source = "binary:" + filepath.Base(f)
		}
		for _, u := range urls {
			if seen[u.URL] {
				continue
			}
			seen[u.URL] = true
			score, ok := rankBundleURL(u.URL, tokens)
			if !ok {
				continue
			}
			add(r, u.URL, source, score)
			found++
		}
	}
}

// rankBundleURL scores a URL found in a binary. Affinity with the vendor or
// product outranks keywords; URLs with neither affinity nor a strong
// update keyword, or from well-known third-party hosts, are dropped.
func rankBundleURL(u string, tokens []string) (int, bool) {
	if dependencyPaths.MatchString(u) {
		return 0, false
	}
	affinity := hasAffinity(u, tokens)
	strong := strongKeywords.MatchString(u)
	switch {
	case affinity && strong:
		return 80, true
	case affinity:
		return 55, true
	case noisyHost(u):
		return 0, false
	case strong:
		return 60, true
	}
	return 35, true
}

var templateMarkers = []string{"%@", "%s", "%d", "%l", "%u", "{", "}"}

// scanExecutableURLs streams the main executable looking for literal HTTPS
// URLs. Nothing is executed; only update-flavoured URLs are kept.
func scanExecutableURLs(path string, max int) ([]Candidate, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rd := bufio.NewReaderSize(io.LimitReader(f, int64(max)), 1<<20)
	seen := map[string]bool{}
	var out []Candidate
	needle := []byte("https://")
	for len(out) < 20 {
		if _, err := rd.ReadSlice('h'); err != nil {
			if err == bufio.ErrBufferFull {
				continue
			}
			break
		}
		rest, _ := rd.Peek(len(needle) + 300)
		if !bytes.HasPrefix(rest, needle[1:]) {
			continue
		}
		end := 0
		for end < len(rest) && end < 300 && urlByte[rest[end]] {
			end++
		}
		u := "h" + string(rest[:end])
		if end < 12 || seen[u] || !updateKeywords.MatchString(u) {
			continue
		}
		seen[u] = true
		skip := false
		for _, m := range templateMarkers {
			if strings.Contains(u, m) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		out = append(out, Candidate{URL: u, Source: "executable"})
	}
	return out, nil
}
