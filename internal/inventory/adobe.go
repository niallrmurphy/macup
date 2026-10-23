package inventory

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const adobeInventoryDB = "/Library/Application Support/Adobe/caps/hdpim.db"
const adobeCatalogueURL = "https://cdn-ffc.oobesaas.adobe.com/core/v5/products/all?channel=ccm&platform=osx10-64%2Cosx10%2Cmacarm64%2Cmacuniversal"

type AdobeInstalled struct {
	SAPCode      string `json:"sap_code"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
}
type AdobeProduct struct {
	ID        string   `json:"id"`
	Version   string   `json:"version"`
	Name      string   `json:"name"`
	Platforms []string `json:"platforms"`
}
type AdobeResult struct {
	CheckStatus string           `json:"check_status"`
	Installed   []AdobeInstalled `json:"installed"`
	Products    []AdobeProduct   `json:"products,omitempty"`
	Warnings    []string         `json:"warnings"`
}

// An inventory database is not a source of available versions. Run the Adobe
// catalogue check separately and only interpret versions for known products.
func ScanAdobe(ctx context.Context) AdobeResult {
	r := AdobeResult{CheckStatus: "unavailable", Installed: []AdobeInstalled{}, Warnings: []string{}}
	installed, err := readAdobeInstalled(ctx, adobeInventoryDB)
	if err != nil {
		if !os.IsNotExist(err) {
			r.Warnings = append(r.Warnings, "installed-product inventory: "+err.Error())
		}
		return r
	}
	r.Installed = installed
	if len(installed) == 0 {
		return r
	}
	products, err := fetchAdobeCatalogue(ctx, http.DefaultClient)
	if err != nil {
		r.CheckStatus = "failed"
		r.Warnings = append(r.Warnings, "product catalogue: "+err.Error())
		return r
	}
	r.Products = products
	r.CheckStatus = "checked"
	return r
}

func readAdobeInstalled(ctx context.Context, db string) ([]AdobeInstalled, error) {
	if _, err := os.Stat(db); err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Open SQLite in immutable read-only mode through sqlite3's -readonly flag.
	cmd := exec.CommandContext(cctx, "sqlite3", "-init", "/dev/null", "-readonly", "-csv", "-noheader", db, `SELECT SAPCode, ProductVersion, ProcessorFamily FROM product_installation_info WHERE Status = 1 ORDER BY SAPCode;`)
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("sqlite3 read-only query: %w", err)
	}
	var result []AdobeInstalled
	reader := csv.NewReader(bytes.NewReader(raw))
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("inventory CSV: %w", err)
	}
	for _, parts := range rows {
		if len(parts) != 3 {
			return nil, fmt.Errorf("unexpected Adobe inventory row")
		}
		result = append(result, AdobeInstalled{SAPCode: strings.TrimSpace(parts[0]), Version: strings.Trim(strings.TrimSpace(parts[1]), "'\""), Architecture: strings.TrimSpace(parts[2])})
	}
	return result, nil
}

func fetchAdobeCatalogue(ctx context.Context, client *http.Client) ([]AdobeProduct, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, adobeCatalogueURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-adobe-app-id", "accc-hdcore-desktop")
	limited := *client
	limited.Timeout = 30 * time.Second
	limited.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := limited.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 20<<20 {
		return nil, fmt.Errorf("catalogue exceeds 20 MiB")
	}
	return parseAdobeCatalogue(raw)
}

func parseAdobeCatalogue(raw []byte) ([]AdobeProduct, error) {
	var root struct {
		Channel []struct {
			Products struct {
				Product []struct {
					ID          string `json:"id"`
					Version     string `json:"version"`
					DisplayName string `json:"displayName"`
					Platforms   struct {
						Platform []struct {
							ID string `json:"id"`
						} `json:"platform"`
					} `json:"platforms"`
				} `json:"product"`
			} `json:"products"`
		} `json:"channel"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	if len(root.Channel) == 0 {
		return nil, fmt.Errorf("missing channel array")
	}
	var out []AdobeProduct
	for _, c := range root.Channel {
		for _, p := range c.Products.Product {
			if p.ID == "" || p.Version == "" {
				continue
			}
			x := AdobeProduct{ID: p.ID, Version: p.Version, Name: p.DisplayName}
			for _, platform := range p.Platforms.Platform {
				x.Platforms = append(x.Platforms, platform.ID)
			}
			out = append(out, x)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no product versions in catalogue")
	}
	return out, nil
}

var adobeApps = map[string]string{
	"Adobe Lightroom Classic":    "LTRM",
	"Adobe Photoshop 2023":       "PHSP",
	"Adobe Content Synchronizer": "COSY",
	"CCXProcess":                 "CCXP",
}
var adobeVersionPattern = regexp.MustCompile(`^\d+(?:\.\d+)*$`)

func adobeNumbers(s string) ([]int, bool) {
	if !adobeVersionPattern.MatchString(s) {
		return nil, false
	}
	parts := strings.Split(s, ".")
	v := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		v[i] = n
	}
	return v, true
}
func adobeCompare(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := 0, 0
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x > y {
			return 1
		}
		if x < y {
			return -1
		}
	}
	return 0
}

