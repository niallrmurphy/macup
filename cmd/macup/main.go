package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/niallrmurphy/macup/internal/cache"
	"github.com/niallrmurphy/macup/internal/diagnostics"
	"github.com/niallrmurphy/macup/internal/inventory"
	"github.com/niallrmurphy/macup/internal/output"
	"github.com/niallrmurphy/macup/internal/probe"
	"github.com/niallrmurphy/macup/internal/update"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Set at build time: -ldflags "-X main.version=1.0.0 -X main.commit=abc -X main.date=2026-10-28".
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func versionString() string {
	s := "macup " + version
	if commit != "" {
		s += " (" + commit
		if date != "" {
			s += ", " + date
		}
		s += ")"
	}
	return s
}

func main() { os.Exit(run(os.Args[1:])) }
func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 0
	}
	switch args[0] {
	case "probe":
		fs := flag.NewFlagSet("probe", flag.ContinueOnError)
		fetch := fs.Bool("fetch", false, "fetch up to three candidate public HTTPS pages per app")
		lookup := fs.Bool("lookup", false, "query keyed public sources: App Store catalogue, Homebrew cask index, release feeds")
		origins := fs.Bool("origins", false, "read local download-origin records (quarantine database, Spotlight where-froms)")
		model := fs.String("model", "", "optional local Ollama model (requires --fetch)")
		pages := fs.Int("max-pages", 3, "maximum pages per app (1 to 3)")
		all := fs.Bool("all", false, "probe every discovered application")
		roots := fs.String("roots", "", "comma-separated application roots (requires --all)")
		includeSystem := fs.Bool("include-system", false, "include macOS-managed apps (requires --all)")
		workers := fs.Int("workers", 2, "concurrent app probes in --all mode (1 to 4)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *pages < 1 || *pages > 3 || *workers < 1 || *workers > 4 {
			fmt.Fprintln(os.Stderr, "--max-pages must be 1..3; --workers must be 1..4")
			return 2
		}
		if *model != "" && !*fetch {
			fmt.Fprintln(os.Stderr, "--model requires --fetch")
			return 2
		}
		opt := probe.Options{Fetch: *fetch, Lookup: *lookup, Origins: *origins, Model: *model, MaxPages: *pages}
		if *all {
			if fs.NArg() != 0 {
				fmt.Fprintln(os.Stderr, "--all cannot be combined with an application path")
				return 2
			}
			result := inventory.Scan(context.Background(), selectedRoots(*roots))
			if !*includeSystem {
				result.Applications = inventory.FilterSystemApplications(result.Applications)
			}
			batch := runProbeBatch(context.Background(), result.Applications, result.Warnings, opt, *workers)
			e := json.NewEncoder(os.Stdout)
			e.SetIndent("", "  ")
			if err := e.Encode(batch); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return 0
		}
		if fs.NArg() != 1 || *roots != "" || *includeSystem {
			fmt.Fprintln(os.Stderr, "usage: macup probe [--all [--roots DIRS] [--include-system]] [--lookup] [--origins] [--fetch] [--model MODEL] [--max-pages 1..3] [--workers 1..4] [APP.app]")
			return 2
		}
		r, err := probe.Run(context.Background(), fs.Arg(0), opt)
		if err != nil {
			fmt.Fprintln(os.Stderr, "probe:", err)
			return 1
		}
		e := json.NewEncoder(os.Stdout)
		e.SetIndent("", "  ")
		if err := e.Encode(r); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	case "scan":
		fs := flag.NewFlagSet("scan", flag.ContinueOnError)
		jsonOut := fs.Bool("json", false, "print machine-readable JSON")
		appsFlag := fs.Bool("apps", false, "scan installed macOS .app bundles")
		includeSystem := fs.Bool("include-system", false, "include macOS-managed system application bundles")
		brewFlag := fs.Bool("brew", false, "scan installed Homebrew formulae and casks")
		sparkleFlag := fs.Bool("sparkle", false, "check discovered apps with Sparkle appcasts (network access)")
		quietFlag := fs.Bool("quiet", false, "suppress scan progress messages on stderr")
		verboseFlag := fs.Bool("verbose", false, "print per-feed Sparkle progress on stderr")
		allFlag := fs.Bool("all", false, "show entries already known to be current (table output only)")
		statusFlag := fs.String("status", "", "show only these statuses, comma-separated, e.g. 'NO SOURCE' or 'OUTDATED,UPGRADE AVAILABLE' (table output only)")
		pathsFlag := fs.Bool("paths", false, "show bundle paths in the DETAIL column (table output only)")
		lookupFlag := fs.Bool("lookup", false, "enable the lookup provider (on by default): public catalogues and release feeds for apps no other provider evaluated")
		noLookupFlag := fs.Bool("no-lookup", false, "disable the lookup provider for this run")
		cacheTTL := fs.Duration("cache-ttl", time.Hour, "reuse Sparkle, lookup, Microsoft and Adobe results younger than this; 0 disables the cache")
		freshFlag := fs.Bool("fresh", false, "ignore cached provider results for this run (results are still stored)")
		debugFlag := fs.Bool("sparkle-debug", false, "print Sparkle per-feed diagnostics to stderr (implies --sparkle)")
		debugXMLDir := fs.String("sparkle-debug-xml", "", "directory for sanitized appcast XML structure samples (implies --sparkle-debug)")
		appStoreFlag := fs.Bool("appstore", false, "scan Mac App Store software via mas")
		providersFlag := fs.String("providers", "all", "update-check providers: all, none, microsoft, sparkle, adobe, lookup (comma-separated)")
		offlineFlag := fs.Bool("offline", false, "nothing leaves the machine: local inventory and Homebrew's local state only; no providers, no App Store query, no cache")
		rootsFlag := fs.String("roots", "", "comma-separated directories to scan (defaults to standard macOS app directories)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if fs.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "unexpected arguments:", fs.Args())
			return 2
		}
		cfg := scanConfig{providersFlag: *providersFlag, sparkleFlag: *sparkleFlag, debugFlag: *debugFlag, debugXMLDir: *debugXMLDir, lookupFlag: *lookupFlag, noLookupFlag: *noLookupFlag, appsFlag: *appsFlag, brewFlag: *brewFlag, appStoreFlag: *appStoreFlag, rootsFlag: *rootsFlag, offlineFlag: *offlineFlag, cacheTTL: *cacheTTL, freshFlag: *freshFlag, quietFlag: *quietFlag, verboseFlag: *verboseFlag, includeSystem: *includeSystem}
		result, err := performScan(cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if *jsonOut {
			err = output.JSON(os.Stdout, result)
		} else {
			opts := output.TableOptions{ShowAll: *allFlag, Paths: *pathsFlag}
			if strings.TrimSpace(*statusFlag) != "" {
				opts.Statuses = strings.Split(*statusFlag, ",")
			}
			err = output.TableWith(os.Stdout, result, opts)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	case "update":
		fs := flag.NewFlagSet("update", flag.ContinueOnError)
		yes := fs.Bool("yes", false, "execute the plan; without it only the plan is printed")
		fetchOnly := fs.Bool("fetch-only", false, "download and verify Sparkle updates into the staging directory, nothing else")
		allowUnverified := fs.Bool("allow-unverified", false, "download Sparkle enclosures that cannot be signature-verified (not recommended)")
		adopt := fs.Bool("adopt", false, "with --yes: bring apps that match a Homebrew cask under Homebrew (brew install --cask --force TOKEN); replaces the installed app")
		only := fs.String("only", "", "comma-separated names (case-insensitive substrings) to limit the plan to")
		quietFlag := fs.Bool("quiet", false, "suppress scan progress messages on stderr")
		freshFlag := fs.Bool("fresh", false, "ignore cached provider results for the scan")
		skipBrewUpdate := fs.Bool("skip-brew-update", false, "do not run `brew update` before planning")
		noRescan := fs.Bool("no-rescan", false, "skip the rescan and summary after executing")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if fs.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "unexpected arguments:", fs.Args())
			return 2
		}
		cfg := defaultScanConfig()
		cfg.quietFlag, cfg.freshFlag = *quietFlag, *freshFlag
		announce := func(format string, args ...any) {
			if !*quietFlag {
				fmt.Fprintf(os.Stderr, format+"\n", args...)
			}
		}
		if (*yes || *fetchOnly) && !*skipBrewUpdate {
			// Otherwise the first `brew upgrade` auto-updates Homebrew and the
			// versions installed differ from the ones the plan advertised.
			announce("Homebrew: refreshing metadata (brew update)")
			if err := update.BrewUpdate(context.Background(), os.Stderr, os.Stderr); err != nil {
				announce("Homebrew: brew update failed: %v", err)
			}
		}
		result, err := performScan(cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		plan := update.Plan(result)
		if strings.TrimSpace(*only) != "" {
			plan = update.Filter(plan, strings.Split(*only, ","))
		}
		if !*yes && !*fetchOnly {
			if err := update.PrintPlan(os.Stdout, plan); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			fmt.Fprintln(os.Stderr, "Plan only. Re-run with --yes to execute, or --fetch-only to download and verify Sparkle updates.")
			for _, a := range plan {
				if a.Route == update.RouteAdopt {
					fmt.Fprintln(os.Stderr, "brew-adopt actions replace an app Homebrew does not own; they run only with --yes --adopt.")
					break
				}
			}
			return 0
		}
		opts := update.Options{FetchOnly: *fetchOnly, AllowUnverified: *allowUnverified, Adopt: *adopt, Out: os.Stdout, Err: os.Stderr}
		brewBefore := update.BrewVersions(context.Background())
		outcomes := update.Execute(context.Background(), plan, opts)
		failed := 0
		for _, o := range outcomes {
			if o.Result == "failed" {
				failed++
			}
		}
		if *noRescan {
			if failed > 0 {
				fmt.Fprintf(os.Stderr, "%d actions failed\n", failed)
				return 1
			}
			return 0
		}
		announce("Rescanning to confirm what changed")
		after, err := performScan(cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		update.Summarise(outcomes, after, brewBefore, update.BrewVersions(context.Background())).Print(os.Stdout)
		if failed > 0 {
			return 1
		}
		return 0
	case "doctor":
		fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
		rootsFlag := fs.String("roots", "", "comma-separated directories to inspect")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if fs.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "unexpected arguments:", fs.Args())
			return 2
		}
		if err := diagnostics.Run(os.Stdout, selectedRoots(*rootsFlag)); err != nil {
			return 1
		}
		return 0
	case "version", "--version", "-v":
		fmt.Println(versionString())
		return 0
	case "help", "-h", "--help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		usage()
		return 2
	}
}

