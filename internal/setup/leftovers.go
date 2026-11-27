package setup

import (
	"os"
	"path/filepath"
	"regexp"
)

// leftoverRE matches the names that replacing an installed file leaves
// behind (DESIGN.md §12.8, question 4): NAME.new while it is written,
// NAME.old for the file it replaced, and NAME.<hex>.old when an older
// NAME.old could not be deleted yet. Only the installed names qualify, so
// cleaning a folder never touches a file Ouroboros did not write.
var leftoverRE = regexp.MustCompile(`(?i)^(ouroboros\.exe|restic\.exe|license)(\.[0-9a-f]{8})?\.(old|new)$`)

// cleanLeftovers deletes leftover files in dir, including NAME.new when
// withNew is set, and returns the paths it could not delete.
func cleanLeftovers(dir string, withNew bool) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var stuck []string
	for _, e := range entries {
		m := leftoverRE.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		if m[3] == "new" && !withNew {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			stuck = append(stuck, p)
		}
	}
	return stuck
}
