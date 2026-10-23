package inventory

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// AppStorePackage is an application identified by the mas CLI.
type AppStorePackage struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Installed        string `json:"installed"`
	AvailableVersion string `json:"available_version,omitempty"`
	Outdated         bool   `json:"outdated"`
}

type AppStoreResult struct {
	Available bool              `json:"available"`
	Packages  []AppStorePackage `json:"packages"`
	Warnings  []string          `json:"warnings"`
}

type MasCommand func(context.Context, ...string) ([]byte, error)

func commandMas(ctx context.Context, args ...string) ([]byte, error) {
	path, err := exec.LookPath("mas")
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	return exec.CommandContext(cctx, path, args...).Output()
}

// mas list: 497799835 Xcode (16.3)
// mas outdated: 497799835 Xcode (16.3 -> 16.4)
var masRow = regexp.MustCompile(`^\s*(\d+)\s+(.+?)\s+\(([^()]*)\)\s*$`)

func parseMasList(data []byte) []AppStorePackage {
	var out []AppStorePackage
	for _, line := range strings.Split(string(data), "\n") {
		match := masRow.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		out = append(out, AppStorePackage{ID: match[1], Name: match[2], Installed: strings.TrimSpace(match[3])})
	}
	return out
}

func ScanAppStore(ctx context.Context) AppStoreResult { return ScanAppStoreWith(ctx, commandMas) }
func ScanAppStoreWith(ctx context.Context, command MasCommand) AppStoreResult {
	r := AppStoreResult{Packages: []AppStorePackage{}, Warnings: []string{}}
	if command == nil {
		command = commandMas
	}
	data, err := command(ctx, "list")
	if err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("mas list: %v (install mas to enable App Store inventory)", err))
		return r
	}
	r.Available = true
	r.Packages = parseMasList(data)
	index := map[string]int{}
	for i, p := range r.Packages {
		index[p.ID] = i
	}
	data, err = command(ctx, "outdated")
	if err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("mas outdated: %v", err))
	} else {
		for _, line := range strings.Split(string(data), "\n") {
			match := masRow.FindStringSubmatch(line)
			if match == nil {
				continue
			}
			if i, ok := index[match[1]]; ok {
				r.Packages[i].Outdated = true
				versions := strings.SplitN(match[3], "->", 2)
				if len(versions) == 2 {
					r.Packages[i].AvailableVersion = strings.TrimSpace(versions[1])
				}
			}
		}
	}
	sort.Slice(r.Packages, func(i, j int) bool { return strings.ToLower(r.Packages[i].Name) < strings.ToLower(r.Packages[j].Name) })
	return r
}