// runLookups queries keyed public sources for entries no provider evaluated,
// with a small worker pool, and folds the verdicts into the result.
// scanConfig carries the scan command's flags so `update` can run the same
// scan before planning.
type scanConfig struct {
	providersFlag                                    string
	sparkleFlag, debugFlag                           bool
	debugXMLDir                                      string
	lookupFlag, noLookupFlag                         bool
	appsFlag, brewFlag, appStoreFlag                 bool
	rootsFlag                                        string
	offlineFlag                                      bool
	cacheTTL                                         time.Duration
	freshFlag, quietFlag, verboseFlag, includeSystem bool
}

func defaultScanConfig() scanConfig {
	return scanConfig{providersFlag: "all", cacheTTL: time.Hour}
}

// performScan runs sources and providers and returns the reconciled result.
func performScan(cfg scanConfig) (inventory.Result, error) {
	// Inventory sources and update-check providers are independent selections.
	providerSet, err := parseProviders(cfg.providersFlag)
	if err != nil {
		return inventory.Result{}, err
	}
	if cfg.sparkleFlag || cfg.debugFlag || cfg.debugXMLDir != "" {
		providerSet["sparkle"] = true
	}
	if cfg.lookupFlag {
		providerSet["lookup"] = true
	}
	if cfg.noLookupFlag {
		delete(providerSet, "lookup")
	}
	explicitSource := cfg.appsFlag || cfg.brewFlag || cfg.appStoreFlag || strings.TrimSpace(cfg.rootsFlag) != ""
	useApps := !explicitSource || cfg.appsFlag || strings.TrimSpace(cfg.rootsFlag) != ""
	useBrew := !explicitSource || cfg.brewFlag
	useAppStore := !explicitSource || cfg.appStoreFlag
	if cfg.offlineFlag {
		// Nothing leaves the machine. mas asks Apple for outdated apps, every
		// provider talks to a vendor or catalogue, and the cache holds
		// answers from earlier network runs; all three are off.
		providerSet = map[string]bool{}
		useAppStore = false
		cfg.cacheTTL = 0
	}
	useMicrosoft := providerSet["microsoft"] && useApps
	useAdobe := providerSet["adobe"] && useApps
	useSparkle := providerSet["sparkle"] && useApps
	useLookup := providerSet["lookup"] && useApps

	// Every source and provider announces itself on stderr, so slow steps
	// (msupdate, catalogue fetches) are visible without touching stdout.
	announce := func(format string, args ...any) {
		if !cfg.quietFlag {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		}
	}
	store := cache.Open(cfg.cacheTTL)
	store.Prune()
	cached := func(key string, v any) bool { return !cfg.freshFlag && store.Get(key, v) }
	result := inventory.Result{Applications: []inventory.Application{}, Warnings: []inventory.Warning{}, Roots: []string{}}
	if cfg.offlineFlag {
		announce("Offline: nothing leaves the machine this run")
	}
	if useApps {
		roots := selectedRoots(cfg.rootsFlag)
		announce("Applications: scanning %d roots for .app bundles", len(roots))
		result = inventory.Scan(context.Background(), roots)
		if !cfg.includeSystem {
			result.Applications = inventory.FilterSystemApplications(result.Applications)
		}
		announce("Applications: %d bundles found; %d warnings", len(result.Applications), len(result.Warnings))
	}
	if useBrew {
		announce("Homebrew: listing installed and outdated formulae and casks")
		b := inventory.ScanHomebrew(context.Background())
		result.Homebrew = &b
		announce("Homebrew: %s", summariseOutdated(b.Available, len(b.Packages), countBrewOutdated(b.Packages)))
	}
	if useAppStore {
		announce("App Store: querying mas for installed and outdated apps")
		a := inventory.ScanAppStore(context.Background())
		result.AppStore = &a
		announce("App Store: %s", summariseOutdated(a.Available, len(a.Packages), countMasOutdated(a.Packages)))
		if useApps && a.Available {
			inventory.AnnotateAppStoreIDs(context.Background(), &result)
		}
	}
	inventory.Reconcile(&result)
	if useMicrosoft {
		key := "microsoft|" + versionsKey(result.Applications, "com.microsoft.")
		var m inventory.MicrosoftResult
		if cached(key, &m) && m.CheckStatus == "checked" {
			announce("Microsoft: using cached msupdate result")
		} else {
			announce("Microsoft: running msupdate --list (can take over a minute)")
			m = inventory.ScanMicrosoft(context.Background())
			if m.CheckStatus == "checked" {
				store.Put(key, m)
			}
		}
		result.Microsoft = &m
		inventory.ApplyMicrosoft(&result)
		announce("Microsoft: %s", summariseCheck(m.CheckStatus, len(m.Updates), "updates reported"))
	}
	if useAdobe {
		key := "adobe|" + versionsKey(result.Applications, "com.adobe.")
		var a inventory.AdobeResult
		if cached(key, &a) && a.CheckStatus == "checked" {
			announce("Adobe: using cached catalogue result")
		} else {
			announce("Adobe: reading installed products and fetching the release catalogue")
			a = inventory.ScanAdobe(context.Background())
			if a.CheckStatus == "checked" {
				store.Put(key, a)
			}
		}
		result.Adobe = &a
		inventory.ApplyAdobe(&result)
		announce("Adobe: %s", summariseCheck(a.CheckStatus, len(a.Products), "catalogue releases"))
	}
	if useSparkle {
		// Feeds set in code or in user defaults are invisible to Info.plist alone.
		inventory.SparkleFeedFinder = probe.SparkleFeed
		var progress inventory.SparkleProgressFunc
		// Per-feed lines are noisy once the checker is trusted; the summary below stays.
		if cfg.verboseFlag || cfg.debugFlag || cfg.debugXMLDir != "" {
			progress = func(e inventory.SparkleProgress) {
				switch e.Phase {
				case "start":
					fmt.Fprintf(os.Stderr, "Sparkle [%d/%d] %s: checking feed...\n", e.Index, e.Total, e.Name)
				case "done", "skip":
					detail := e.Status
					if e.Reason != "" {
						detail += " (" + e.Reason + ")"
					}
					if e.Phase == "skip" {
						fmt.Fprintf(os.Stderr, "Sparkle [%d/%d] %s: %s (skipped before fetch)\n", e.Index, e.Total, e.Name, detail)
					} else {
						fmt.Fprintf(os.Stderr, "Sparkle [%d/%d] %s: %s (%s)\n", e.Index, e.Total, e.Name, detail, e.Duration.Round(time.Millisecond))
					}
					if cfg.debugFlag || cfg.debugXMLDir != "" {
						printSparkleDiagnostics(os.Stderr, e.Check)
					}
				}
			}
		}
		// Reuse checks for apps whose installed version has not changed.
		var toCheck []inventory.Application
		var reused []inventory.SparkleCheck
		noFeed := 0
		for _, a := range result.Applications {
			var c inventory.SparkleCheck
			switch {
			case !cached(sparkleKey(a), &c):
				toCheck = append(toCheck, a)
			case c.Status == "NO FEED":
				noFeed++ // remembered as having nothing to check; not a result
			default:
				reused = append(reused, c)
			}
		}
		switch {
		case len(reused)+noFeed > 0 && len(toCheck) == 0:
			announce("Sparkle: all %d applications served from cache (%d feeds, %d without one)", len(reused)+noFeed, len(reused), noFeed)
		case len(reused)+noFeed > 0:
			announce("Sparkle: %d applications served from cache; examining %d for update feeds", len(reused)+noFeed, len(toCheck))
		default:
			announce("Sparkle: examining %d applications for update feeds", len(toCheck))
		}
		var s inventory.SparkleResult
		if cfg.debugXMLDir != "" {
			s = inventory.CheckSparkleWithXMLSamples(context.Background(), toCheck, nil, progress, cfg.debugXMLDir)
		} else if cfg.debugFlag {
			s = inventory.CheckSparkleWithDiagnostics(context.Background(), toCheck, nil, progress)
		} else {
			s = inventory.CheckSparkleWithProgress(context.Background(), toCheck, nil, progress)
		}
		byPath := map[string]inventory.Application{}
		for _, a := range toCheck {
			byPath[a.Path] = a
		}
		// Unchecked results are cached too: most are deterministic (no
		// comparable release, invalid feed URL) and a transient failure is
		// retried after the TTL or with --fresh.
		for _, c := range s.Checks {
			if a, ok := byPath[c.Path]; ok {
				store.Put(sparkleKey(a), c)
			}
			delete(byPath, c.Path)
		}
		// Apps that produced no check have no feed; remember that so the next
		// scan does not re-inspect their bundles.
		for path, a := range byPath {
			store.Put(sparkleKey(a), inventory.SparkleCheck{Path: path, Status: "NO FEED"})
		}
		s.Checks = append(s.Checks, reused...)
		result.Sparkle = &s
		inventory.ApplySparkle(&result)
		if !cfg.quietFlag || cfg.debugFlag || cfg.debugXMLDir != "" {
			var outdated, notFlagged, unchecked int
			for _, c := range s.Checks {
				switch c.Status {
				case "OUTDATED":
					outdated++
				case "NOT FLAGGED":
					notFlagged++
				default:
					unchecked++
				}
			}
			fmt.Fprintf(os.Stderr, "Sparkle: %d feeds found; %d outdated, %d not flagged, %d unchecked\n", len(s.Checks), outdated, notFlagged, unchecked)
			if cfg.debugFlag {
				fmt.Fprintln(os.Stderr, "Sparkle: every discovered feed is included above, including pre-fetch skips")
			}
		}
	}
	if useApps {
		// Label what remains: apps that update themselves, or are updated by
		// the App Store, a browser, a vendor agent, or their parent product.
		inventory.ApplyUpdaters(&result, probe.Updater)
	}
	if useLookup {
		runLookups(context.Background(), &result, announce, cached, store.Put)
	} else if useApps && !cfg.offlineFlag {
		unresolved := 0
		for _, e := range result.Entries {
			if inventory.LookupEligible(e) {
				unresolved++
			}
		}
		if unresolved > 0 {
			announce("Lookup: skipped; %d applications have no version source (public catalogues and feeds are consulted with --lookup)", unresolved)
		}
	}
	if cfg.offlineFlag {
		announce("Offline: no network provider ran; statuses reflect local evidence and Homebrew's local state only")
	}
	return result, nil
}

