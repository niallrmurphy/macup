package output

import (
	"encoding/json"
	"fmt"
	"github.com/example/macup/internal/inventory"
	"io"
	"text/tabwriter"
)

func JSON(w io.Writer, r inventory.Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
func Table(w io.Writer, r inventory.Result) error {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	if _, err := fmt.Fprintln(tw, "APPLICATION\tVERSION\tBUILD\tBUNDLE ID\tPATH"); err != nil {
		return err
	}
	for _, a := range r.Applications {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", a.Name, nonempty(a.Version), nonempty(a.Build), nonempty(a.BundleID), a.Path); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "\n%d applications, %d warnings\n", len(r.Applications), len(r.Warnings)); err != nil {
		return err
	}
	for _, warning := range r.Warnings {
		if _, err := fmt.Fprintf(w, "warning: %s: %s\n", warning.Path, warning.Message); err != nil {
			return err
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
