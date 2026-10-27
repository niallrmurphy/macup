package update

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Options struct {
	FetchOnly       bool
	AllowUnverified bool
	Out, Err        io.Writer
}

// Execute runs the plan and returns the number of failed actions. Every
// action is appended to the update log.
func Execute(ctx context.Context, plan []Action, opts Options) int {
	failed := 0
	for _, a := range plan {
		var err error
		switch {
		case a.Route == RouteSparkle:
			err = fetchSparkle(ctx, a, opts)
		case opts.FetchOnly:
			continue
		case a.Route == RouteBrew, a.Route == RouteMas, a.Route == RouteMAU:
			err = delegate(ctx, a, opts)
		default:
			fmt.Fprintf(opts.Out, "skip   %s: %s\n", a.Name, a.Reason)
			continue
		}
		logAction(a, err)
		if err != nil {
			failed++
			fmt.Fprintf(opts.Err, "failed %s: %v\n", a.Name, err)
		}
	}
	return failed
}

// delegate runs the package manager's own command with its output visible.
func delegate(ctx context.Context, a Action, opts Options) error {
	fmt.Fprintf(opts.Out, "run    %s: %s\n", a.Name, strings.Join(a.Command, " "))
	path, err := exec.LookPath(a.Command[0])
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, a.Command[1:]...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = opts.Out, opts.Err, os.Stdin
	cmd.Env = append(os.Environ(), "HOMEBREW_NO_ANALYTICS=1")
	return cmd.Run()
}

const maxEnclosure = 1 << 30

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// StagingDir is where verified downloads land.
func StagingDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "macup-downloads")
	}
	return filepath.Join(dir, "macup", "downloads")
}

// fetchSparkle downloads the enclosure, verifies its EdDSA signature with the
// application's own public key, and opens it unless fetching only. It never
// replaces the application bundle: Sparkle's installer, or the user via
// Finder, does that.
func fetchSparkle(ctx context.Context, a Action, opts Options) error {
	if a.Reason != "" && !opts.AllowUnverified {
		fmt.Fprintf(opts.Out, "skip   %s: %s (use --allow-unverified to download anyway)\n", a.Name, a.Reason)
		return nil
	}
	u, err := url.Parse(a.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return errors.New("enclosure is not a public HTTPS URL")
	}
	dir := StagingDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := unsafeName.ReplaceAllString(filepath.Base(u.Path), "_")
	if name == "" || name == "." || name == "_" {
		name = unsafeName.ReplaceAllString(a.Name, "_") + "-" + unsafeName.ReplaceAllString(a.Available, "_")
	}
	dest := filepath.Join(dir, name)
	fmt.Fprintf(opts.Out, "fetch  %s: %s\n", a.Name, a.URL)
	data, err := download(ctx, a.URL, maxEnclosure)
	if err != nil {
		return err
	}
	if a.Length > 0 && int64(len(data)) != a.Length {
		return fmt.Errorf("size %d does not match appcast length %d", len(data), a.Length)
	}
	verified := false
	if a.Signature != "" && a.PublicKey != "" {
		if err := verifyEdDSA(data, a.Signature, a.PublicKey); err != nil {
			return fmt.Errorf("EdDSA verification failed: %w (download discarded)", err)
		}
		verified = true
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return err
	}
	// Mark the file as downloaded so Gatekeeper treats it like any other download.
	_ = exec.CommandContext(ctx, "/usr/bin/xattr", "-w", "com.apple.quarantine", "0083;00000000;macup;", dest).Run()
	status := "verified"
	if !verified {
		status = "UNVERIFIED"
	}
	fmt.Fprintf(opts.Out, "staged %s: %s (%s, %d bytes)\n", a.Name, dest, status, len(data))
	if opts.FetchOnly {
		return nil
	}
	fmt.Fprintf(opts.Out, "open   %s: %s\n", a.Name, dest)
	return exec.CommandContext(ctx, "/usr/bin/open", dest).Run()
}

func download(ctx context.Context, raw string, max int) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "macup-update/0.1")
	client := &http.Client{Timeout: 15 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if r.URL.Scheme != "https" {
			return errors.New("redirect to non-HTTPS URL blocked")
		}
		return nil
	}}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, int64(max)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > max {
		return nil, fmt.Errorf("enclosure exceeds %d bytes", max)
	}
	return data, nil
}

// verifyEdDSA checks a Sparkle 2 EdDSA signature: ed25519 over the whole
// file, with the signature and the app's SUPublicEDKey both base64.
func verifyEdDSA(data []byte, signature, publicKey string) error {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return fmt.Errorf("signature is not base64: %w", err)
	}
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKey))
	if err != nil {
		return fmt.Errorf("public key is not base64: %w", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("public key is %d bytes, want %d", len(pub), ed25519.PublicKeySize)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("signature is %d bytes, want %d", len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), data, sig) {
		return errors.New("signature does not match")
	}
	return nil
}

func logAction(a Action, err error) {
	dir, e := os.UserCacheDir()
	if e != nil {
		return
	}
	entry := map[string]any{"time": time.Now().UTC().Format(time.RFC3339), "name": a.Name, "route": a.Route, "available": a.Available, "command": a.Command, "url": a.URL}
	if err != nil {
		entry["error"] = err.Error()
	}
	raw, _ := json.Marshal(entry)
	_ = os.MkdirAll(filepath.Join(dir, "macup"), 0o755)
	f, e := os.OpenFile(filepath.Join(dir, "macup", "update.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if e != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(raw, '\n'))
}
