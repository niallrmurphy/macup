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
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Options struct {
	FetchOnly       bool
	AllowUnverified bool
	Adopt           bool // run brew-adopt actions; without it they are skipped
	AppleRestart    bool // run Apple updates that need a restart; without it they are skipped
	Out, Err        io.Writer
}

// Outcome records what happened to one planned action.
type Outcome struct {
	Action Action
	Result string // ran, staged, opened, skipped, failed
	Err    error
}

// Execute runs the plan and returns one outcome per action. Homebrew
// actions that share a command are batched into a single invocation so
// dependencies are resolved once, and Apple updates share one sudo
// softwareupdate call; everything else runs one at a time.
// Adoptions replace an app Homebrew does not own, so they run only when
// opts.Adopt is set. Apple updates that need a restart run only when
// opts.AppleRestart is set, and macup never restarts the machine itself.
// Every executed or failed action is appended to the update log.
func Execute(ctx context.Context, plan []Action, opts Options) []Outcome {
	var out []Outcome
	var batches []*brewBatch
	for _, a := range plan {
		switch {
		case a.Route == RouteSparkle:
			res, err := fetchSparkle(ctx, a, opts)
			out = append(out, record(a, res, err, opts))
		case opts.FetchOnly:
			out = append(out, Outcome{Action: a, Result: "skipped"})
		case a.Route == RouteAdopt && !opts.Adopt:
			fmt.Fprintf(opts.Out, "skip   %s: %s (re-run with --adopt to let Homebrew replace it)\n", a.Name, strings.Join(a.Command, " "))
			out = append(out, Outcome{Action: a, Result: "skipped"})
		case a.Route == RouteApple && a.Restart && !opts.AppleRestart:
			fmt.Fprintf(opts.Out, "skip   %s: %s (needs a restart; re-run with --apple-restart to install it)\n", a.Name, strings.Join(a.Command, " "))
			out = append(out, Outcome{Action: a, Result: "skipped"})
		case a.Route == RouteApple && len(a.Command) < 4:
			out = append(out, record(a, "failed", errors.New("no command planned"), opts))
		case (a.Route == RouteBrew || a.Route == RouteAdopt) && len(a.Command) < 2:
			out = append(out, record(a, "failed", errors.New("no command planned"), opts))
		case a.Route == RouteBrew, a.Route == RouteAdopt, a.Route == RouteApple:
			batches = addToBatch(batches, a)
		case a.Route == RouteMas, a.Route == RouteMAU:
			err := delegate(ctx, a.Name, a.Command, opts)
			out = append(out, record(a, "ran", err, opts))
		default:
			fmt.Fprintf(opts.Out, "skip   %s: %s\n", a.Name, a.Reason)
			out = append(out, Outcome{Action: a, Result: "skipped"})
		}
	}
	for _, b := range batches {
		cmd := append(append([]string{}, b.prefix...), b.names...)
		err := delegate(ctx, b.label(), cmd, opts)
		for _, a := range b.actions {
			out = append(out, record(a, "ran", err, opts))
		}
	}
	return out
}

// brewBatch groups actions whose command differs only in its final argument
// (a formula, cask or softwareupdate label), so one call covers them all.
type brewBatch struct {
	prefix  []string
	names   []string
	actions []Action
}

func addToBatch(batches []*brewBatch, a Action) []*brewBatch {
	prefix, name := a.Command[:len(a.Command)-1], a.Command[len(a.Command)-1]
	for _, b := range batches {
		if strings.Join(b.prefix, " ") == strings.Join(prefix, " ") {
			b.names, b.actions = append(b.names, name), append(b.actions, a)
			return batches
		}
	}
	return append(batches, &brewBatch{prefix: prefix, names: []string{name}, actions: []Action{a}})
}

func (b *brewBatch) label() string {
	kind := "formulae"
	switch {
	case b.actions[0].Route == RouteApple:
		return fmt.Sprintf("%d Apple updates", len(b.names))
	case b.actions[0].Route == RouteAdopt:
		kind = "cask adoptions"
	case has(b.prefix, "--cask"):
		kind = "casks"
	}
	return fmt.Sprintf("%d Homebrew %s", len(b.names), kind)
}

func record(a Action, result string, err error, opts Options) Outcome {
	logAction(a, err)
	if err != nil {
		fmt.Fprintf(opts.Err, "failed %s: %v\n", a.Name, err)
		return Outcome{Action: a, Result: "failed", Err: err}
	}
	return Outcome{Action: a, Result: result}
}

