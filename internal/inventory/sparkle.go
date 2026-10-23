package inventory

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SparkleCheck is advisory metadata only: macup does not reproduce Sparkle's
// channel, OS, architecture, rollout, or signature-verification policy.
type SparkleCheck struct {
	Path             string              `json:"path"`
	FeedURL          string              `json:"feed_url,omitempty"`
	InstalledBuild   string              `json:"installed_build,omitempty"`
	AvailableBuild   string              `json:"available_build,omitempty"`
	AvailableVersion string              `json:"available_version,omitempty"`
	Status           string              `json:"status"`
	Reason           string              `json:"reason,omitempty"`
	Diagnostics      *SparkleDiagnostics `json:"diagnostics,omitempty"`
}

// SparkleDiagnostics captures why appcast items could or could not be used.
// Counts are per appcast item, not per enclosure.
type SparkleDiagnostics struct {
	TotalItems             int    `json:"total_items"`
	Comparable             int    `json:"comparable"`
	SkippedChannel         int    `json:"skipped_channel"`
	SkippedDelta           int    `json:"skipped_delta"`
	SkippedOS              int    `json:"skipped_os"`
	UnknownOS              int    `json:"unknown_os"`
	SkippedMinimumUpdate   int    `json:"skipped_minimum_update"`
	MissingVersion         int    `json:"missing_version"`
	UncomparableVersion    int    `json:"uncomparable_version"`
	ItemVersions           int    `json:"item_versions"`
	EnclosureVersions      int    `json:"enclosure_versions"`
	ItemShortVersions      int    `json:"item_short_versions"`
	EnclosureShortVersions int    `json:"enclosure_short_versions"`
	EnclosureURLs          int    `json:"enclosure_urls"`
	XMLSample              string `json:"xml_sample,omitempty"`
}

// SparkleDebugXMLDir enables writing an element/attribute-name-only XML sample
// to the specified local directory. No XML values or download URLs are written.
// This is intentionally diagnostic-only; normal calls never write files.
type SparkleDebugXMLDir string

type SparkleResult struct {
	Checks []SparkleCheck `json:"checks"`
}

type AppcastFetcher func(context.Context, string) ([]byte, error)

// SparkleProgress reports feed checks without coupling inventory to terminal output.
// Total is the number of discovered apps, not the number with Sparkle feeds.
// A start event precedes each eligible network fetch; a done event always follows it.
type SparkleProgress struct {
	Name     string
	Index    int
	Total    int
	Phase    string // "start", "done", or "skip"
	Status   string
	Reason   string
	Duration time.Duration
	Check    SparkleCheck
}

type SparkleProgressFunc func(SparkleProgress)

var sparkleClient = &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}

func fetchAppcast(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return nil, errors.New("appcast URL must be an absolute HTTPS URL without credentials")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, err
	}
	resp, err := sparkleClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > 2<<20 {
		return nil, errors.New("appcast exceeds 2 MiB")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 2<<20 {
		return nil, errors.New("appcast exceeds 2 MiB")
	}
	return b, nil
}

const sparkleNS = "http://www.andymatuschak.org/xml-namespaces/sparkle"

type appcast struct {
	Items []struct {
		Version   string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle version"`
		Short     string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle shortVersionString"`
		MinOS     string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle minimumSystemVersion"`
		MaxOS     string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle maximumSystemVersion"`
		Channel   string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle channel"`
		MinUpdate string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle minimumUpdateVersion"`
		Enclosure struct {
			URL       string `xml:"url,attr"`
			Version   string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle version,attr"`
			Short     string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle shortVersionString,attr"`
			OS        string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle os,attr"`
			DeltaFrom string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle deltaFrom,attr"`
		} `xml:"enclosure"`
		Deltas []struct{} `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle deltas>enclosure"`
	} `xml:"channel>item"`
}

var numericVersion = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)

func compareBuilds(a, b string) (int, bool) {
	if !numericVersion.MatchString(a) || !numericVersion.MatchString(b) {
		return 0, false
	}
	x, y := strings.Split(a, "."), strings.Split(b, ".")
	n := len(x)
	if len(y) > n {
		n = len(y)
	}
	for i := 0; i < n; i++ {
		var ai, bi uint64
		var err error
		if i < len(x) {
			ai, err = strconv.ParseUint(x[i], 10, 64)
			if err != nil {
				return 0, false
			}
		}
		if i < len(y) {
			bi, err = strconv.ParseUint(y[i], 10, 64)
			if err != nil {
				return 0, false
			}
		}
		if ai > bi {
			return 1, true
		}
		if ai < bi {
			return -1, true
		}
	}
	return 0, true
}