func runLookups(ctx context.Context, result *inventory.Result, announce func(string, ...any), cached func(string, any) bool, put func(string, any)) {
	byPath := map[string]inventory.Application{}
	for _, a := range result.Applications {
		byPath[a.Path] = a
	}
	var targets []inventory.Application
	verdicts := map[string][]inventory.LookupVerdict{}
	reused := 0
	for _, e := range result.Entries {
		a, ok := byPath[e.Path]
		if !ok || !inventory.LookupEligible(e) {
			continue
		}
		var vs []inventory.LookupVerdict
		if cached(lookupKey(a), &vs) {
			verdicts[a.Path] = vs
			reused++
			continue
		}
		targets = append(targets, a)
	}
	if reused > 0 {
		announce("Lookup: %d results reused from cache", reused)
	}
	announce("Lookup: querying App Store catalogue, Homebrew cask index and release feeds for %d applications", len(targets))
	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan inventory.Application)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for a := range jobs {
				vs, _ := probe.LookupVersions(ctx, a)
				if vs == nil {
					vs = []inventory.LookupVerdict{}
				}
				mu.Lock()
				verdicts[a.Path] = vs
				mu.Unlock()
				put(lookupKey(a), vs)
			}
		}()
	}
	for _, a := range targets {
		jobs <- a
	}
	close(jobs)
	wg.Wait()
	inventory.ApplyLookups(result, verdicts)
	var outdated, notFlagged int
	for _, e := range result.Entries {
		if len(e.Sources) == 2 && e.Sources[1] == "lookup" {
			if e.Status == "OUTDATED" {
				outdated++
			} else {
				notFlagged++
			}
		}
	}
	announce("Lookup: %d outdated, %d not flagged, %d without a comparable version", outdated, notFlagged, len(targets)+reused-outdated-notFlagged)
}

