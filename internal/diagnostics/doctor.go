package diagnostics

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
)

func Run(w io.Writer, roots []string) error {
	if _, err := fmt.Fprintf(w, "macup doctor\nOS: %s/%s\n", runtime.GOOS, runtime.GOARCH); err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		fmt.Fprintln(w, "note: macup targets macOS; fixture scans can run on other systems")
	}
	for _, tool := range []string{"plutil", "brew", "mas"} {
		path, err := exec.LookPath(tool)
		if err != nil {
			fmt.Fprintf(w, "%s: not found (optional except plutil for binary plists)\n", tool)
		} else {
			fmt.Fprintf(w, "%s: %s\n", tool, path)
		}
	}
	for _, root := range roots {
		info, err := os.Stat(root)
		switch {
		case os.IsNotExist(err):
			fmt.Fprintf(w, "root %s: missing\n", root)
		case err != nil:
			fmt.Fprintf(w, "root %s: %v\n", root, err)
		case !info.IsDir():
			fmt.Fprintf(w, "root %s: not a directory\n", root)
		default:
			fmt.Fprintf(w, "root %s: OK\n", root)
		}
	}
	fmt.Fprintln(w, "read-only: no applications were modified")
	return nil
}