// hostVersion is deliberately unknown outside macOS: unknown eligibility is
// never treated as proof that an update is available.
func hostVersion(ctx context.Context) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	b, err := exec.CommandContext(ctx, "/usr/bin/sw_vers", "-productVersion").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// eligibleRelease checks platform constraints without rejecting the entire feed.
// An unknown host version makes an OS-gated release indeterminate.
func eligibleRelease(itMin, itMax, osName, host string) (bool, bool) {
	if osName != "" && osName != "macos" {
		return false, true
	}
	if itMin == "" && itMax == "" {
		return true, true
	}
	if host == "" {
		return false, false
	}
	if itMin != "" {
		cmp, ok := compareBuilds(host, itMin)
		if !ok {
			return false, false
		}
		if cmp < 0 {
			return false, true
		}
	}
	if itMax != "" {
		cmp, ok := compareBuilds(host, itMax)
		if !ok {
			return false, false
		}
		if cmp > 0 {
			return false, true
		}
	}
	return true, true
}

// CheckSparkle checks only bundles with SUFeedURL and numerical build versions.
// Feed checks are opt-in because URLs supplied by app bundles are untrusted.
func CheckSparkle(ctx context.Context, apps []Application, fetch AppcastFetcher) SparkleResult {
	return CheckSparkleWithProgress(ctx, apps, fetch, nil)
}

// CheckSparkleWithProgress adds optional per-feed progress notifications.
// It does not change the structure of the returned inventory data.
func CheckSparkleWithProgress(ctx context.Context, apps []Application, fetch AppcastFetcher, progress SparkleProgressFunc) SparkleResult {
	return checkSparkle(ctx, apps, fetch, progress, false, "")
}

// CheckSparkleWithDiagnostics captures per-release selection statistics.
func CheckSparkleWithDiagnostics(ctx context.Context, apps []Application, fetch AppcastFetcher, progress SparkleProgressFunc) SparkleResult {
	return checkSparkle(ctx, apps, fetch, progress, true, "")
}

func CheckSparkleWithXMLSamples(ctx context.Context, apps []Application, fetch AppcastFetcher, progress SparkleProgressFunc, dir string) SparkleResult {
	return checkSparkle(ctx, apps, fetch, progress, true, dir)
}

func checkSparkle(ctx context.Context, apps []Application, fetch AppcastFetcher, progress SparkleProgressFunc, diagnostics bool, sampleDir string) SparkleResult {
	if fetch == nil {
		fetch = fetchAppcast
	}
	r := SparkleResult{Checks: []SparkleCheck{}}
	osVersion := hostVersion(ctx)
	for index, a := range apps {
		if ctx.Err() != nil {
			break
		}
		m, err := loadPlist(filepath.Join(a.Path, "Contents", "Info.plist"))
		if err != nil {
			continue
		}
		feed := strings.TrimSpace(m["SUFeedURL"])
		if feed == "" {
			continue
		}
		c := SparkleCheck{Path: a.Path, FeedURL: feed, InstalledBuild: a.Build, Status: "UNCHECKED"}
		if diagnostics {
			c.Diagnostics = &SparkleDiagnostics{}
		}
		emit := func(phase string, duration time.Duration) {
			if progress != nil {
				progress(SparkleProgress{Name: a.Name, Index: index + 1, Total: len(apps), Phase: phase, Status: c.Status, Reason: c.Reason, Duration: duration, Check: c})
			}
		}
		u, e := url.Parse(feed)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			c.Reason = "non-HTTPS or invalid feed URL"
			emit("skip", 0)
			r.Checks = append(r.Checks, c)
			continue
		}
		if !numericVersion.MatchString(a.Build) {
			c.Reason = "non-numeric or missing bundle build"
			emit("skip", 0)
			r.Checks = append(r.Checks, c)
			continue
		}
		started := time.Now()
		emit("start", 0)
		data, e := fetch(ctx, feed)
		if e != nil {
			c.Reason = e.Error()
			r.Checks = append(r.Checks, c)
			emit("done", time.Since(started))
			continue
		}
		var cast appcast
		if e = xml.Unmarshal(data, &cast); e != nil {
			c.Reason = "invalid appcast XML"
			r.Checks = append(r.Checks, c)
			emit("done", time.Since(started))
			continue
		}
		if diagnostics {
			c.Diagnostics.XMLSample = ""
			if sampleDir != "" {
				if name, err := writeAppcastShape(sampleDir, a.Name, index, data); err == nil {
					c.Diagnostics.XMLSample = name
				} else {
					c.Diagnostics.XMLSample = "sample unavailable: " + err.Error()
				}
			}
		}
		// Assess each full release individually. Delta enclosures are
		// alternatives, not reasons to discard the corresponding full release.
		best := a.Build
		bestShort := ""
		uncertain := false
		eligible := false
		for _, it := range cast.Items {
			if diagnostics {
				c.Diagnostics.TotalItems++
				if it.Version != "" {
					c.Diagnostics.ItemVersions++
				}
				if it.Short != "" {
					c.Diagnostics.ItemShortVersions++
				}
				if it.Enclosure.Version != "" {
					c.Diagnostics.EnclosureVersions++
				}
				if it.Enclosure.Short != "" {
					c.Diagnostics.EnclosureShortVersions++
				}
				if it.Enclosure.URL != "" {
					c.Diagnostics.EnclosureURLs++
				}
			}
			if it.Channel != "" { // Can't assume the user opted into this channel.
				if diagnostics {
					c.Diagnostics.SkippedChannel++
				}
				continue
			}
			if it.Enclosure.DeltaFrom != "" { // A delta-only item isn't a full release.
				if diagnostics {
					c.Diagnostics.SkippedDelta++
				}
				continue
			}
			allowed, known := eligibleRelease(it.MinOS, it.MaxOS, it.Enclosure.OS, osVersion)
			if !known {
				if diagnostics {
					c.Diagnostics.UnknownOS++
				}
				uncertain = true
				continue
			}
			if !allowed {
				if diagnostics {
					c.Diagnostics.SkippedOS++
				}
				continue
			}
			if it.MinUpdate != "" { // This constraint needs Sparkle-specific policy.
				if diagnostics {
					c.Diagnostics.SkippedMinimumUpdate++
				}
				uncertain = true
				continue
			}
			v := it.Version
			if v == "" {
				v = it.Enclosure.Version
			}
			if v == "" {
				if diagnostics {
					c.Diagnostics.MissingVersion++
				}
				continue
			}
			cmp, ok := compareBuilds(v, best)
			if !ok {
				if diagnostics {
					c.Diagnostics.UncomparableVersion++
				}
				uncertain = true
				continue
			}
			eligible = true
			if diagnostics {
				c.Diagnostics.Comparable++
			}
			if cmp > 0 {
				best = v
				bestShort = it.Short
				if bestShort == "" {
					bestShort = it.Enclosure.Short
				}
			}
		}
		if best != a.Build {
			c.Status = "OUTDATED"
			c.AvailableBuild = best
			c.AvailableVersion = bestShort
		} else if uncertain {
			c.Reason = "some releases have unknown compatibility or version ordering"
		} else if eligible {
			c.Status = "NOT FLAGGED"
		} else {
			c.Reason = "no compatible comparable full releases"
		}
		r.Checks = append(r.Checks, c)
		emit("done", time.Since(started))
	}
	return r
}

