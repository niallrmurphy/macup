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

type row struct{ source, name, installed, available, status, updateSource, detail string }

// TableOptions controls which entries the table shows.
type TableOptions struct {
	// ShowAll includes entries already known to be current. By default the
	// table lists only entries that may need attention.
	ShowAll bool
	// Statuses, when non-empty, restricts the table to these statuses
	// (case-insensitive) and takes precedence over ShowAll.
	Statuses []string
}

func (o TableOptions) keep(status string) bool {
	if len(o.Statuses) > 0 {
		for _, want := range o.Statuses {
			if strings.EqualFold(strings.TrimSpace(want), status) {
				return true
			}
		}
		return false
	}
	return o.ShowAll || !currentStatus(status)
}

// Table presents every entry from all requested inventory sources.
func Table(w io.Writer, r inventory.Result) error {
	return TableWith(w, r, TableOptions{ShowAll: true})
}

// currentStatus is a provider's positive statement that no update applies.
func currentStatus(status string) bool {
	return status == "CURRENT" || status == "NOT FLAGGED"
}

// TableWith presents inventory sources in a single sortable view, hiding
// known-current entries unless asked for everything.
func TableWith(w io.Writer, r inventory.Result, opts TableOptions) error {
	if r.Entries == nil {
		inventory.Reconcile(&r)
	}
	rows := make([]row, 0, len(r.Entries))
	hidden := 0
	for _, e := range r.Entries {
		if !opts.keep(e.Status) {
			hidden++
			continue
		}
		detail := e.Path
		if detail == "" {
			detail = e.Detail
		}
		if e.Package != "" && e.Path != "" {
			detail += " (cask:" + e.Package + ")"
		}
		rows = append(rows, row{strings.Join(e.Sources, "+"), e.Name, e.Installed, e.Available, e.Status, e.UpdateSource, detail})
	}
	sort.Slice(rows, func(i, j int) bool {
		if strings.ToLower(rows[i].name) != strings.ToLower(rows[j].name) {
			return strings.ToLower(rows[i].name) < strings.ToLower(rows[j].name)
		}
		return rows[i].source < rows[j].source
	})
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "SOURCE\tNAME\tINSTALLED\tAVAILABLE\tSTATUS\tUPDATE SOURCE\tDETAIL"); err != nil {
		return err
	}
	for _, x := range rows {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", x.source, x.name, nonempty(x.installed), availableLabel(x.available, x.status), x.status, nonempty(x.updateSource), nonempty(x.detail)); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(opts.Statuses) > 0 {
		if _, err := fmt.Fprintf(w, "\n%d entries shown with status %s; %d hidden\n", len(rows), strings.ToUpper(strings.Join(opts.Statuses, ",")), hidden); err != nil {
			return err
		}
	} else if hidden > 0 {
		if _, err := fmt.Fprintf(w, "\n%d entries shown; %d current entries hidden (use --all to show)\n", len(rows), hidden); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintf(w, "\n%d entries\n", len(rows)); err != nil {
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
	if r.Microsoft != nil {
		if r.Microsoft.CheckStatus == "checked" {
			if _, err := fmt.Fprintf(w, "provider (microsoft): checked; %d updates reported (%s response); core Office apps marked CURRENT when no update is offered\n", len(r.Microsoft.Updates), r.Microsoft.ResponseFormat); err != nil {
				return err
			}
		}
		for _, x := range r.Microsoft.Warnings {
			if _, err := fmt.Fprintf(w, "warning (microsoft): %s\n", x); err != nil {
				return err
			}
		}
	}
	if r.Adobe != nil {
		if r.Adobe.CheckStatus == "checked" {
			if _, err := fmt.Fprintf(w, "provider (adobe): checked; %d catalogue releases; %d installed package records\n", len(r.Adobe.Products), len(r.Adobe.Installed)); err != nil {
				return err
			}
		}
		for _, x := range r.Adobe.Warnings {
			if _, err := fmt.Fprintf(w, "warning (adobe): %s\n", x); err != nil {
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

// The available-version cell describes whether a newer applicable version is
// known, not whether a provider happened to populate a string field.
func availableLabel(version, status string) string {
	if version != "" {
		return version
	}
	switch status {
	case "CURRENT", "NOT FLAGGED":
		return "—" // checked: no applicable update reported
	default:
		return "?" // no reliable available-version information
	}
}
