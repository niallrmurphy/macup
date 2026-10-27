package probe

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var homeDir = func() string {
	h, _ := os.UserHomeDir()
	return h
}

// inspectLocal reads macOS records that live outside the bundle: the app's
// own preferences, vendor launch agents, and the code signature.
func inspectLocal(ctx context.Context, r *Report, fields map[string]string) {
	bid := r.Application.BundleID
	if bid != "" {
		prefs := filepath.Join(homeDir(), "Library", "Preferences", bid+".plist")
		if vals, err := readPlistScalars(prefs); err == nil {
			for _, k := range []string{"SUFeedURL", "SUFeedURLForFinal"} {
				if u := vals[k]; u != "" {
					add(r, u, "defaults:"+k, 90)
					r.addFamily("sparkle")
				}
			}
		}
		for _, label := range vendorLaunchAgents(bid) {
			r.addFamily("launch-agent:" + label)
		}
	}
}

func inspectSigner(ctx context.Context, r *Report) {
	signer, team := codeSigner(ctx, r.Application.Path)
	r.Signer, r.TeamID = signer, team
}

var launchDirs = func() []string {
	return []string{"/Library/LaunchAgents", "/Library/LaunchDaemons", filepath.Join(homeDir(), "Library", "LaunchAgents")}
}
var updaterLabel = regexp.MustCompile(`(?i)(updat|keystone|installer|creativecloud|autoupdate)`)

// vendorLaunchAgents lists updater-looking launchd jobs sharing the app's
// vendor prefix (the first two reverse-DNS labels). Jobs that also name the
// product (com.microsoft.OneDrive -> OneDriveStandaloneUpdater) sort first,
// ahead of vendor-wide agents. Apple jobs are ignored.
func vendorLaunchAgents(bundleID string) []string {
	parts := strings.Split(strings.ToLower(bundleID), ".")
	if len(parts) < 2 || parts[0]+"."+parts[1] == "com.apple" {
		return nil
	}
	prefix := parts[0] + "." + parts[1] + "."
	var product []string
	for _, p := range parts[2:] {
		if p = squash(p); len(p) >= 4 && !genericTokens[p] {
			product = append(product, p)
		}
	}
	var specific, general, other []string
	for _, dir := range launchDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := strings.ToLower(strings.TrimSuffix(e.Name(), ".plist"))
			if !strings.HasPrefix(name, prefix) || !updaterLabel.MatchString(name) {
				continue
			}
			label := strings.TrimSuffix(e.Name(), ".plist")
			rest := squash(strings.TrimPrefix(name, prefix))
			named := false
			for _, p := range product {
				if strings.Contains(rest, p) || strings.Contains(rest, p[:4]) {
					named = true
				}
			}
			switch {
			case named:
				specific = append(specific, label)
			case vendorWideAgent(rest):
				general = append(general, label)
			default:
				// Names another product (OneDriveStandaloneUpdater seen from Word).
				other = append(other, label)
			}
		}
	}
	return append(append(specific, general...), other...)
}

// Words that describe an updater job rather than a product. A label made only
// of these after the vendor prefix is a vendor-wide agent.
var agentWords = []string{"autoupdate", "updater", "update", "keystone", "agent", "daemon", "helper", "installer", "xpcservice", "wake", "system", "login", "check", "service", "creativecloud", "googleupdater"}

func vendorWideAgent(rest string) bool {
	for _, w := range agentWords {
		rest = strings.ReplaceAll(rest, w, "")
	}
	return strings.Trim(rest, "0123456789") == ""
}

var authorityLine = regexp.MustCompile(`(?m)^Authority=(.+)$`)
var teamLine = regexp.MustCompile(`(?m)^TeamIdentifier=(.+)$`)
var developerID = regexp.MustCompile(`^(?:Developer ID Application|Apple Development|Apple Distribution|3rd Party Mac Developer Application): (.+?) \(([A-Z0-9]+)\)$`)

// codeSigner returns the signing organisation and team, or the Apple
// authority name when the app was signed by Apple or the App Store.
func codeSigner(ctx context.Context, path string) (string, string) {
	// codesign writes its report to stderr.
	text := string(combinedOutput(ctx, "/usr/bin/codesign", "-dv", "--verbose=2", "--", path))
	auth := authorityLine.FindStringSubmatch(text)
	team := ""
	if m := teamLine.FindStringSubmatch(text); m != nil && strings.TrimSpace(m[1]) != "not set" {
		team = strings.TrimSpace(m[1])
	}
	if auth == nil {
		return "", team
	}
	a := strings.TrimSpace(auth[1])
	if m := developerID.FindStringSubmatch(a); m != nil {
		return m[1], m[2]
	}
	return a, team
}