// Adobe package versions may omit a bundle-only build suffix. Accept a
// suffix only for the two locally confirmed Adobe components that use this
// convention; never relax matching for Photoshop or Lightroom Classic.
func adobeInstalledMatchesBundle(code, bundleVersion, packageVersion string) bool {
	packageNums, ok := adobeNumbers(packageVersion)
	if !ok {
		return false
	}
	bundleNums, ok := adobeNumbers(bundleVersion)
	if ok && adobeCompare(bundleNums, packageNums) == 0 {
		return true
	}
	switch code {
	case "COSY":
		// Example: Core Sync package 7.8.10, app bundle 7.8.10.1.
		return strings.HasPrefix(bundleVersion, packageVersion+".") &&
			adobeVersionPattern.MatchString(bundleVersion)
	case "CCXP":
		// Example: CCXProcess package 7.5.0, app bundle 7.5.0-8.
		suffix, found := strings.CutPrefix(bundleVersion, packageVersion+"-")
		if !found || suffix == "" {
			return false
		}
		for _, c := range suffix {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}
	return false
}

func adobeMacPlatform(p AdobeProduct) bool {
	for _, id := range p.Platforms {
		switch id {
		case "macuniversal", "macarm64", "osx10-64", "osx10":
			return true
		}
	}
	return false
}

// ApplyAdobe never overwrites an established update from another provider.
// 'UPGRADE AVAILABLE' means a newer major release is catalogued, NOT that
// Creative Cloud has confirmed an upgrade path for this installation.
func ApplyAdobe(r *Result) {
	if r.Adobe == nil || r.Adobe.CheckStatus != "checked" {
		return
	}
	for i := range r.Entries {
		e := &r.Entries[i]
		code, ok := adobeApps[e.Name]
		if !ok || e.Path == "" || e.Status != "UNCHECKED" {
			continue
		}
		// Bundle IDs differ across Adobe generations. Require an exact app name,
		// one matching installed SAP record, and a strictly verified bundle version.
		matches := 0
		var installed AdobeInstalled
		for _, p := range r.Adobe.Installed {
			if p.SAPCode == code {
				matches++
				installed = p
			}
		}
		if matches != 1 {
			continue
		}
		current, valid := adobeNumbers(installed.Version)
		if !valid || len(current) == 0 {
			continue
		}
		if !adobeInstalledMatchesBundle(code, e.Installed, installed.Version) {
			continue
		}
		var bestSame, bestMajor []int
		var sameVersion, majorVersion string
		foundExact := false
		for _, p := range r.Adobe.Products {
			if p.ID != code || !adobeMacPlatform(p) {
				continue
			}
			v, ok := adobeNumbers(p.Version)
			if !ok || len(v) == 0 {
				continue
			}
			if adobeCompare(v, current) == 0 {
				foundExact = true
			}
			if adobeCompare(v, current) <= 0 {
				continue
			}
			if v[0] == current[0] {
				if bestSame == nil || adobeCompare(v, bestSame) > 0 {
					bestSame = v
					sameVersion = p.Version
				}
			}
			if v[0] > current[0] {
				if bestMajor == nil || adobeCompare(v, bestMajor) > 0 {
					bestMajor = v
					majorVersion = p.Version
				}
			}
		}
		switch {
		case bestSame != nil:
			e.Status = "OUTDATED"
			e.Available = sameVersion
			e.UpdateSource = "adobe-catalog"
			e.Detail = "newer same-major catalogue release; applicability not verified"
		case bestMajor != nil:
			e.Status = "UPGRADE AVAILABLE"
			e.Available = majorVersion
			e.UpdateSource = "adobe-catalog"
			e.Detail = "newer major catalogue release; upgrade eligibility not verified"
		case foundExact:
			// Publishing a version in the catalogue does not prove that the
			// Creative Cloud updater checked this machine or that it's current.
			e.Status = "UNKNOWN"
			e.UpdateSource = "adobe-catalog"
			e.Detail = "catalogue includes installed version; eligibility not checked"
		}
	}
}
