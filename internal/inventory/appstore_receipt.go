package inventory

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// MetadataCommand queries the Spotlight Adam ID attached to an application.
// The App Store receipt is checked separately; neither a name nor an ID alone
// is enough to attribute an installed application to the App Store.
type MetadataCommand func(context.Context, string) (string, error)

func spotlightAdamID(ctx context.Context, path string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "/usr/bin/mdls", "-raw", "-name", "kMDItemAppStoreAdamID", path).Output()
	return strings.TrimSpace(string(out)), err
}

var adamIDPattern = regexp.MustCompile(`^[0-9]+$`)

// AnnotateAppStoreIDs annotates discovered apps only when they have a regular
// receipt file and a numeric Spotlight Adam ID. This is metadata correlation,
// not cryptographic verification of the receipt or proof of purchase.
func AnnotateAppStoreIDs(ctx context.Context, r *Result) {
	AnnotateAppStoreIDsWith(ctx, r, spotlightAdamID)
}

func AnnotateAppStoreIDsWith(ctx context.Context, r *Result, metadata MetadataCommand) {
	if metadata == nil {
		metadata = spotlightAdamID
	}
	for i := range r.Applications {
		if ctx.Err() != nil {
			return
		}
		app := &r.Applications[i]
		receipt := filepath.Join(app.Path, "Contents", "_MASReceipt", "receipt")
		info, err := os.Lstat(receipt)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		id, err := metadata(ctx, app.Path)
		id = strings.TrimSpace(id)
		if err == nil && adamIDPattern.MatchString(id) && id != "0" {
			app.AppStoreID = id
		}
	}
}