// delegate runs the package manager's own command with its output visible.
func delegate(ctx context.Context, label string, command []string, opts Options) error {
	fmt.Fprintf(opts.Out, "run    %s: %s\n", label, strings.Join(command, " "))
	path, err := exec.LookPath(command[0])
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, command[1:]...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = opts.Out, opts.Err, os.Stdin
	cmd.Env = append(os.Environ(), "HOMEBREW_NO_ANALYTICS=1")
	return cmd.Run()
}

// BrewUpdate refreshes Homebrew's metadata so the plan reflects what
// `brew upgrade` would actually install, instead of letting the first
// upgrade trigger an auto-update that changes the versions mid-run.
func BrewUpdate(ctx context.Context, out, errw io.Writer) error {
	path, err := exec.LookPath("brew")
	if err != nil {
		return nil // no Homebrew, nothing to refresh
	}
	cmd := exec.CommandContext(ctx, path, "update", "--quiet")
	cmd.Stdout, cmd.Stderr = out, errw
	cmd.Env = append(os.Environ(), "HOMEBREW_NO_ANALYTICS=1")
	return cmd.Run()
}

// BrewVersions snapshots installed formula and cask versions, so the
// dependency side effects of an upgrade can be reported afterwards.
func BrewVersions(ctx context.Context) map[string]string {
	path, err := exec.LookPath("brew")
	if err != nil {
		return nil
	}
	versions := map[string]string{}
	for _, kind := range []string{"--formula", "--cask"} {
		cmd := exec.CommandContext(ctx, path, "list", kind, "--versions")
		cmd.Env = append(os.Environ(), "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ANALYTICS=1")
		raw, err := cmd.Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				versions[f[0]] = f[len(f)-1]
			}
		}
	}
	return versions
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

// stagingDir is what fetchSparkle actually consults; tests point it elsewhere.
var stagingDir = StagingDir

// fetchSparkle downloads the enclosure, verifies its EdDSA signature with the
// application's own public key, and opens it unless fetching only. An
// enclosure staged by an earlier run is reused when it still passes the same
// checks, so re-running update does not fetch the same bytes again. It never
// replaces the application bundle: Sparkle's installer, or the user via
// Finder, does that.
func fetchSparkle(ctx context.Context, a Action, opts Options) (string, error) {
	if a.Reason != "" && !opts.AllowUnverified {
		fmt.Fprintf(opts.Out, "skip   %s: %s (use --allow-unverified to download anyway)\n", a.Name, a.Reason)
		return "skipped", nil
	}
	u, err := url.Parse(a.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return "failed", fmt.Errorf("enclosure is not an HTTPS URL, refusing to fetch it: %q", a.URL)
	}
	// One directory per download: opening a zip makes Archive Utility expand
	// the .app beside it, and that belongs with its archive, not in the root.
	dir := filepath.Join(stagingDir(), unsafeName.ReplaceAllString(a.Name+"-"+a.Available, "_"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "failed", err
	}
	dest, data, err := stagedEnclosure(dir, a)
	if err != nil {
		return "failed", err
	}
	if dest != "" {
		fmt.Fprintf(opts.Out, "reuse  %s: %s\n", a.Name, dest)
	} else {
		fmt.Fprintf(opts.Out, "fetch  %s: %s\n", a.Name, a.URL)
		var finalURL, disposition string
		data, finalURL, disposition, err = download(ctx, a.URL, maxEnclosure)
		if err != nil {
			return "failed", err
		}
		dest = filepath.Join(dir, stagedName(a, finalURL, disposition, data))
		if a.Length > 0 && int64(len(data)) != a.Length {
			return "failed", fmt.Errorf("size %d does not match appcast length %d", len(data), a.Length)
		}
		if a.Signature != "" && a.PublicKey != "" {
			if err := verifyEdDSA(data, a.Signature, a.PublicKey); err != nil {
				hint := ""
				if !namesApp(path.Base(u.Path), a.Name) {
					// A feed that now serves a differently named product has usually
					// been renamed and re-keyed by the vendor; the installed app's key
					// cannot verify it and Sparkle itself would refuse too.
					hint = fmt.Sprintf("; the enclosure %q does not name %q, so the vendor may have renamed or re-keyed the product", path.Base(u.Path), a.Name)
				}
				return "failed", fmt.Errorf("EdDSA verification failed: %w (download discarded)%s", err, hint)
			}
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return "failed", err
		}
	}
	// Record what this is for so CleanStaging can retire it once installed.
	writeManifest(dir, a)
	// Mark the file as downloaded so Gatekeeper treats it like any other download.
	_ = exec.CommandContext(ctx, "/usr/bin/xattr", "-w", "com.apple.quarantine", "0083;00000000;macup;", dest).Run()
	status := "verified"
	if a.Signature == "" || a.PublicKey == "" {
		status = "UNVERIFIED"
	}
	fmt.Fprintf(opts.Out, "staged %s: %s (%s, %d bytes)\n", a.Name, dest, status, len(data))
	if opts.FetchOnly {
		return "staged", nil
	}
	fmt.Fprintf(opts.Out, "open   %s: %s\n", a.Name, dest)
	if err := exec.CommandContext(ctx, "/usr/bin/open", dest).Run(); err != nil {
		return "staged", err
	}
	return "opened", nil
}

// stagedEnclosure looks for an enclosure an earlier run left in dir and
// checks it as a fresh download is checked: size against the appcast, EdDSA
// signature against the app's key. The directory is keyed by app and version,
// so any installer-looking file in it is a candidate. One that no longer
// passes is removed so the fetch replaces it rather than leaving a stale
// file beside the new one.
func stagedEnclosure(dir string, a Action) (string, []byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !(installerExt(name) || strings.HasSuffix(name, ".bin")) {
			continue
		}
		p := filepath.Join(dir, name)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if a.Length > 0 && int64(len(data)) != a.Length {
			_ = os.Remove(p)
			continue
		}
		if a.Signature != "" && a.PublicKey != "" {
			if err := verifyEdDSA(data, a.Signature, a.PublicKey); err != nil {
				_ = os.Remove(p)
				continue
			}
		}
		return p, data, nil
	}
	return "", nil, nil
}

