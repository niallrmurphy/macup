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

// Inspect reads bundle contents and local OS records only. Results are
// cached per path for the life of the process because the scan asks twice:
// once for the Sparkle feed, once for the updater label.
func Inspect(ctx context.Context, path string) Summary {
	if v, ok := inspectCache.Load(path); ok {
		return v.(Summary)
	}
	s := inspect(ctx, path)
	inspectCache.Store(path, s)
	return s
}

func inspect(ctx context.Context, path string) Summary {
	r, fields, err := discover(path)
	if err != nil {
		return Summary{}
	}
	inspectBundle(ctx, &r, fields)
	inspectLocal(ctx, &r, fields)
	classify(&r, fields)
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
