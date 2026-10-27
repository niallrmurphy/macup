package probe

import (
	"regexp"
	"strconv"
	"strings"
)

var versionDigits = regexp.MustCompile(`\d+(?:\.\d+)*`)

// versionNumbers extracts the first dotted numeric run from a version string.
func versionNumbers(s string) ([]int, bool) {
	m := versionDigits.FindString(s)
	if m == "" {
		return nil, false
	}
	var out []int
	for _, p := range strings.Split(m, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// compareVersions returns -1, 0 or 1 for a relative to b, and false when either
// side has no numeric content. Build suffixes after the numeric run are ignored.
func compareVersions(a, b string) (int, bool) {
	x, ok1 := versionNumbers(a)
	y, ok2 := versionNumbers(b)
	if !ok1 || !ok2 {
		return 0, false
	}
	for i := 0; i < len(x) || i < len(y); i++ {
		var p, q int
		if i < len(x) {
			p = x[i]
		}
		if i < len(y) {
			q = y[i]
		}
		if p < q {
			return -1, true
		}
		if p > q {
			return 1, true
		}
	}
	return 0, true
}

// comparison labels a remote version relative to the installed one.
func comparison(installed, remote string) string {
	c, ok := compareVersions(remote, installed)
	if !ok || !sameScheme(installed, remote) {
		return "unknown"
	}
	switch {
	case c > 0:
		return "newer"
	case c < 0:
		return "older"
	}
	return "same"
}

// sameScheme rejects comparisons between obviously different numbering
// schemes, such as a build counter against a dotted marketing version, or a
// Chromium version against a vendor's own major number.
func sameScheme(a, b string) bool {
	x, _ := versionNumbers(a)
	y, _ := versionNumbers(b)
	if (len(x) == 1) != (len(y) == 1) {
		return false
	}
	if len(x) > 0 && len(y) > 0 && x[0] > 0 && y[0] > 0 {
		hi, lo := x[0], y[0]
		if lo > hi {
			hi, lo = lo, hi
		}
		if hi > 10*lo {
			return false
		}
	}
	return true
}