// ApplySparkle enriches only unmanged app entries. Existing package-manager
// signals are deliberately not overridden by advisory appcast checks.
func ApplySparkle(r *Result) {
	if r.Sparkle == nil {
		return
	}
	index := map[string]SparkleCheck{}
	for _, c := range r.Sparkle.Checks {
		index[identityPath(c.Path)] = c
	}
	for i := range r.Entries {
		e := &r.Entries[i]
		if len(e.Sources) != 1 || e.Sources[0] != "apps" {
			continue
		}
		c, ok := index[identityPath(e.Path)]
		if !ok {
			continue
		}
		if c.Status == "OUTDATED" {
			e.Status = "OUTDATED"
			e.UpdateSource = "sparkle"
			e.Available = c.AvailableVersion
			if e.Available == "" {
				e.Available = c.AvailableBuild
			}
			e.Sources = append(e.Sources, "sparkle")
		} else if c.Status == "NOT FLAGGED" {
			e.UpdateSource = "sparkle"
			e.Status = "NOT FLAGGED"
			e.Sources = append(e.Sources, "sparkle")
		}
		if c.Reason != "" {
			e.Detail = c.Reason
		}
	}
}

// writeAppcastShape saves ONLY structural XML metadata: element names and
// attribute names from the first channel item. No values, URLs, or contents.
// This is safe to inspect but still should be reviewed before public sharing.
func writeAppcastShape(dir, appName string, index int, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	inItem := false
	depth := 0
	lines := []string{"# First appcast item: structural XML metadata only"}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch x := tok.(type) {
		case xml.StartElement:
			if !inItem {
				if x.Name.Local != "item" {
					continue
				}
				inItem = true
			}
			if depth < 6 && len(lines) < 100 {
				attrs := make([]string, 0, len(x.Attr))
				for _, a := range x.Attr {
					attrs = append(attrs, a.Name.Local)
				}
				sort.Strings(attrs)
				lines = append(lines, strings.Repeat("  ", depth)+x.Name.Local+" attributes=["+strings.Join(attrs, ", ")+"]")
			}
			depth++
		case xml.EndElement:
			if inItem {
				depth--
				if depth == 0 {
					goto done
				}
			}
		}
	}
done:
	if !inItem {
		lines = append(lines, "(no item elements)")
	}
	safe := regexp.MustCompile(`[^a-zA-Z0-9_-]+`).ReplaceAllString(appName, "-")
	if safe == "" {
		safe = "application"
	}
	if len(safe) > 50 {
		safe = safe[:50]
	}
	filename := fmt.Sprintf("%03d-%s.shape.txt", index+1, safe)
	target := path.Join(dir, filename)
	if err := os.WriteFile(target, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		return "", err
	}
	return target, nil
}
