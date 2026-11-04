package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/niallrmurphy/macup/internal/inventory"
)

// manifestName is the file in each staging directory that records what the
// enclosure beside it is for, so a later run can tell whether it is still
// wanted without parsing the directory name.
const manifestName = "macup.json"

// unmanifestedAge is how long a staging directory without a manifest (left by
// a macup that predates them, or by something else) survives before cleanup.
const unmanifestedAge = 30 * 24 * time.Hour

type manifest struct {
	Name      string `json:"name"`
	Available string `json:"available"`
	URL       string `json:"url,omitempty"`
	Staged    string `json:"staged"`
}

func writeManifest(dir string, a Action) {
	raw, err := json.Marshal(manifest{Name: a.Name, Available: a.Available, URL: a.URL, Staged: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, manifestName), raw, 0o644)
}

// CleanStaging removes staged downloads that can no longer be reused. One is
// kept only while the inventory still names its version as the next one to
// install for that app: it goes once the app is at that version or reported
// CURRENT, the appcast has moved on to a newer one, or the app is gone. An
// app with no verdict this run (probe failed, feed missing) keeps its download,
// and a directory whose manifest is unreadable is left alone until it is
// unmanifestedAge old, since nothing can say what it was for. It returns one
// line per removal, sorted, with the bytes freed.
func CleanStaging(after inventory.Result, now time.Time) ([]string, int64) {
	root := stagingDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, 0
	}
	byName := map[string]inventory.Entry{}
	for _, e := range after.Entries {
		byName[e.Name] = e
	}
	var removed []string
	var freed int64
	for _, d := range entries {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(root, d.Name())
		raw, err := os.ReadFile(filepath.Join(dir, manifestName))
		var m manifest
		if err != nil || json.Unmarshal(raw, &m) != nil || m.Name == "" || m.Available == "" {
			info, err := d.Info()
			if err != nil || now.Sub(info.ModTime()) < unmanifestedAge {
				continue
			}
			freed += dirSize(dir)
			if os.RemoveAll(dir) == nil {
				removed = append(removed, d.Name()+" (no record of what it was for, older than 30 days)")
			}
			continue
		}
		why := ""
		e, ok := byName[m.Name]
		switch {
		case !ok:
			why = "app no longer found"
		case e.Installed == m.Available || e.Status == "CURRENT":
			why = "now installed"
		case e.Available != "" && e.Available != m.Available:
			why = "superseded by " + e.Available
		default:
			// Still the next version to install: a reuse candidate.
			continue
		}
		freed += dirSize(dir)
		if os.RemoveAll(dir) == nil {
			removed = append(removed, m.Name+" "+m.Available+" ("+why+")")
		}
	}
	sort.Strings(removed)
	return removed, freed
}

func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}
