package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/example/macup/internal/diagnostics"
	"github.com/example/macup/internal/inventory"
	"github.com/example/macup/internal/output"
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
		rootsFlag := fs.String("roots", "", "comma-separated directories to scan (defaults to standard macOS app directories)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if fs.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "unexpected arguments:", fs.Args())
			return 2
		}
		roots := selectedRoots(*rootsFlag)
		result := inventory.Scan(context.Background(), roots)
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
	fmt.Println(`macup — read-only macOS application inventory (Milestone 1)

Usage:
  macup scan [--json] [--roots /Applications,/Users/me/Applications]
  macup doctor [--roots /Applications]
  macup help

No applications are updated or modified.`)
}
