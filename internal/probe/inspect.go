package probe

import (
	"context"
	"runtime"
	"strings"
	"sync"

	"github.com/niallrmurphy/macup/internal/inventory"
)

// Summary is the network-free part of a probe, for use by the scan: a
// Sparkle feed the bundle hides outside Info.plist, and who updates the app.
type Summary struct {
	SparkleFeed string
	Updater     string // embedded mechanism: sparkle, squirrel, electron-updater, keystone, mozilla, paddle, devmate
	ManagedBy   string // appstore, vendor:<label>, browser, parent, setapp
}

var inspectCache sync.Map

// inspectReport runs the network-free stages once per path for the life of
// the process; the scan asks several times (Sparkle feed, updater label,
// lookups).
func inspectReport(ctx context.Context, path string) *Report {
	if v, ok := inspectCache.Load(path); ok {
		return v.(*Report)
	}
	r, fields, err := discover(path)
	if err == nil {
		inspectBundle(ctx, &r, fields)
		inspectLocal(ctx, &r, fields)
		classify(&r, fields)
	}
	inspectCache.Store(path, &r)
	return &r
}

// Inspect reads bundle contents and local OS records only.
func Inspect(ctx context.Context, path string) Summary {
	r := *inspectReport(ctx, path)
	s := Summary{}
	if r.hasFamily("sparkle") {
		s.SparkleFeed = chooseAppcast(r.Candidates)
	}
	if r.Management != nil {
		switch r.Management.Kind {
		case "self":
			s.Updater = strings.TrimPrefix(r.Management.Detail, "embedded updater: ")
		case "appstore", "setapp":
			s.ManagedBy = r.Management.Kind
		case "pwa":
			s.ManagedBy = "browser"
		case "helper":
			s.ManagedBy = "parent"
		case "vendor":
			s.ManagedBy = "vendor:" + strings.TrimPrefix(r.Management.Detail, "vendor updater job ")
		}
	}
	return s
}

var archTokens = map[string][]string{
	"arm64": {"arm64", "aarch64", "applesilicon", "apple-silicon"},
	"amd64": {"intel64", "x86_64", "x64", "intel", "amd64"},
}

// chooseAppcast picks the strongest appcast-looking candidate, preferring one
// that names the running architecture when a vendor publishes several.
func chooseAppcast(cs []Candidate) string {
	var feeds []Candidate
	for _, c := range cs {
		if c.Score < 80 {
			continue
		}
		lower := strings.ToLower(c.URL)
		if strings.HasPrefix(c.Source, "defaults:") || strings.Contains(lower, "appcast") || strings.Contains(lower, "sparkle") || strings.HasSuffix(lower, ".xml") {
			feeds = append(feeds, c)
		}
	}
	if len(feeds) == 0 {
		return ""
	}
	mine := archTokens[runtime.GOARCH]
	var other []string
	for arch, toks := range archTokens {
		if arch != runtime.GOARCH {
			other = append(other, toks...)
		}
	}
	for _, c := range feeds {
		for _, t := range mine {
			if strings.Contains(strings.ToLower(c.URL), t) {
				return c.URL
			}
		}
	}
	for _, c := range feeds {
		lower := strings.ToLower(c.URL)
		foreign := false
		for _, t := range other {
			if strings.Contains(lower, t) {
				foreign = true
			}
		}
		if !foreign {
			return c.URL
		}
	}
	return ""
}

// SparkleFeed is the scan's fallback when Info.plist has no SUFeedURL.
func SparkleFeed(app inventory.Application) string {
	return Inspect(context.Background(), app.Path).SparkleFeed
}

// LookupVersions runs the keyed public lookups for one application and
// returns them in the scan's terms. Network access; caller opts in.
func LookupVersions(ctx context.Context, app inventory.Application) ([]inventory.LookupVerdict, []string) {
	r := *inspectReport(ctx, app.Path)
	r.Lookups, r.Warnings = nil, nil
	if r.Application.Path == "" {
		return nil, nil
	}
	lookups(ctx, &r)
	var out []inventory.LookupVerdict
	for _, l := range r.Lookups {
		out = append(out, inventory.LookupVerdict{Source: l.Source, Version: l.Version, Comparison: l.Comparison, URL: l.URL, Note: l.Note})
	}
	return out, r.Warnings
}

// Updater labels an application nothing else could check.
func Updater(app inventory.Application) inventory.UpdaterInfo {
	s := Inspect(context.Background(), app.Path)
	switch {
	case s.Updater != "":
		return inventory.UpdaterInfo{Status: "SELF-UPDATING", Source: s.Updater}
	case s.ManagedBy != "":
		return inventory.UpdaterInfo{Status: "MANAGED", Source: s.ManagedBy}
	}
	return inventory.UpdaterInfo{}
}
