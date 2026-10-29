package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/niallrmurphy/macup/internal/inventory"
)

// lookups performs keyed public queries that need no maintained database:
// the App Store catalogue by bundle ID, the Homebrew cask index by app name,
// and release feeds derived from evidence already in the report.
func lookups(ctx context.Context, r *Report) {
	installed := r.Application.Version
	if r.Application.BundleID != "" {
		if l, err := lookupITunes(ctx, r.Application.BundleID, installed); err != nil {
			r.warn("itunes lookup: %s", err.Error())
		} else if l != nil {
			r.Lookups = append(r.Lookups, *l)
			if l.Note != "" {
				add(r, l.Note, "lookup:itunes:sellerUrl", 55)
			}
		}
	}
	if l, c, err := lookupCask(ctx, r.Application, installed); err != nil {
		r.warn("cask lookup: %s", err.Error())
	} else if l != nil {
		r.Lookups = append(r.Lookups, *l)
		// A cask's download or homepage often names the project's forge (kitty,
		// cool-retro-term), which gives a release feed for free.
		deriveForgeFeeds(r, c.URL)
		deriveForgeFeeds(r, c.Homepage)
	}
	done := map[string]bool{}
	for _, c := range r.Candidates {
		kind := feedKind(c)
		if kind == "" || done[kind] {
			continue
		}
		done[kind] = true
		l, err := lookupFeed(ctx, kind, c.URL, installed)
		if err != nil {
			r.warn("%s: %s", kind, err.Error())
			continue
		}
		if l != nil {
			r.Lookups = append(r.Lookups, *l)
		}
	}
	if !r.hasVersionedLookup() {
		genericLookups(ctx, r, installed)
	}
}

func (r *Report) hasVersionedLookup() bool {
	for _, l := range r.Lookups {
		if l.Version != "" {
			return true
		}
	}
	return false
}

// genericLookups runs only when nothing keyed or well-formed answered. It
// interprets the best vendor-affine candidates by content shape, tries
// well-known version files beside directory URLs, and HEADs download links.
// Requests are bounded so the long tail cannot turn into a crawl.
func genericLookups(ctx context.Context, r *Report, installed string) {
	tokens := affinityTokens(r)
	const maxRequests = 8
	requests := 0
	tried := map[string]bool{}
	try := func(u string, f func(context.Context, string) (*Lookup, error)) bool {
		if requests >= maxRequests || tried[u] {
			return false
		}
		tried[u] = true
		requests++
		// Vendor endpoints found in binaries are often parameterised or dead;
		// a short deadline keeps one of them from spending the whole budget.
		rctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		l, err := f(rctx, u)
		if err != nil {
			r.warn("generic lookup %s: %s", u, err.Error())
			return false
		}
		if l != nil {
			r.Lookups = append(r.Lookups, *l)
			return l.Version != ""
		}
		return false
	}
	var affine []Candidate
	for _, c := range r.Candidates {
		if c.Score >= 55 && feedKind(c) == "" && hasAffinity(c.URL, tokens) {
			affine = append(affine, c)
		}
	}
	for _, c := range affine {
		if installerExtURL.MatchString(c.URL) {
			if try(c.URL, func(c context.Context, u string) (*Lookup, error) { return headDownload(c, u, installed) }) {
				return
			}
			continue
		}
		if try(c.URL, func(c context.Context, u string) (*Lookup, error) { return interpretGeneric(c, u, installed, tokens) }) {
			return
		}
	}
	// Explicit directories (trailing slash) first; bare hosts are a weaker guess.
	var dirs []string
	for _, c := range affine {
		if directoryLike(c.URL) && strings.HasSuffix(c.URL, "/") {
			dirs = append(dirs, c.URL)
		}
	}
	for _, c := range affine {
		if directoryLike(c.URL) && !strings.HasSuffix(c.URL, "/") {
			dirs = append(dirs, c.URL)
		}
	}
	if len(dirs) > 2 {
		dirs = dirs[:2]
	}
	for _, d := range dirs {
		base := strings.TrimRight(d, "/") + "/"
		for _, f := range wellKnownFiles {
			if try(base+f, func(c context.Context, u string) (*Lookup, error) { return interpretGeneric(c, u, installed, tokens) }) {
				return
			}
		}
	}
}

// feedKind maps a candidate to a parser we can run without page scraping.
func feedKind(c Candidate) string {
	switch {
	case strings.Contains(c.URL, "github.com/") && strings.HasSuffix(c.URL, "/releases.atom"):
		return "github-releases"
	case strings.Contains(c.URL, "gitlab.com/") && strings.HasSuffix(c.URL, "/-/tags?format=atom"):
		return "gitlab-tags"
	case strings.HasPrefix(c.URL, "https://sourceforge.net/projects/") && strings.Contains(c.URL, "/rss"):
		return "sourceforge-files"
	case c.Source == "application.ini:AppUpdate":
		return "mozilla-aus"
	case strings.HasPrefix(c.Source, "electron-updater") && strings.HasSuffix(c.URL, "-mac.yml"):
		return "electron-updater"
	case strings.HasPrefix(c.Source, "Info.plist:SUFeedURL") || strings.HasPrefix(c.Source, "defaults:SUFeedURL") || (c.Source == "executable" && strings.Contains(strings.ToLower(c.URL), "appcast")):
		return "sparkle-appcast"
	}
	return ""
}

