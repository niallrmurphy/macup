package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BrewPackage describes an installed Homebrew formula or cask.
type BrewPackage struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Installed string   `json:"installed"`
	Current   string   `json:"current,omitempty"`
	Outdated  bool     `json:"outdated"`
	AppPaths  []string `json:"app_paths,omitempty"`
}

type HomebrewResult struct {
	Available bool          `json:"available"`
	Packages  []BrewPackage `json:"packages"`
	Warnings  []string      `json:"warnings"`
}

// BrewCommand is injectable so command parsing can be tested without Homebrew.
type BrewCommand func(context.Context, ...string) ([]byte, error)

func commandBrew(ctx context.Context, args ...string) ([]byte, error) {
	path, err := exec.LookPath("brew")
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	return exec.CommandContext(cctx, path, args...).Output()
}

// ScanHomebrew discovers installed packages and marks entries outdated according
// to Homebrew itself. It never invokes update or upgrade.
func ScanHomebrew(ctx context.Context) HomebrewResult { return ScanHomebrewWith(ctx, commandBrew) }

func ScanHomebrewWith(ctx context.Context, command BrewCommand) HomebrewResult {
	r := HomebrewResult{Packages: []BrewPackage{}, Warnings: []string{}}
	if _, err := exec.LookPath("brew"); err != nil && command == nil {
		r.Warnings = append(r.Warnings, "Homebrew not found in PATH")
		return r
	}
	if command == nil {
		command = commandBrew
	}
	all := map[string]*BrewPackage{}
	succeeded := false
	for _, kind := range []string{"formula", "cask"} {
		out, err := command(ctx, "list", "--"+kind, "--versions")
		if err != nil {
			r.Warnings = append(r.Warnings, fmt.Sprintf("brew list --%s --versions: %v", kind, err))
			continue
		}
		succeeded = true
		for _, p := range parseBrewVersions(out, kind) {
			cp := p
			all[kind+":"+p.Name] = &cp
		}
	}
	r.Available = succeeded
	if !succeeded {
		return r
	}
	out, err := command(ctx, "outdated", "--json=v2")
	if err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("brew outdated --json=v2: %v", err))
	} else {
		var decoded struct {
			Formulae []struct {
				Name              string   `json:"name"`
				InstalledVersions []string `json:"installed_versions"`
				CurrentVersion    string   `json:"current_version"`
			} `json:"formulae"`
			Casks []struct {
				Name              string   `json:"name"`
				InstalledVersions []string `json:"installed_versions"`
				CurrentVersion    string   `json:"current_version"`
			} `json:"casks"`
		}
		if err := json.Unmarshal(out, &decoded); err != nil {
			r.Warnings = append(r.Warnings, fmt.Sprintf("invalid brew outdated JSON: %v", err))
		} else {
			for _, p := range decoded.Formulae {
				markBrewOutdated(all, "formula", p.Name, p.CurrentVersion)
			}
			for _, p := range decoded.Casks {
				markBrewOutdated(all, "cask", p.Name, p.CurrentVersion)
			}
		}
	}
	// Installed cask artifact destinations are used for exact path reconciliation.
	// This is descriptive metadata, not confirmation that the path exists.
	if out, err := command(ctx, "info", "--json=v2", "--installed"); err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("brew info --json=v2 --installed: %v", err))
	} else if paths, err := parseInstalledCaskPaths(out); err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("invalid brew info JSON: %v", err))
	} else {
		for name, destinations := range paths {
			if pkg, ok := all["cask:"+name]; ok {
				pkg.AppPaths = destinations
			}
		}
	}
	for _, p := range all {
		r.Packages = append(r.Packages, *p)
	}
	sort.Slice(r.Packages, func(i, j int) bool {
		if r.Packages[i].Kind != r.Packages[j].Kind {
			return r.Packages[i].Kind < r.Packages[j].Kind
		}
		return r.Packages[i].Name < r.Packages[j].Name
	})
	return r
}

func parseBrewVersions(out []byte, kind string) []BrewPackage {
	var result []BrewPackage
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		result = append(result, BrewPackage{Name: fields[0], Kind: kind, Installed: strings.Join(fields[1:], ", ")})
	}
	return result
}
func markBrewOutdated(packages map[string]*BrewPackage, kind, name, current string) {
	if p, ok := packages[kind+":"+name]; ok {
		p.Outdated = true
		p.Current = current
	}
}

// parseInstalledCaskPaths uses app artifact targets from Homebrew's installed
// cask metadata. Only absolute paths are accepted; names are not guessed.
func parseInstalledCaskPaths(data []byte) (map[string][]string, error) {
	var doc struct {
		Casks []struct {
			Token     string `json:"token"`
			Artifacts []struct {
				App    json.RawMessage `json:"app"`
				Target string          `json:"target"`
			} `json:"artifacts"`
		} `json:"casks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	paths := make(map[string][]string)
	for _, c := range doc.Casks {
		for _, artifact := range c.Artifacts {
			if len(artifact.App) == 0 || !filepath.IsAbs(artifact.Target) {
				continue
			}
			paths[c.Token] = append(paths[c.Token], filepath.Clean(artifact.Target))
		}
	}
	return paths, nil
}
