package inventory

import (
	"sort"
	"strings"
)

// SparkleFeedFinder, when set, supplies a feed URL for bundles whose
// Info.plist has no SUFeedURL (feeds set in code or in user defaults). It
// must not perform network requests.
var SparkleFeedFinder func(Application) string

// UpdaterInfo describes who updates an application that no provider checked.
type UpdaterInfo struct {
	Status string // SELF-UPDATING or MANAGED
	Source string // mechanism or manager, shown in the UPDATE SOURCE column
	Detail string // optional evidence, such as the update endpoint found
}

// ApplyUpdaters relabels UNCHECKED application entries whose bundle carries
// an embedded updater or is managed by another party. It never changes an
// entry a provider has already evaluated.
func ApplyUpdaters(r *Result, find func(Application) UpdaterInfo) {
	if find == nil {
		return
	}
	byPath := map[string]Application{}
	for _, a := range r.Applications {
		byPath[identityPath(a.Path)] = a
	}
	for i := range r.Entries {
		e := &r.Entries[i]
		if e.Status != "NO SOURCE" || e.Path == "" {
			continue
		}
		a, ok := byPath[identityPath(e.Path)]
		if !ok {
			continue
		}
		u := find(a)
		if a.Platform == "ios" {
			// iPhone and iPad apps on Apple silicon are installed and updated only by the App Store.
			u = UpdaterInfo{Status: "MANAGED", Source: "appstore"}
		}
		if u.Status == "" {
			continue
		}
		e.Status = u.Status
		e.UpdateSource = u.Source
		if u.Detail != "" {
			e.Detail = u.Detail
		}
	}
}

// LookupVerdict is one keyed public lookup result in the scan's terms.
type LookupVerdict struct {
	Source     string `json:"source"`
	Version    string `json:"version,omitempty"`
	Comparison string `json:"comparison"`
	URL        string `json:"url,omitempty"`
	Note       string `json:"note,omitempty"`
}

// lookupPriority orders sources by how directly they speak for the vendor.
// Catalogues (Homebrew, App Store) outrank feeds derived from a repository
// name found in a binary, which can belong to a dependency or a namesake.
var lookupPriority = []string{"electron-updater", "sparkle-appcast", "mozilla-aus", "homebrew-cask", "itunes", "github-releases", "gitlab-tags", "sourceforge-files", "generic:", "download-head"}

func lookupRank(source string) int {
	for i, p := range lookupPriority {
		if source == p || (strings.HasSuffix(p, ":") && strings.HasPrefix(source, p)) {
			return i
		}
	}
	return len(lookupPriority)
}

// LookupEligible says whether an entry still needs an answer: nothing a
// provider already evaluated, and nothing updated by a parent or browser.
func LookupEligible(e Entry) bool {
	if len(e.Sources) != 1 || e.Sources[0] != "apps" || e.Path == "" {
		return false
	}
	switch e.Status {
	case "OUTDATED", "CURRENT", "NOT FLAGGED", "UPGRADE AVAILABLE":
		return false
	case "MANAGED":
		return e.UpdateSource != "parent" && e.UpdateSource != "browser"
	}
	return true
}

// ApplyLookups folds keyed lookup results into entries that no provider
// evaluated. A source reporting a newer version makes the entry OUTDATED;
// failing that, a source reporting the same version makes it NOT FLAGGED;
// anything else is recorded in Detail without changing the status.
func ApplyLookups(r *Result, verdicts map[string][]LookupVerdict) {
	for i := range r.Entries {
		e := &r.Entries[i]
		if !LookupEligible(*e) {
			continue
		}
		vs := append([]LookupVerdict{}, verdicts[identityPath(e.Path)]...)
		if len(vs) == 0 {
			continue
		}
		sort.SliceStable(vs, func(a, b int) bool { return lookupRank(vs[a].Source) < lookupRank(vs[b].Source) })
		var newer, same, any *LookupVerdict
		for j := range vs {
			v := &vs[j]
			if v.Version == "" {
				continue
			}
			switch v.Comparison {
			case "newer":
				if newer == nil {
					newer = v
				}
			case "same", "none-offered":
				if same == nil {
					same = v
				}
			}
			if any == nil {
				any = v
			}
		}
		switch {
		case newer != nil:
			e.Status = "OUTDATED"
			e.Available = newer.Version
			e.UpdateSource = newer.Source
			e.Sources = append(e.Sources, "lookup")
		case same != nil:
			e.Status = "NOT FLAGGED"
			e.UpdateSource = same.Source
			e.Sources = append(e.Sources, "lookup")
		case any != nil:
			e.Detail = "lookup: " + any.Source + " lists " + any.Version + " (" + any.Comparison + ")"
		}
	}
}