// Cache keys include everything whose change must invalidate the entry: the
// bundle path and its installed version and build, and a schema number that
// is bumped whenever the cached structure gains fields.
const cacheSchema = "2"

func sparkleKey(a inventory.Application) string {
	return "sparkle|" + cacheSchema + "|" + a.Path + "|" + a.Version + "|" + a.Build
}
func lookupKey(a inventory.Application) string {
	return "lookup|" + cacheSchema + "|" + a.Path + "|" + a.Version + "|" + a.Build
}

// versionsKey summarises the installed versions of one vendor's apps so a
// cached vendor-wide result is dropped once anything of theirs changes.
func versionsKey(apps []inventory.Application, bundlePrefix string) string {
	var parts []string
	for _, a := range apps {
		if strings.HasPrefix(strings.ToLower(a.BundleID), bundlePrefix) {
			parts = append(parts, a.BundleID+"="+a.Version+"/"+a.Build)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

// summariseOutdated phrases a package-manager result for the progress line.
func summariseOutdated(available bool, total, outdated int) string {
	if !available {
		return "unavailable"
	}
	return fmt.Sprintf("%d packages; %d outdated", total, outdated)
}

// summariseCheck phrases a provider's check status for the progress line.
func summariseCheck(status string, count int, noun string) string {
	switch status {
	case "checked":
		return fmt.Sprintf("checked; %d %s", count, noun)
	case "":
		return "unavailable"
	}
	return status
}

func countBrewOutdated(pkgs []inventory.BrewPackage) int {
	n := 0
	for _, p := range pkgs {
		if p.Outdated {
			n++
		}
	}
	return n
}

func countMasOutdated(pkgs []inventory.AppStorePackage) int {
	n := 0
	for _, p := range pkgs {
		if p.Outdated {
			n++
		}
	}
	return n
}

func printSparkleDiagnostics(w *os.File, c inventory.SparkleCheck) {
	fmt.Fprintf(w, "  Feed: %s\n  Installed build: %s\n", c.FeedURL, c.InstalledBuild)
	if c.Diagnostics == nil {
		return
	}
	d := c.Diagnostics
	fmt.Fprintf(w, "  Version fields: item=%d enclosure=%d; display fields: item=%d enclosure=%d; enclosure URLs=%d\n", d.ItemVersions, d.EnclosureVersions, d.ItemShortVersions, d.EnclosureShortVersions, d.EnclosureURLs)
	if d.XMLSample != "" {
		fmt.Fprintf(w, "  XML structure: %s\n", d.XMLSample)
	}
	fmt.Fprintf(w, "  Items: %d; comparable: %d; skipped channel: %d; delta: %d; incompatible OS: %d; unknown OS: %d; minimumUpdateVersion: %d; missing version: %d; incomparable: %d\n", d.TotalItems, d.Comparable, d.SkippedChannel, d.SkippedDelta, d.SkippedOS, d.UnknownOS, d.SkippedMinimumUpdate, d.MissingVersion, d.UncomparableVersion)
	if c.AvailableBuild != "" {
		fmt.Fprintf(w, "  Available build: %s (display: %s)\n", c.AvailableBuild, c.AvailableVersion)
	}
}

// ProbeItem retains per-application errors without aborting the whole inventory.
type ProbeItem struct {
	Path   string        `json:"path"`
	Report *probe.Report `json:"report,omitempty"`
	Error  string        `json:"error,omitempty"`
}
type ProbeBatch struct {
	Total     int                 `json:"total"`
	Succeeded int                 `json:"succeeded"`
	Failed    int                 `json:"failed"`
	Warnings  []inventory.Warning `json:"inventory_warnings"`
	Results   []ProbeItem         `json:"results"`
}

func runProbeBatch(ctx context.Context, apps []inventory.Application, warnings []inventory.Warning, opt probe.Options, workers int) ProbeBatch {
	batch := ProbeBatch{Total: len(apps), Warnings: warnings, Results: make([]ProbeItem, len(apps))}
	// A small bounded pool prevents hundreds of simultaneous HTTP/Ollama requests.
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				app := apps[i]
				r, err := probe.Run(ctx, app.Path, opt)
				item := ProbeItem{Path: app.Path}
				if err != nil {
					item.Error = err.Error()
				} else {
					item.Report = &r
				}
				batch.Results[i] = item
			}
		}()
	}
	for i := range apps {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	for _, item := range batch.Results {
		if item.Error != "" {
			batch.Failed++
		} else {
			batch.Succeeded++
		}
	}
	fmt.Fprintf(os.Stderr, "Probe: %d applications; %d succeeded, %d failed\n", batch.Total, batch.Succeeded, batch.Failed)
	return batch
}

func selectedRoots(s string) []string {
	if strings.TrimSpace(s) == "" {
		return inventory.DefaultRoots()
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
func usage() {
	fmt.Println(`macup — read-only macOS software inventory

Usage:
  macup scan [--json] [--apps] [--include-system] [--brew] [--appstore] [--providers all|none|microsoft,sparkle,adobe,lookup] [--offline] [--sparkle] [--sparkle-debug] [--sparkle-debug-xml DIR] [--lookup|--no-lookup] [--cache-ttl 1h] [--fresh] [--quiet] [--verbose] [--all] [--status "NO SOURCE"] [--paths] [--roots /Applications,/Users/me/Applications]
  macup version
  macup update [--only NAME,...] [--yes [--adopt] | --fetch-only] [--allow-unverified] [--skip-brew-update] [--no-rescan] [--fresh] [--quiet]
  macup probe [--lookup] [--origins] [--fetch] [--model llama3.2:latest] [--max-pages 3] /Applications/Example.app
  macup probe --all [--roots /Applications] [--include-system] [--lookup] [--origins] [--fetch] [--model MODEL] [--workers 2]
  macup doctor [--roots /Applications]
  macup help

With no source flags, all available inventory sources are scanned.
--providers selects update-check providers; defaults to all.
Source flags select only the named sources; --roots implies --apps.
System-volume apps are hidden by default; --include-system shows them.
--sparkle enables opt-in HTTPS appcast checks for discovered apps.
--sparkle-debug implies --sparkle and explains release filtering on stderr.
--sparkle-debug-xml DIR also saves structural XML samples (without values) to DIR.
--quiet suppresses routine progress on stderr (debug output remains enabled).
No applications are updated or modified.`)
}

func parseProviders(raw string) (map[string]bool, error) {
	p := map[string]bool{}
	parts := strings.Split(strings.ToLower(strings.TrimSpace(raw)), ",")
	for _, part := range parts {
		switch strings.TrimSpace(part) {
		case "all":
			p["microsoft"] = true
			p["sparkle"] = true
			p["adobe"] = true
			p["lookup"] = true
		case "none":
			if len(parts) != 1 {
				return nil, fmt.Errorf("--providers=none cannot be combined with other providers")
			}
		case "microsoft", "sparkle", "adobe", "lookup":
			p[strings.TrimSpace(part)] = true
		default:
			return nil, fmt.Errorf("unknown update provider %q (choose all, none, microsoft, sparkle, adobe, lookup)", part)
		}
	}
	return p, nil
}