var combinedOutput = func(ctx context.Context, name string, args ...string) []byte {
	out, _ := execCombined(ctx, name, args...)
	return out
}

// --- Download origins (opt-in: these records are a browsing history) ---

const cocoaEpoch = 978307200

var quarantineDB = func() string {
	return filepath.Join(homeDir(), "Library", "Preferences", "com.apple.LaunchServices.QuarantineEventsV2")
}

type quarantineRow struct {
	ID     string  `json:"id"`
	TS     float64 `json:"ts"`
	Agent  string  `json:"agent"`
	URL    string  `json:"url"`
	Origin string  `json:"origin"`
}

func inspectOrigins(ctx context.Context, r *Report) {
	path := r.Application.Path
	// Spotlight "Where from" on the bundle itself, when the extractor preserved it.
	if out, err := runCommand(ctx, "/usr/bin/mdls", "-raw", "-name", "kMDItemWhereFroms", "--", path); err == nil {
		for _, u := range quotedStrings(string(out)) {
			r.Origins = append(r.Origins, Origin{URL: u, Source: "mdls:kMDItemWhereFroms"})
		}
	}
	// The quarantine xattr carries the event UUID, which joins exactly to the LaunchServices database.
	eventID := ""
	if out, err := runCommand(ctx, "/usr/bin/xattr", "-p", "com.apple.quarantine", "--", path); err == nil {
		if f := strings.Split(strings.TrimSpace(string(out)), ";"); len(f) >= 4 {
			eventID = f[3]
		}
	}
	db := quarantineDB()
	if _, err := os.Stat(db); err != nil {
		return
	}
	out, err := runCommand(ctx, "/usr/bin/sqlite3", "-readonly", "-json", db, "select LSQuarantineEventIdentifier id, LSQuarantineTimeStamp ts, LSQuarantineAgentName agent, LSQuarantineDataURLString url, LSQuarantineOriginURLString origin from LSQuarantineEvent where LSQuarantineDataURLString is not null order by LSQuarantineTimeStamp desc")
	if err != nil {
		r.warn("quarantine database: %s", err.Error())
		return
	}
	var rows []quarantineRow
	if len(strings.TrimSpace(string(out))) > 0 {
		if err := json.Unmarshal(out, &rows); err != nil {
			r.warn("quarantine database: %s", err.Error())
			return
		}
	}
	for _, o := range matchQuarantine(rows, eventID, r.Application.Name, filepath.Base(path)) {
		r.Origins = append(r.Origins, o)
	}
	for _, o := range r.Origins {
		add(r, o.URL, "origin:download", 70)
		if o.Referrer != "" {
			add(r, o.Referrer, "origin:referrer", 60)
		}
	}
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func squash(s string) string { return nonAlnum.ReplaceAllString(strings.ToLower(s), "") }

// matchQuarantine joins by event UUID when available, otherwise by the
// download filename containing the application name. At most three matches.
func matchQuarantine(rows []quarantineRow, eventID, appName, bundleName string) []Origin {
	var out []Origin
	names := []string{squash(appName), squash(strings.TrimSuffix(bundleName, ".app"))}
	for _, row := range rows {
		matched := eventID != "" && strings.EqualFold(row.ID, eventID)
		source := "quarantine-db:event"
		if !matched {
			u, err := url.Parse(row.URL)
			if err != nil {
				continue
			}
			file := squash(filepath.Base(u.Path))
			for _, n := range names {
				if len(n) >= 4 && strings.Contains(file, n) {
					matched = true
					source = "quarantine-db:name"
				}
			}
		}
		if !matched {
			continue
		}
		o := Origin{URL: row.URL, Referrer: row.Origin, Agent: row.Agent, Source: source}
		if row.TS > 0 {
			o.Time = time.Unix(int64(row.TS)+cocoaEpoch, 0).UTC().Format(time.RFC3339)
		}
		out = append(out, o)
		if len(out) >= 3 {
			break
		}
	}
	return out
}

var quoted = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)

func quotedStrings(s string) []string {
	var out []string
	for _, m := range quoted.FindAllStringSubmatch(s, -1) {
		if v, err := strconv.Unquote(`"` + m[1] + `"`); err == nil {
			out = append(out, v)
		} else {
			out = append(out, m[1])
		}
	}
	return out
}
