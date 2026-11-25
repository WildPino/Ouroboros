package restic

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WildPino/ouroboros/internal/backend"
)

// TestIntegrationExcludeIgnoresCase excludes paths written with a different
// letter case than on disk, as NTFS treats them as the same path.
func TestIntegrationExcludeIgnoresCase(t *testing.T) {
	e := newIntegrationEnv(t)
	ctx := context.Background()
	b := e.backend(t, filepath.Join(e.dir, "repo"))
	if err := b.Init(ctx); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(e.dir, "Src")
	writeFile(t, filepath.Join(src, "Keep", "k.txt"), "keep")
	writeFile(t, filepath.Join(src, "SkipMe", "s.txt"), "listed")
	writeFile(t, filepath.Join(src, "app", "Node_Modules", "n.js"), "pattern")
	writeFile(t, filepath.Join(src, "Keep", "Big.ISO"), "from the file")
	excludeFile := filepath.Join(e.dir, "excludes.txt")
	if err := os.WriteFile(excludeFile, []byte("*.iso\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := b.Backup(ctx, backend.Job{
		Name:        "case",
		Paths:       []string{src},
		Exclude:     []string{strings.ToLower(filepath.Join(src, "skipme")), `**\node_modules`},
		ExcludeFile: excludeFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(e.dir, "out")
	if err := b.Restore(ctx, backend.RestoreOptions{SnapshotID: res.SnapshotID, Target: target}); err != nil {
		t.Fatal(err)
	}
	if findFileOrEmpty(t, target, "k.txt") == "" {
		t.Error("k.txt missing")
	}
	for _, name := range []string{"s.txt", "n.js", "Big.ISO"} {
		if findFileOrEmpty(t, target, name) != "" {
			t.Errorf("%s was not excluded", name)
		}
	}
}
