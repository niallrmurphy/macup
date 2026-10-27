package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const defaultMSUpdate = "/Library/Application Support/Microsoft/MAU2.0/Microsoft AutoUpdate.app/Contents/MacOS/msupdate"

type MicrosoftUpdate struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	AvailableVersion string `json:"available_version"`
}
type MicrosoftResult struct {
	Available      bool              `json:"available"`
	CheckStatus    string            `json:"check_status"`              // checked, failed, unavailable
	ResponseFormat string            `json:"response_format,omitempty"` // plist or text
	Updates        []MicrosoftUpdate `json:"updates"`
	Warnings       []string          `json:"warnings"`
}

type MSUpdateCommand func(context.Context) ([]byte, error)

// Running --list is read-only. Never invoke --install.
func commandMSUpdate(ctx context.Context) ([]byte, error) {
	info, err := os.Stat(defaultMSUpdate)
	if err != nil {
		return nil, err
	}
	if info.IsDir() || info.Mode()&0111 == 0 {
		return nil, fmt.Errorf("msupdate is not executable")
	}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, filepath.Clean(defaultMSUpdate), "--list", "--format", "plist")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("msupdate --list failed: %w (%s)", err, diagnosticSnippet(out))
	}
	return out, nil
}

func ScanMicrosoft(ctx context.Context) MicrosoftResult {
	return ScanMicrosoftWith(ctx, commandMSUpdate, convertMicrosoftPlist)
}

// PlistConverter converts msupdate's property list to JSON. Keeping it injected
// also permits testing on machines without macOS or Microsoft AutoUpdate.
type PlistConverter func(context.Context, []byte) ([]byte, error)

func convertMicrosoftPlist(ctx context.Context, data []byte) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", "-")
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("plutil: %w (%s)", err, diagnosticSnippet(out))
	}
	return out, nil
}

func ScanMicrosoftWith(ctx context.Context, command MSUpdateCommand, convert PlistConverter) MicrosoftResult {
	r := MicrosoftResult{CheckStatus: "unavailable", Updates: []MicrosoftUpdate{}, Warnings: []string{}}
	if command == nil {
		command = commandMSUpdate
	}
	if convert == nil {
		convert = convertMicrosoftPlist
	}
	raw, err := command(ctx)
	if err != nil {
		if os.IsNotExist(err) {
			return r
		}
		r.CheckStatus = "failed"
		r.Warnings = append(r.Warnings, "msupdate --list: "+err.Error())
		return r
	}
	// Some MAU versions can return an empty or human-readable response despite
	// accepting --format plist. Do not report a successful check in that case.
	payload, err := convert(ctx, plistPayload(raw))
	if err != nil {
		if microsoftNoUpdatesText(raw) {
			r.Available = true
			r.CheckStatus = "checked"
			r.ResponseFormat = "text"
			return r
		}
		r.CheckStatus = "failed"
		r.Warnings = append(r.Warnings, "msupdate output conversion: "+err.Error()+"; msupdate output: "+diagnosticSnippet(raw))
		return r
	}
	updates, err := parseMicrosoftUpdates(payload)
	if err != nil {
		r.CheckStatus = "failed"
		r.Warnings = append(r.Warnings, "msupdate output parsing: "+err.Error())
		return r
	}
	r.Available = true
	r.CheckStatus = "checked"
	r.ResponseFormat = "plist"
	r.Updates = updates
	return r
}

func parseMicrosoftUpdates(data []byte) ([]MicrosoftUpdate, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	raw, ok := root["Updates"]
	if !ok {
		if pending, ok := parseUpdateAssistant(root); ok {
			return pending, nil
		}
		return nil, fmt.Errorf("missing Updates array (unknown MAU output schema)")
	}
	var items []map[string]interface{}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("Updates: %w", err)
	}
	result := make([]MicrosoftUpdate, 0, len(items))
	for _, item := range items {
		str := func(k string) string { v, _ := item[k].(string); return strings.TrimSpace(v) }
		id, name, version := str("Application ID"), str("Application Name"), str("Available Version")
		if id == "" || version == "" {
			return nil, fmt.Errorf("update missing Application ID or Available Version")
		}
		if name == "" {
			name = id
		}
		result = append(result, MicrosoftUpdate{ID: id, Name: name, AvailableVersion: version})
	}
	return result, nil
}

// Explicit application names map to MAU's documented IDs; never fuzzy-match.
var mauNames = map[string]string{
	"MSWD2019": "Microsoft Word", "XCEL2019": "Microsoft Excel",
	"PPT32019": "Microsoft PowerPoint", "OPIM2019": "Microsoft Outlook",
	"ONMC2019": "Microsoft OneNote", "ONDR18": "OneDrive",
	"MSau04": "Microsoft AutoUpdate", "MSCP10": "Microsoft Copilot",
	"WDAVCONSUMER": "Microsoft Defender", "WDAV00": "Microsoft Defender",
	"MSQA01": "Quick Assist", "MSRH01": "Remote Help",
	"IMCP01": "Company Portal", "MSRD10": "Windows App",
}

