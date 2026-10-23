package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/niallrmurphy/macup/internal/diagnostics"
	"github.com/niallrmurphy/macup/internal/inventory"
	"github.com/niallrmurphy/macup/internal/output"
	"os"
	"strings"
	"time"
)

func main() { os.Exit(run(os.Args[1:])) }
func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 0
	}
	switch args[0] {
	case "scan":
		fs := flag.NewFlagSet("scan", flag.ContinueOnError)
		jsonOut := fs.Bool("json", false, "print machine-readable JSON")
		appsFlag := fs.Bool("apps", false, "scan installed macOS .app bundles")
		includeSystem := fs.Bool("include-system", false, "include macOS-managed system application bundles")
		brewFlag := fs.Bool("brew", false, "scan installed Homebrew formulae and casks")
		sparkleFlag := fs.Bool("sparkle", false, "check discovered apps with Sparkle appcasts (network access)")
		quietFlag := fs.Bool("quiet", false, "suppress scan progress messages on stderr")
		debugFlag := fs.Bool("sparkle-debug", false, "print Sparkle per-feed diagnostics to stderr (implies --sparkle)")
		debugXMLDir := fs.String("sparkle-debug-xml", "", "directory for sanitized appcast XML structure samples (implies --sparkle-debug)")
		appStoreFlag := fs.Bool("appstore", false, "scan Mac App Store software via mas")
		providersFlag := fs.String("providers", "all", "update-check providers: all, none, microsoft, sparkle (comma-separated)")
		rootsFlag := fs.String("roots", "", "comma-separated directories to scan (defaults to standard macOS app directories)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if fs.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "unexpected arguments:", fs.Args())
			return 2
		}
		// Inventory sources and update-check providers are independent selections.
		providerSet, err := parseProviders(*providersFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if *sparkleFlag || *debugFlag || *debugXMLDir != "" {
			providerSet["sparkle"] = true
		}
		explicitSource := *appsFlag || *brewFlag || *appStoreFlag || strings.TrimSpace(*rootsFlag) != ""
		useApps := !explicitSource || *appsFlag || strings.TrimSpace(*rootsFlag) != ""
		useBrew := !explicitSource || *brewFlag
		useAppStore := !explicitSource || *appStoreFlag
		useMicrosoft := providerSet["microsoft"] && useApps
		useSparkle := providerSet["sparkle"] && useApps

		result := inventory.Result{Applications: []inventory.Application{}, Warnings: []inventory.Warning{}, Roots: []string{}}
		if useApps {
			result = inventory.Scan(context.Background(), selectedRoots(*rootsFlag))
			if !*includeSystem {
				result.Applications = inventory.FilterSystemApplications(result.Applications)
			}
		}
		if useBrew {
			b := inventory.ScanHomebrew(context.Background())
			result.Homebrew = &b
		}
		if useAppStore {
			a := inventory.ScanAppStore(context.Background())
			result.AppStore = &a
			if useApps && a.Available {
				inventory.AnnotateAppStoreIDs(context.Background(), &result)
			}
		}
		inventory.Reconcile(&result)
		if useMicrosoft {
			m := inventory.ScanMicrosoft(context.Background())
			result.Microsoft = &m
			inventory.ApplyMicrosoft(&result)
		}
		if useSparkle {
			var progress inventory.SparkleProgressFunc
			if !*quietFlag || *debugFlag || *debugXMLDir != "" {
				fmt.Fprintf(os.Stderr, "Sparkle: examining %d applications for update feeds\n", len(result.Applications))
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
						if *debugFlag || *debugXMLDir != "" {
							printSparkleDiagnostics(os.Stderr, e.Check)
						}
					}
				}
			}
			var s inventory.SparkleResult
			if *debugXMLDir != "" {
				s = inventory.CheckSparkleWithXMLSamples(context.Background(), result.Applications, nil, progress, *debugXMLDir)
			} else if *debugFlag {
				s = inventory.CheckSparkleWithDiagnostics(context.Background(), result.Applications, nil, progress)
			} else {
				s = inventory.CheckSparkleWithProgress(context.Background(), result.Applications, nil, progress)
			}
			result.Sparkle = &s
			inventory.ApplySparkle(&result)
			if !*quietFlag || *debugFlag || *debugXMLDir != "" {
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
				if *debugFlag {
					fmt.Fprintln(os.Stderr, "Sparkle: every discovered feed is included above, including pre-fetch skips")
				}
			}
		}
		if *jsonOut {
			err = output.JSON(os.Stdout, result)
		} else {
			err = output.Table(os.Stdout, result)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
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
	case "help", "-h", "--help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		usage()
		return 2
	}
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
  macup scan [--json] [--apps] [--include-system] [--brew] [--appstore] [--providers all|none|microsoft,sparkle] [--sparkle] [--sparkle-debug] [--sparkle-debug-xml DIR] [--quiet] [--roots /Applications,/Users/me/Applications]
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
		case "none":
			if len(parts) != 1 {
				return nil, fmt.Errorf("--providers=none cannot be combined with other providers")
			}
		case "microsoft", "sparkle":
			p[strings.TrimSpace(part)] = true
		default:
			return nil, fmt.Errorf("unknown update provider %q (choose all, none, microsoft, sparkle)", part)
		}
	}
	return p, nil
}