// stagedName picks a filename for the enclosure. Vendors often publish the
// enclosure through a redirector (HandBrake's rotation.php), so the original
// URL's path is the least reliable source: prefer Content-Disposition, then
// the URL that actually served the bytes, then the content's own magic.
func stagedName(a Action, finalURL, disposition string, data []byte) string {
	candidates := []string{}
	if i := strings.Index(strings.ToLower(disposition), "filename="); i >= 0 {
		candidates = append(candidates, strings.Trim(disposition[i+len("filename="):], `"; `))
	}
	for _, raw := range []string{finalURL, a.URL} {
		if u, err := url.Parse(raw); err == nil {
			candidates = append(candidates, filepath.Base(u.Path))
			if f := u.Query().Get("file"); f != "" {
				candidates = append(candidates, filepath.Base(f))
			}
		}
	}
	for _, c := range candidates {
		c = unsafeName.ReplaceAllString(c, "_")
		if installerExt(c) {
			return c
		}
	}
	base := unsafeName.ReplaceAllString(a.Name+"-"+a.Available, "_")
	return base + sniffExtension(data)
}

func installerExt(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range []string{".dmg", ".zip", ".pkg", ".mpkg", ".tar.xz", ".tar.gz", ".tgz", ".tbz", ".tar.bz2"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// sniffExtension recognises the formats Sparkle enclosures come in.
func sniffExtension(data []byte) string {
	switch {
	case len(data) >= 512 && string(data[len(data)-512:len(data)-508]) == "koly":
		return ".dmg"
	case len(data) >= 4 && string(data[:4]) == "PK\x03\x04":
		return ".zip"
	case len(data) >= 4 && string(data[:4]) == "xar!":
		return ".pkg"
	case len(data) >= 6 && string(data[:6]) == "\xfd7zXZ\x00":
		return ".tar.xz"
	case len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b:
		return ".tar.gz"
	case len(data) >= 3 && string(data[:3]) == "BZh":
		return ".tar.bz2"
	}
	return ".bin"
}

func download(ctx context.Context, raw string, max int) ([]byte, string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", "", err
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
		return nil, "", "", err
	}
	defer res.Body.Close()
	final := raw
	if res.Request != nil && res.Request.URL != nil {
		final = res.Request.URL.String()
	}
	if res.StatusCode != http.StatusOK {
		return nil, final, "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, int64(max)+1))
	if err != nil {
		return nil, final, "", err
	}
	if len(data) > max {
		return nil, final, "", fmt.Errorf("enclosure exceeds %d bytes", max)
	}
	return data, final, res.Header.Get("Content-Disposition"), nil
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

// namesApp reports whether a filename contains the app's name, ignoring
// case, spaces, URL escapes and punctuation.
func namesApp(filename, app string) bool {
	squash := func(s string) string {
		s, _ = url.PathUnescape(s)
		return unsafeName.ReplaceAllString(strings.ToLower(strings.ReplaceAll(s, " ", "")), "")
	}
	a := squash(app)
	return a == "" || strings.Contains(squash(filename), a)
}
