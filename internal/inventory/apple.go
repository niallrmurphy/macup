package inventory

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const softwareUpdatePath = "/usr/sbin/softwareupdate"

// AppleUpdate is one item from `softwareupdate --list`: a macOS update, the
// Xcode Command Line Tools, a Safari or other Apple-published update.
type AppleUpdate struct {
	Label       string `json:"label"`
	Title       string `json:"title"`
	Version     string `json:"version,omitempty"`
	Size        string `json:"size,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
	Restart     bool   `json:"restart,omitempty"` // installing it needs a restart or shutdown
}

type AppleResult struct {
	Available   bool          `json:"available"`
	CheckStatus string        `json:"check_status"` // checked, failed, unavailable
	Updates     []AppleUpdate `json:"updates"`
	Warnings    []string      `json:"warnings"`
}

type SoftwareUpdateCommand func(context.Context) ([]byte, error)

// Running --list is read-only. Never invoke --install from the scan.
func commandSoftwareUpdate(ctx context.Context) ([]byte, error) {
	info, err := os.Stat(softwareUpdatePath)
	if err != nil {
		return nil, err
	}
	if info.IsDir() || info.Mode()&0111 == 0 {
		return nil, fmt.Errorf("softwareupdate is not executable")
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(cctx, softwareUpdatePath, "--list").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("softwareupdate --list failed: %w (%s)", err, diagnosticSnippet(out))
	}
	return out, nil
}

func ScanApple(ctx context.Context) AppleResult {
	return ScanAppleWith(ctx, commandSoftwareUpdate)
}

func ScanAppleWith(ctx context.Context, command SoftwareUpdateCommand) AppleResult {
	r := AppleResult{CheckStatus: "unavailable", Updates: []AppleUpdate{}, Warnings: []string{}}
	if command == nil {
		command = commandSoftwareUpdate
	}
	raw, err := command(ctx)
	if err != nil {
		if os.IsNotExist(err) {
			return r
		}
		r.CheckStatus = "failed"
		r.Warnings = append(r.Warnings, err.Error())
		return r
	}
	updates, err := parseAppleUpdates(raw)
	if err != nil {
		r.CheckStatus = "failed"
		r.Warnings = append(r.Warnings, "softwareupdate output parsing: "+err.Error())
		return r
	}
	r.Available = true
	r.CheckStatus = "checked"
	r.Updates = updates
	return r
}

var (
	appleLabelLine = regexp.MustCompile(`^\s*\*\s*Label:\s*(.+?)\s*$`)
	appleField     = regexp.MustCompile(`(Title|Version|Size|Recommended|Action):\s*([^,]*)`)
)

// parseAppleUpdates reads the listing printed by `softwareupdate --list`:
//
//   - Label: Command Line Tools for Xcode-16.2
//     Title: Command Line Tools for Xcode, Version: 16.2, Size: 713258KiB, Recommended: YES,
//
// "No new software available." is the one terminal zero-update state. Anything
// else that has no Label line is an unknown format and is an error, never a
// silent "no updates".
func parseAppleUpdates(raw []byte) ([]AppleUpdate, error) {
	updates := []AppleUpdate{}
	var cur *AppleUpdate
	noNew := false
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r", "\n"), "\n") {
		if m := appleLabelLine.FindStringSubmatch(line); m != nil {
			updates = append(updates, AppleUpdate{Label: m[1]})
			cur = &updates[len(updates)-1]
			continue
		}
		if strings.Contains(strings.ToLower(line), "no new software available") {
			noNew = true
			continue
		}
		if cur == nil || !strings.Contains(line, "Title:") {
			continue
		}
		for _, f := range appleField.FindAllStringSubmatch(line, -1) {
			v := strings.TrimSpace(f[2])
			switch f[1] {
			case "Title":
				cur.Title = v
			case "Version":
				cur.Version = v
			case "Size":
				cur.Size = v
			case "Recommended":
				cur.Recommended = strings.EqualFold(v, "YES")
			case "Action":
				a := strings.ToLower(v)
				cur.Restart = strings.Contains(a, "restart") || strings.Contains(a, "shut down")
			}
		}
		cur = nil
	}
	for i := range updates {
		if updates[i].Title == "" {
			updates[i].Title = updates[i].Label
		}
	}
	if len(updates) == 0 && !noNew {
		return nil, fmt.Errorf("unrecognised output: %s", diagnosticSnippet(raw))
	}
	return updates, nil
}

// ApplyApple adds each pending Apple update as an OUTDATED entry of its own:
// these are not application bundles Reconcile could have matched.
func ApplyApple(r *Result) {
	if r.Apple == nil || !r.Apple.Available {
		return
	}
	for _, u := range r.Apple.Updates {
		r.Entries = append(r.Entries, Entry{
			Name:         u.Title,
			Available:    u.Version,
			Status:       "OUTDATED",
			UpdateSource: "softwareupdate",
			Sources:      []string{"softwareupdate"},
			Detail:       "label:" + u.Label,
		})
	}
}
