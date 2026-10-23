package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/niallrmurphy/macup/internal/inventory"
)

func JSON(w io.Writer, r inventory.Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

type row struct{ source, name, installed, available, status, detail string }

// Table presents all requested inventory sources in a single sortable view.
func Table(w io.Writer, r inventory.Result) error {
	rows := make([]row, 0, len(r.Applications))
	for _, a := range r.Applications {
		rows = append(rows, row{"app", a.Name, a.Version, "", "UNCHECKED", a.Path})
	}
	if r.Homebrew != nil {
		for _, p := range r.Homebrew.Packages {
			status := "NOT FLAGGED"
			if p.Outdated {
				status = "OUTDATED"
			}
			rows = append(rows, row{"brew-" + p.Kind, p.Name, p.Installed, p.Current, status, ""})
		}
	}
	if r.AppStore != nil {
		for _, p := range r.AppStore.Packages {
			status := "NOT FLAGGED"
			if p.Outdated {
				status = "OUTDATED"
			}
			rows = append(rows, row{"appstore", p.Name, p.Installed, p.AvailableVersion, status, "id:" + p.ID})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if strings.ToLower(rows[i].name) != strings.ToLower(rows[j].name) {
			return strings.ToLower(rows[i].name) < strings.ToLower(rows[j].name)
		}
		return rows[i].source < rows[j].source
	})
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "SOURCE\tNAME\tINSTALLED\tAVAILABLE\tSTATUS\tDETAIL"); err != nil {
		return err
	}
	for _, x := range rows {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", x.source, x.name, nonempty(x.installed), nonempty(x.available), x.status, nonempty(x.detail)); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "\n%d entries\n", len(rows)); err != nil {
		return err
	}
	for _, x := range r.Warnings {
		if _, err := fmt.Fprintf(w, "warning: %s: %s\n", x.Path, x.Message); err != nil {
			return err
		}
	}
	if r.Homebrew != nil {
		for _, x := range r.Homebrew.Warnings {
			if _, err := fmt.Fprintf(w, "warning (brew): %s\n", x); err != nil {
				return err
			}
		}
	}
	if r.AppStore != nil {
		for _, x := range r.AppStore.Warnings {
			if _, err := fmt.Fprintf(w, "warning (appstore): %s\n", x); err != nil {
				return err
			}
		}
	}
	return nil
}
func nonempty(v string) string {
	if v == "" {
		return "-"
	}
	return v
}
