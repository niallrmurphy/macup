package inventory

// SparkleFeedFinder, when set, supplies a feed URL for bundles whose
// Info.plist has no SUFeedURL (feeds set in code or in user defaults). It
// must not perform network requests.
var SparkleFeedFinder func(Application) string

// UpdaterInfo describes who updates an application that no provider checked.
type UpdaterInfo struct {
	Status string // SELF-UPDATING or MANAGED
	Source string // mechanism or manager, shown in the UPDATE SOURCE column
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
		if e.Status != "UNCHECKED" || e.Path == "" {
			continue
		}
		a, ok := byPath[identityPath(e.Path)]
		if !ok {
			continue
		}
		u := find(a)
		if u.Status == "" {
			continue
		}
		e.Status = u.Status
		e.UpdateSource = u.Source
	}
}
