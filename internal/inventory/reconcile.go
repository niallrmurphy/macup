package inventory

import (
	"path/filepath"
	"sort"
	"strings"
)

// Entry is a normalized inventory item. An app installed through a cask has
// both discovery sources but only one row. Raw provider records are retained.
type Entry struct {
	Name         string   `json:"name"`
	Installed    string   `json:"installed,omitempty"`
	Available    string   `json:"available,omitempty"`
	Status       string   `json:"status"`
	UpdateSource string   `json:"update_source,omitempty"`
	Sources      []string `json:"sources"`
	Path         string   `json:"path,omitempty"`
	BundleID     string   `json:"bundle_id,omitempty"`
	Package      string   `json:"package,omitempty"`
	Detail       string   `json:"detail,omitempty"`
}

// Reconcile merges cask records only if their recorded app target uniquely
// matches a discovered bundle path. No name-only or bundle-ID-only merges.
func Reconcile(r *Result) {
	entries := make([]Entry, 0, len(r.Applications))
	byPath := make(map[string][]int)
	for _, a := range r.Applications {
		idx := len(entries)
		entries = append(entries, Entry{Name: a.Name, Installed: a.Version, Status: "UNCHECKED", Sources: []string{"apps"}, Path: a.Path, BundleID: a.BundleID})
		key := identityPath(a.Path)
		byPath[key] = append(byPath[key], idx)
	}
	if r.Homebrew != nil {
		owners := map[string]int{}
		for _, p := range r.Homebrew.Packages {
			if p.Kind == "cask" && len(p.AppPaths) == 1 {
				owners[identityPath(p.AppPaths[0])]++
			}
		}
		for _, p := range r.Homebrew.Packages {
			source := "brew-" + p.Kind
			status := "CURRENT"
			if p.Outdated {
				status = "OUTDATED"
			}
			if p.Kind == "cask" && len(p.AppPaths) == 1 && owners[identityPath(p.AppPaths[0])] == 1 {
				matches := byPath[identityPath(p.AppPaths[0])]
				if len(matches) == 1 && len(entries[matches[0]].Sources) == 1 {
					e := &entries[matches[0]]
					e.Sources = append(e.Sources, source)
					e.Package = p.Name
					e.Status = status
					if p.Outdated {
						e.UpdateSource = "homebrew"
					}
					e.Available = p.Current
					// Bundle version is the observed app version, which may differ
					// from Homebrew's cask version; keep it as installed.
					continue
				}
			}
			entries = append(entries, Entry{Name: p.Name, Installed: p.Installed, Available: p.Current, Status: status, Sources: []string{source}, Package: p.Name, UpdateSource: updateSource(p.Outdated, "homebrew")})
		}
	}
	if r.AppStore != nil {
		// A receipt and a Spotlight-reported Adam ID must agree with mas.
		// Never merge on display name or bundle identifier alone.
		appIDs := make(map[string][]int)
		for i := range entries {
			if len(entries[i].Sources) == 1 && entries[i].Sources[0] == "apps" {
				for _, a := range r.Applications {
					if identityPath(a.Path) == identityPath(entries[i].Path) && a.AppStoreID != "" {
						appIDs[a.AppStoreID] = append(appIDs[a.AppStoreID], i)
					}
				}
			}
		}
		masCounts := map[string]int{}
		for _, p := range r.AppStore.Packages {
			masCounts[p.ID]++
		}
		for _, p := range r.AppStore.Packages {
			status := "CURRENT"
			if p.Outdated {
				status = "OUTDATED"
			}
			if matches := appIDs[p.ID]; masCounts[p.ID] == 1 && len(matches) == 1 {
				e := &entries[matches[0]]
				if len(e.Sources) == 1 {
					e.Sources = append(e.Sources, "appstore")
					e.Status = status
					if p.Outdated {
						e.UpdateSource = "appstore"
					}
					e.Available = p.AvailableVersion
					e.Detail = "id:" + p.ID
					continue
				}
			}
			entries = append(entries, Entry{Name: p.Name, Installed: p.Installed, Available: p.AvailableVersion, Status: status, Sources: []string{"appstore"}, Detail: "id:" + p.ID, UpdateSource: updateSource(p.Outdated, "appstore")})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := strings.ToLower(entries[i].Name), strings.ToLower(entries[j].Name)
		if a != b {
			return a < b
		}
		return strings.Join(entries[i].Sources, ",") < strings.Join(entries[j].Sources, ",")
	})
	r.Entries = entries
}

func identityPath(s string) string {
	// Clean paths; avoid resolving symlinks because the inventory scanner
	// deliberately ignores them and the destination is only expected metadata.
	return filepath.Clean(s)
}

func updateSource(outdated bool, source string) string {
	if outdated {
		return source
	}
	return ""
}