func lookupFeed(ctx context.Context, kind, feed, installed string) (*Lookup, error) {
	body, _, final, err := fetchBytesFinal(ctx, feed, "application/atom+xml, application/rss+xml, application/xml, text/xml, text/yaml, */*", 2<<20)
	if err != nil {
		return nil, err
	}
	l := &Lookup{Source: kind, URL: feed}
	if final != feed {
		// A renamed repository or moved host still answers, but the reader
		// should know the evidence came from somewhere other than the bundle said.
		l.Note = "redirected to " + final
	}
	switch kind {
	case "github-releases", "gitlab-tags":
		title, updated, link, err := latestAtomEntry(body)
		if err != nil {
			return nil, err
		}
		l.Version, l.Released = title, updated
		if link != "" {
			l.URL = link
		}
		if prerelease.MatchString(title) {
			l.Note = strings.TrimPrefix(l.Note+"; pre-release", "; ")
		}
	case "sourceforge-files":
		l.Version, l.Released, l.URL, err = latestSourceForgeFile(body)
		if err != nil {
			return nil, err
		}
	case "mozilla-aus":
		v, ok := mozillaOffered(body)
		if !ok {
			l.Comparison = "none-offered"
			l.Note = "update server offered no update for this exact build"
			return l, nil
		}
		l.Version = v
	case "electron-updater":
		l.Version, l.Released = electronLatest(body)
	case "sparkle-appcast":
		l.Version, l.Released = latestAppcastItem(body)
	}
	if l.Version == "" {
		return nil, errors.New("no version found in feed")
	}
	l.Comparison = comparison(installed, l.Version)
	return l, nil
}

// --- iTunes Search API: public, keyless, keyed on bundle identifier ---

func lookupITunes(ctx context.Context, bundleID, installed string) (*Lookup, error) {
	body, _, err := fetchBytes(ctx, "https://itunes.apple.com/lookup?bundleId="+url.QueryEscape(bundleID)+"&entity=macSoftware", "application/json", 1<<20)
	if err != nil {
		return nil, err
	}
	return parseITunes(body, installed)
}

