package inventory

import (
	"path/filepath"
	"strings"
)

// IsSystemManagedApp identifies application bundles supplied on the macOS
// system volume. It deliberately does not infer ownership from bundle IDs,
// names or code signatures: Apple also distributes separately updated apps.
func IsSystemManagedApp(path string) bool {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return false
	}
	for _, prefix := range []string{"/System/Applications", "/System/Library/CoreServices/Applications"} {
		if strings.HasPrefix(clean, prefix+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// FilterSystemApplications preserves discovery order while hiding OS-managed
// bundles. Homebrew and Mac App Store records are not affected.
func FilterSystemApplications(apps []Application) []Application {
	filtered := make([]Application, 0, len(apps))
	for _, app := range apps {
		if !IsSystemManagedApp(app.Path) {
			filtered = append(filtered, app)
		}
	}
	return filtered
}
