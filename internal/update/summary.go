package update

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/niallrmurphy/macup/internal/inventory"
)

// Summary compares the plan's outcomes with a rescan taken afterwards.
// "Opened" is not "installed": a Sparkle download the user has not yet
// dragged into place still shows as outdated, and is reported as such.
type Summary struct {
	Updated      []string
	Staged       []string // downloaded and verified, not yet installed
	StillOut     []string // ran, but the rescan still says outdated
	Skipped      []string
	Failed       []string
	NewlyOut     []string // outdated now, not in the plan (metadata moved)
	Dependencies []string // Homebrew packages that changed without being planned
}

func Summarise(outcomes []Outcome, after inventory.Result, brewBefore, brewAfter map[string]string) Summary {
	outdated := map[string]bool{}
	for _, e := range after.Entries {
		if e.Status == "OUTDATED" {
			outdated[strings.ToLower(e.Name)] = true
		}
	}
	var s Summary
	planned := map[string]bool{}
	for _, o := range outcomes {
		name := o.Action.Name
		planned[strings.ToLower(name)] = true
		if o.Action.Route == RouteBrew {
			planned[strings.ToLower(o.Action.Command[len(o.Action.Command)-1])] = true
		}
		still := outdated[strings.ToLower(name)]
		switch {
		case o.Result == "failed":
			s.Failed = append(s.Failed, name+": "+o.Err.Error())
		case o.Result == "skipped":
			s.Skipped = append(s.Skipped, name)
		case o.Result == "staged" || o.Result == "opened":
			if still {
				s.Staged = append(s.Staged, name+" "+o.Action.Available)
			} else {
				s.Updated = append(s.Updated, name)
			}
		case still:
			s.StillOut = append(s.StillOut, name)
		default:
			s.Updated = append(s.Updated, name)
		}
	}
	for _, e := range after.Entries {
		if e.Status == "OUTDATED" && !planned[strings.ToLower(e.Name)] {
			s.NewlyOut = append(s.NewlyOut, e.Name+" "+e.Available)
		}
	}
	for name, v := range brewAfter {
		if planned[strings.ToLower(name)] {
			continue
		}
		if before, ok := brewBefore[name]; !ok {
			s.Dependencies = append(s.Dependencies, name+" "+v+" (new)")
		} else if before != v {
			s.Dependencies = append(s.Dependencies, name+" "+before+" -> "+v)
		}
	}
	for name := range brewBefore {
		if _, ok := brewAfter[name]; !ok && !planned[strings.ToLower(name)] {
			s.Dependencies = append(s.Dependencies, name+" (removed)")
		}
	}
	for _, l := range [][]string{s.Updated, s.Staged, s.StillOut, s.Skipped, s.Failed, s.NewlyOut, s.Dependencies} {
		sort.Strings(l)
	}
	return s
}

func (s Summary) Print(w io.Writer) {
	section := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(w, "%s (%d):\n", title, len(items))
		for _, it := range items {
			fmt.Fprintf(w, "  %s\n", it)
		}
	}
	fmt.Fprintln(w)
	section("Updated", s.Updated)
	section("Downloaded and verified, not yet installed: open the staged file and move the app into place", s.Staged)
	section("Still outdated after running", s.StillOut)
	section("Newly outdated since the plan (Homebrew metadata moved during the run)", s.NewlyOut)
	section("Also changed by Homebrew as dependencies", s.Dependencies)
	section("Skipped (no automated route)", s.Skipped)
	section("Failed", s.Failed)
	if len(s.Updated)+len(s.Staged)+len(s.StillOut)+len(s.Failed)+len(s.Skipped) == 0 {
		fmt.Fprintln(w, "Nothing to do.")
	}
}
