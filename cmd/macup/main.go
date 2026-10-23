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
		brewFlag := fs.Bool("brew", false, "scan installed Homebrew formulae and casks")
		appStoreFlag := fs.Bool("appstore", false, "scan Mac App Store software via mas")
		rootsFlag := fs.String("roots", "", "comma-separated directories to scan (defaults to standard macOS app directories)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if fs.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "unexpected arguments:", fs.Args())
			return 2
		}
		// An explicit source flag selects only named providers. Without source
		// flags, scan all sources. Custom roots imply --apps to avoid running
		// unrelated package managers when querying a specific directory.
		explicit := *appsFlag || *brewFlag || *appStoreFlag || strings.TrimSpace(*rootsFlag) != ""
		useApps := !explicit || *appsFlag || strings.TrimSpace(*rootsFlag) != ""
		useBrew := !explicit || *brewFlag
		useAppStore := !explicit || *appStoreFlag
		result := inventory.Result{Applications: []inventory.Application{}, Warnings: []inventory.Warning{}, Roots: []string{}}
		if useApps {
			result = inventory.Scan(context.Background(), selectedRoots(*rootsFlag))
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
		var err error
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
  macup scan [--json] [--apps] [--brew] [--appstore] [--roots /Applications,/Users/me/Applications]
  macup doctor [--roots /Applications]
  macup help

With no source flags, all three inventory sources are scanned.
Source flags select only the named sources; --roots implies --apps.
No applications are updated or modified.`)
}
