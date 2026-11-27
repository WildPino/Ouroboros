// Command ouroboros-app builds Ouroboros.exe, the windowed program that
// installs, shows and uninstalls Ouroboros (DESIGN.md §12). It is linked
// with -H windowsgui and carries the command line and restic inside it.
package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/WildPino/ouroboros/internal/payload"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

// embeddedFlag is the hidden argument that prints the embedded files.
const embeddedFlag = "--embedded-versions"

// writeEmbedded checks every embedded file against the manifest and
// prints one line for each. It reports whether all of them passed.
func writeEmbedded(w io.Writer, p *payload.Payload, loadErr error) bool {
	fmt.Fprintf(w, "Ouroboros %s\n", version)
	if loadErr != nil {
		fmt.Fprintf(w, "error: %v\n", loadErr)
		return false
	}
	ok := true
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, f := range p.Files {
		v := f.Version
		if v == "" {
			v = "-"
		}
		status := "ok"
		if err := p.Verify(f); err != nil {
			status = "FAILED: " + err.Error()
			ok = false
		}
		fmt.Fprintf(tw, "%s\t%s\t%d bytes\tsha256 %s\t%s\n", f.Path, v, f.Size, f.SHA256, status)
	}
	tw.Flush()
	return ok
}
