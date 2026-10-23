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
	if r.Entries == nil {
		inventory.Reconcile(&r)
	}
	rows := make([]row, 0, len(r.Entries))
	for _, e := range r.Entries {
		detail := e.Path
		if detail == "" {
			detail = e.Detail
		}
		if e.Package != "" && e.Path != "" {
			detail += " (cask:" + e.Package + ")"
		}
		rows = append(rows, row{strings.Join(e.Sources, "+"), e.Name, e.Installed, e.Available, e.Status, detail})
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
