// Package update turns scan results into a plan of update actions and, only
// when asked, executes them. Delegation routes hand the work to the package
// manager that owns the software; the Sparkle route downloads and verifies
// but never replaces an application bundle itself.
package update

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/niallrmurphy/macup/internal/inventory"
)

// Route names what the action does.
const (
	RouteBrew    = "brew"          // brew upgrade
	RouteMas     = "mas"           // mas upgrade
	RouteMAU     = "msupdate"      // Microsoft AutoUpdate --install
	RouteSparkle = "sparkle-fetch" // download enclosure, verify EdDSA, open
	RouteAdvice  = "manual"        // nothing to run; Reason says what to do
)

type Action struct {
	Name      string   `json:"name"`
	Installed string   `json:"installed,omitempty"`
	Available string   `json:"available,omitempty"`
	Route     string   `json:"route"`
	Command   []string `json:"command,omitempty"`
	URL       string   `json:"url,omitempty"`
	Signature string   `json:"ed_signature,omitempty"`
	PublicKey string   `json:"public_key,omitempty"`
	Length    int64    `json:"length,omitempty"`
	Path      string   `json:"path,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}

var masID = regexp.MustCompile(`\bid:(\d+)`)

const mauPath = "/Library/Application Support/Microsoft/MAU2.0/Microsoft AutoUpdate.app/Contents/MacOS/msupdate"

// Plan maps every OUTDATED entry to the most trustworthy route available.
func Plan(r inventory.Result) []Action {
	mauIDs := map[string]string{}
	if r.Microsoft != nil {
		for _, u := range r.Microsoft.Updates {
			mauIDs[strings.ToLower(u.Name)] = u.ID
		}
	}
	sparkle := map[string]inventory.SparkleCheck{}
	if r.Sparkle != nil {
		for _, c := range r.Sparkle.Checks {
			sparkle[c.Path] = c
		}
	}
	var plan []Action
	for _, e := range r.Entries {
		if e.Status != "OUTDATED" {
			continue
		}
		a := Action{Name: e.Name, Installed: e.Installed, Available: e.Available, Path: e.Path}
		switch {
		case has(e.Sources, "brew-formula"):
			a.Route, a.Command = RouteBrew, []string{"brew", "upgrade", "--formula", e.Name}
		case has(e.Sources, "brew-cask"):
			token := e.Package
			if token == "" {
				token = e.Name
			}
			a.Route, a.Command = RouteBrew, []string{"brew", "upgrade", "--cask", token}
		case has(e.Sources, "appstore"):
			if m := masID.FindStringSubmatch(e.Detail); m != nil {
				a.Route, a.Command = RouteMas, []string{"mas", "upgrade", m[1]}
			} else {
				a.Route, a.Reason = RouteAdvice, "App Store app without an id; update from the App Store"
			}
		case e.UpdateSource == "microsoft-autoupdate":
			if id := mauIDs[strings.ToLower(e.Name)]; id != "" {
				a.Route, a.Command = RouteMAU, []string{mauPath, "--install", "--apps", id}
			} else {
				a.Route, a.Reason = RouteAdvice, "Microsoft AutoUpdate lists no application id; open Microsoft AutoUpdate"
			}
		case e.UpdateSource == "sparkle":
			c := sparkle[e.Path]
			switch {
			case c.EnclosureURL == "":
				a.Route, a.Reason = RouteAdvice, "appcast item has no enclosure; use the app's Check for Updates"
			default:
				a.Route, a.URL, a.Signature, a.Length = RouteSparkle, c.EnclosureURL, c.EdSignature, c.EnclosureLength
				if plist, err := inventory.BundlePlist(e.Path); err == nil {
					a.PublicKey = strings.TrimSpace(plist["SUPublicEDKey"])
				}
				if a.Signature == "" || a.PublicKey == "" {
					a.Reason = "enclosure cannot be signature-verified (no EdDSA signature or SUPublicEDKey)"
				}
			}
		case e.UpdateSource == "adobe-catalog":
			a.Route, a.Reason = RouteAdvice, "update through the Creative Cloud app"
		case e.UpdateSource == "homebrew-cask" || e.UpdateSource == "itunes":
			a.Route, a.Reason = RouteAdvice, "newer version published ("+e.UpdateSource+"); install it through the vendor's channel or adopt the cask with brew"
		default:
			a.Route, a.Reason = RouteAdvice, "newer version reported by "+e.UpdateSource+"; no automated route"
		}
		plan = append(plan, a)
	}
	sort.SliceStable(plan, func(i, j int) bool {
		if plan[i].Route != plan[j].Route {
			return routeRank(plan[i].Route) < routeRank(plan[j].Route)
		}
		return strings.ToLower(plan[i].Name) < strings.ToLower(plan[j].Name)
	})
	return plan
}

func routeRank(r string) int {
	for i, x := range []string{RouteBrew, RouteMas, RouteMAU, RouteSparkle, RouteAdvice} {
		if x == r {
			return i
		}
	}
	return 99
}

func has(sources []string, s string) bool {
	for _, x := range sources {
		if x == s {
			return true
		}
	}
	return false
}

// Filter keeps actions whose name contains any of the given substrings.
func Filter(plan []Action, names []string) []Action {
	var out []Action
	for _, a := range plan {
		for _, n := range names {
			n = strings.ToLower(strings.TrimSpace(n))
			if n != "" && strings.Contains(strings.ToLower(a.Name), n) {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

// PrintPlan renders the plan as a table; nothing here has run.
func PrintPlan(w io.Writer, plan []Action) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tINSTALLED\tAVAILABLE\tROUTE\tACTION")
	counts := map[string]int{}
	for _, a := range plan {
		counts[a.Route]++
		action := a.Reason
		switch a.Route {
		case RouteBrew, RouteMas, RouteMAU:
			action = strings.Join(a.Command, " ")
		case RouteSparkle:
			action = "download " + a.URL
			if a.Reason != "" {
				action += " [" + a.Reason + "]"
			} else {
				action += " [EdDSA verified before opening]"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", a.Name, dash(a.Installed), dash(a.Available), a.Route, action)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\n%d actions: %d brew, %d mas, %d msupdate, %d sparkle downloads, %d manual\n", len(plan), counts[RouteBrew], counts[RouteMas], counts[RouteMAU], counts[RouteSparkle], counts[RouteAdvice])
	return err
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
