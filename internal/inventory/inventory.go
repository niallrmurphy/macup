// Package inventory discovers macOS application bundles and reads their metadata.
package inventory

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Application struct {
	Name     string `json:"name"`
	BundleID string `json:"bundle_id,omitempty"`
	Version  string `json:"version,omitempty"`
	Build    string `json:"build,omitempty"`
	Path     string `json:"path"`
}

type Warning struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type Result struct {
	Applications []Application   `json:"applications"`
	Warnings     []Warning       `json:"warnings"`
	Roots        []string        `json:"roots"`
	Homebrew     *HomebrewResult `json:"homebrew,omitempty"`
	AppStore     *AppStoreResult `json:"appstore,omitempty"`
}

// DefaultRoots covers common system-wide and per-user app locations.
func DefaultRoots() []string {
	home, _ := os.UserHomeDir()
	roots := []string{"/Applications", "/System/Applications", "/System/Applications/Utilities"}
	if home != "" {
		roots = append(roots, filepath.Join(home, "Applications"))
	}
	return roots
}

// Scan visits the supplied roots. Nested .app bundles are not recursively traversed.
// A missing root is not an error; unreadable roots and malformed bundles are reported.
func Scan(ctx context.Context, roots []string) Result {
	result := Result{Applications: []Application{}, Warnings: []Warning{}, Roots: append([]string{}, roots...)}
	seen := map[string]bool{}
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			result.Warnings = append(result.Warnings, Warning{root, err.Error()})
			break
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			result.Warnings = append(result.Warnings, Warning{root, err.Error()})
			continue
		}
		info, err := os.Stat(abs)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			result.Warnings = append(result.Warnings, Warning{abs, err.Error()})
			continue
		}
		if !info.IsDir() {
			result.Warnings = append(result.Warnings, Warning{abs, "not a directory"})
			continue
		}
		walkErr := filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if walkErr != nil {
				result.Warnings = append(result.Warnings, Warning{path, walkErr.Error()})
				return filepath.SkipDir
			}
			// Do not follow symlinks; this prevents cycles and escaping selected roots.
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if !entry.IsDir() {
				return nil
			}
			if strings.EqualFold(filepath.Ext(entry.Name()), ".app") {
				canonical := filepath.Clean(path)
				if !seen[canonical] {
					seen[canonical] = true
					app, err := ReadBundle(canonical)
					if err != nil {
						result.Warnings = append(result.Warnings, Warning{canonical, err.Error()})
					} else {
						result.Applications = append(result.Applications, app)
					}
				}
				return filepath.SkipDir
			}
			// App directories sometimes contain many subdirectories; cap discovery depth.
			rel, err := filepath.Rel(abs, path)
			if err == nil && rel != "." && len(strings.Split(rel, string(filepath.Separator))) >= 5 {
				return filepath.SkipDir
			}
			return nil
		})
		if walkErr != nil {
			result.Warnings = append(result.Warnings, Warning{abs, walkErr.Error()})
		}
	}
	sort.Slice(result.Applications, func(i, j int) bool {
		a, b := result.Applications[i], result.Applications[j]
		if strings.EqualFold(a.Name, b.Name) {
			return a.Path < b.Path
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	sort.Slice(result.Warnings, func(i, j int) bool { return result.Warnings[i].Path < result.Warnings[j].Path })
	return result
}

func ReadBundle(path string) (Application, error) {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if filepath.Ext(strings.ToLower(path)) != ".app" {
		return Application{}, fmt.Errorf("not an .app bundle: %s", path)
	}
	data, err := loadPlist(filepath.Join(path, "Contents", "Info.plist"))
	if err != nil {
		return Application{}, fmt.Errorf("reading Info.plist: %w", err)
	}
	name := first(data, "CFBundleDisplayName", "CFBundleName")
	if name == "" {
		name = base
	}
	return Application{Name: name, BundleID: data["CFBundleIdentifier"], Version: data["CFBundleShortVersionString"], Build: data["CFBundleVersion"], Path: path}, nil
}

func first(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(m[k]); v != "" {
			return v
		}
	}
	return ""
}