// mauCoreOffice identifies applications for which MAU is the normal update agent.
// Matching both the exact name and, when available, the bundle identifier
// avoids marking unrelated apps (notably Teams and Edge) as current.
var mauCoreOffice = map[string]string{
	"Microsoft Word":       "com.microsoft.Word",
	"Microsoft Excel":      "com.microsoft.Excel",
	"Microsoft PowerPoint": "com.microsoft.Powerpoint",
	"Microsoft Outlook":    "com.microsoft.Outlook",
	"Microsoft OneNote":    "com.microsoft.onenote.mac",
}

func mauCoveredEntry(e Entry) bool {
	if e.Path == "" || len(e.Sources) == 0 {
		return false
	}
	expected, ok := mauCoreOffice[e.Name]
	if !ok {
		return false
	}
	return e.BundleID == "" || strings.EqualFold(e.BundleID, expected)
}

func ApplyMicrosoft(r *Result) {
	if r.Microsoft == nil || !r.Microsoft.Available {
		return
	}
	// A successful MAU query with no applicable update is a meaningful result
	// for its core Office applications, not an absence of evidence. We do not
	// extend that inference to Edge, Teams, or other Microsoft products.
	for i := range r.Entries {
		e := &r.Entries[i]
		if mauCoveredEntry(*e) && e.Status == "NO SOURCE" {
			e.Status = "CURRENT"
			e.UpdateSource = "microsoft-autoupdate"
		}
	}
	// Only match an app that was actually discovered. Avoid attaching updates
	// to a similarly named Homebrew formula or an ambiguous duplicate.
	counts := make(map[string]int)
	for _, e := range r.Entries {
		if e.Path != "" {
			counts[strings.ToLower(e.Name)]++
		}
	}
	for _, u := range r.Microsoft.Updates {
		name := mauNames[u.ID]
		if name == "" {
			name = u.Name
		}
		target := -1
		for i, e := range r.Entries {
			if e.Path != "" && strings.EqualFold(e.Name, name) && counts[strings.ToLower(e.Name)] == 1 && len(e.Sources) > 0 {
				target = i
				break
			}
		}
		if target >= 0 {
			e := &r.Entries[target]
			if e.UpdateSource == "" || e.Status == "NO SOURCE" || e.Status == "NOT FLAGGED" || e.Status == "CURRENT" {
				e.Available = u.AvailableVersion
				e.Status = "OUTDATED"
				e.UpdateSource = "microsoft-autoupdate"
			}
		} else {
			r.Entries = append(r.Entries, Entry{Name: u.Name, Available: u.AvailableVersion, Status: "OUTDATED", UpdateSource: "microsoft-autoupdate", Sources: []string{"microsoft-autoupdate"}, Detail: "id:" + u.ID})
		}
	}
}

func diagnosticSnippet(data []byte) string {
	s := strings.TrimSpace(string(data))
	s = strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, s)
	if len(s) > 360 {
		s = s[:360] + "..."
	}
	if s == "" {
		return "empty output"
	}
	return fmt.Sprintf("%q", s)
}

// microsoftNoUpdatesText recognises MAU's plain-text zero-update result.
// Only a terminal success state qualifies: this must not turn arbitrary
// unparseable output, a transient check, or an updater error into success.
var mauANSI = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func microsoftNoUpdatesText(raw []byte) bool {
	cleaned := mauANSI.ReplaceAllString(string(raw), "")
	cleaned = strings.ReplaceAll(cleaned, "\r", "\n")
	sawNoUpdates := false
	for _, line := range strings.Split(cleaned, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "Update Assistant: Idle") ||
			strings.EqualFold(line, "Update Assistant: Checking") ||
			strings.EqualFold(line, "Checking for updates...") {
			continue
		}
		if strings.EqualFold(line, "No updates available") {
			sawNoUpdates = true
			continue
		}
		return false
	}
	return sawNoUpdates
}

// plistPayload isolates the property list from the progress lines and
// terminal escapes msupdate prints around it when stdout is not a terminal.
func plistPayload(raw []byte) []byte {
	if i := bytes.Index(raw, []byte("<?xml")); i > 0 {
		raw = raw[i:]
	}
	if i := bytes.LastIndex(raw, []byte("</plist>")); i >= 0 {
		raw = raw[:i+len("</plist>")]
	}
	return raw
}

var assistantTitle = regexp.MustCompile(`Update (\d+(?:\.\d+)+)`)

// parseUpdateAssistant reads the per-application state dictionary MAU emits
// while an update is pending for an open application, for example
// {"OPIM2019": {"AppState": 13, "Title": "Microsoft Outlook Update 16.114.2 (26102345)"}}.
func parseUpdateAssistant(root map[string]json.RawMessage) ([]MicrosoftUpdate, bool) {
	var result []MicrosoftUpdate
	for id, raw := range root {
		var state struct {
			Title string `json:"Title"`
		}
		if json.Unmarshal(raw, &state) != nil || state.Title == "" {
			return nil, false
		}
		m := assistantTitle.FindStringSubmatch(state.Title)
		if m == nil {
			return nil, false
		}
		name := mauNames[id]
		if name == "" {
			name = id
		}
		result = append(result, MicrosoftUpdate{ID: id, Name: name, AvailableVersion: m[1]})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, len(result) > 0
}