func parseITunes(body []byte, installed string) (*Lookup, error) {
	var resp struct {
		Results []struct {
			Kind         string `json:"kind"`
			Version      string `json:"version"`
			ReleaseDate  string `json:"currentVersionReleaseDate"`
			TrackViewURL string `json:"trackViewUrl"`
			SellerURL    string `json:"sellerUrl"`
			TrackName    string `json:"trackName"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	// A bundle identifier can name an iOS app too (Spotify's is shared); only a
	// Mac listing says anything about the Mac build.
	for _, x := range resp.Results {
		if x.Kind != "mac-software" {
			continue
		}
		return &Lookup{Source: "itunes", Version: x.Version, Released: x.ReleaseDate, URL: x.TrackViewURL, Comparison: comparison(installed, x.Version), Note: x.SellerURL}, nil
	}
	return nil, nil
}

// --- Homebrew cask index: borrowed, not maintained. Cached for a day. ---

type cask struct {
	Token     string            `json:"token"`
	Names     []string          `json:"name"`
	Version   string            `json:"version"`
	URL       string            `json:"url"`
	Homepage  string            `json:"homepage"`
	Artifacts []json.RawMessage `json:"artifacts"`
}

type caskIndex struct {
	byApp    map[string][]*cask
	byBundle map[string][]*cask
}

var (
	caskOnce  sync.Mutex
	caskCache *caskIndex
	caskErr   error
)

var caskIndexURL = "https://formulae.brew.sh/api/cask.json"
var caskCacheFile = func() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "macup", "cask.json")
}

func loadCaskIndex(ctx context.Context) (*caskIndex, error) {
	caskOnce.Lock()
	defer caskOnce.Unlock()
	if caskCache != nil || caskErr != nil {
		return caskCache, caskErr
	}
	var raw []byte
	file := caskCacheFile()
	if file != "" {
		if st, err := os.Stat(file); err == nil && time.Since(st.ModTime()) < 24*time.Hour {
			raw, _ = os.ReadFile(file)
		}
	}
	if raw == nil {
		var err error
		raw, _, err = fetchBytes(ctx, caskIndexURL, "application/json", 96<<20)
		if err != nil {
			caskErr = err
			return nil, err
		}
		if file != "" {
			_ = os.MkdirAll(filepath.Dir(file), 0o755)
			_ = os.WriteFile(file, raw, 0o644)
		}
	}
	caskCache, caskErr = buildCaskIndex(raw)
	return caskCache, caskErr
}

func buildCaskIndex(raw []byte) (*caskIndex, error) {
	var casks []*cask
	if err := json.Unmarshal(raw, &casks); err != nil {
		return nil, err
	}
	idx := &caskIndex{byApp: map[string][]*cask{}, byBundle: map[string][]*cask{}}
	for _, c := range casks {
		for _, a := range c.Artifacts {
			var art map[string]json.RawMessage
			if json.Unmarshal(a, &art) != nil {
				continue
			}
			for _, name := range rawStrings(art["app"]) {
				if strings.HasSuffix(name, ".app") {
					idx.byApp[strings.ToLower(name)] = append(idx.byApp[strings.ToLower(name)], c)
				}
			}
			for _, key := range []string{"uninstall", "zap"} {
				var steps []map[string]json.RawMessage
				if json.Unmarshal(art[key], &steps) != nil {
					continue
				}
				for _, s := range steps {
					for _, b := range rawStrings(s["quit"]) {
						idx.byBundle[strings.ToLower(b)] = append(idx.byBundle[strings.ToLower(b)], c)
					}
				}
			}
		}
	}
	return idx, nil
}

// rawStrings accepts a string, or an array mixing strings and objects.
func rawStrings(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var out []string
	for _, it := range items {
		if json.Unmarshal(it, &s) == nil {
			out = append(out, s)
		}
	}
	return out
}

func lookupCask(ctx context.Context, app inventory.Application, installed string) (*Lookup, *cask, error) {
	idx, err := loadCaskIndex(ctx)
	if err != nil {
		return nil, nil, err
	}
	c, note := idx.match(app)
	if c == nil {
		return nil, nil, nil
	}
	return caskLookup(c, note, installed), c, nil
}

// caskLookup reports the cask's version against the installed one. The token
// is exposed as Package only for a clean match, meaning exactly one cask
// ships an app with this filename. A tie between casks broken by heuristic,
// or a pkg cask matched only because its uninstall stanza quits this bundle
// id (Elgato Camera Hub quits DisplayLink Manager, which it bundles), is good
// enough to report a version, not to choose what gets installed.
func caskLookup(c *cask, note, installed string) *Lookup {
	version := c.Version
	if i := strings.Index(version, ","); i > 0 {
		version = version[:i]
	}
	l := &Lookup{Source: "homebrew-cask", URL: c.URL, Note: "cask " + c.Token + note}
	if note == "" {
		l.Package = c.Token
	}
	if version == "latest" || version == "" {
		l.Comparison = "unknown"
		l.Note += "; cask is unversioned"
		return l
	}
	l.Version = version
	l.Comparison = comparison(installed, version)
	return l
}

// match prefers an app filename hit, using the bundle identifier to break
// ties. A bundle-only hit is accepted only for casks that install no .app
// themselves (pkg-based casks), since uninstall stanzas may quit unrelated apps.
func (idx *caskIndex) match(app inventory.Application) (*cask, string) {
	bid := strings.ToLower(app.BundleID)
	cs := idx.byApp[strings.ToLower(filepath.Base(app.Path))]
	if len(cs) > 1 && bid != "" {
		var narrowed []*cask
		for _, c := range cs {
			if c.quits(bid) {
				narrowed = append(narrowed, c)
			}
		}
		if len(narrowed) > 0 {
			cs = narrowed
		}
	}
	switch len(cs) {
	case 1:
		return cs[0], ""
	case 0:
		if bid == "" {
			return nil, ""
		}
		for _, c := range idx.byBundle[bid] {
			if !c.installsApp() {
				return c, " (matched by bundle identifier)"
			}
		}
		return nil, ""
	}
	// Several casks ship the same app name (stable, beta, nightly). Prefer the shortest token.
	best := cs[0]
	for _, c := range cs[1:] {
		if len(c.Token) < len(best.Token) {
			best = c
		}
	}
	return best, fmt.Sprintf(" (ambiguous: %d casks ship this app name)", len(cs))
}

func (c *cask) installsApp() bool {
	for _, a := range c.Artifacts {
		var art map[string]json.RawMessage
		if json.Unmarshal(a, &art) == nil && len(rawStrings(art["app"])) > 0 {
			return true
		}
	}
	return false
}

func (c *cask) quits(bid string) bool {
	for _, a := range c.Artifacts {
		var art map[string]json.RawMessage
		if json.Unmarshal(a, &art) != nil {
			continue
		}
		for _, key := range []string{"uninstall", "zap"} {
			var steps []map[string]json.RawMessage
			if json.Unmarshal(art[key], &steps) != nil {
				continue
			}
			for _, st := range steps {
				for _, b := range rawStrings(st["quit"]) {
					if strings.EqualFold(b, bid) {
						return true
					}
				}
			}
		}
	}
	return false
}
