package restic

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WildPino/ouroboros/internal/backend"
)

// unicodeNames covers accented Latin, Greek, Cyrillic, CJK and characters
// outside the Basic Multilingual Plane (UTF-16 surrogate pairs).
var unicodeNames = []string{"perché città", "Ελληνικά", "Привет мир", "日本語のフォルダ", "math 𝔸𝔹ℂ"}

// TestIntegrationLongAndUnicodePaths backs up and restores files whose
// full path exceeds the 260-character MAX_PATH limit and whose names are
// not ASCII.
func TestIntegrationLongAndUnicodePaths(t *testing.T) {
	e := newIntegrationEnv(t)
	ctx := context.Background()
	b := e.backend(t, filepath.Join(e.dir, "repo"))
	if err := b.Init(ctx); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(e.dir, "sorgente "+unicodeNames[0])
	deep := src
	for i := 0; len(deep) < 320; i++ {
		deep = filepath.Join(deep, unicodeNames[i%len(unicodeNames)]+" "+strings.Repeat("x", 20))
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for i, n := range unicodeNames {
		p := filepath.Join(deep, n+".txt")
		content := bytes.Repeat([]byte(n+"\n"), 100+i)
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
		files[n+".txt"] = content
	}
	// An excluded Unicode name must stay out of the snapshot.
	if err := os.WriteFile(filepath.Join(src, "escluso è.tmp"), []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	if len(filepath.Join(deep, unicodeNames[0]+".txt")) <= 260 {
		t.Fatal("test tree is not deep enough")
	}

	res, err := b.Backup(ctx, backend.Job{Name: "unicode", Paths: []string{src}, Exclude: []string{"escluso è.tmp"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Incomplete {
		t.Fatalf("backup incomplete: %v", res.Warnings)
	}
	snaps, err := b.Snapshots(ctx, backend.SnapshotFilter{Tags: []string{backend.JobTag("unicode")}})
	if err != nil || len(snaps) != 1 || len(snaps[0].Paths) != 1 || !strings.EqualFold(snaps[0].Paths[0], src) {
		t.Fatalf("snapshots %+v, %v", snaps, err)
	}

	target := filepath.Join(e.dir, "ripristino ü")
	if _, err := b.Restore(ctx, backend.RestoreOptions{SnapshotID: res.SnapshotID, Target: target}); err != nil {
		t.Fatal(err)
	}
	found := map[string][]byte{}
	excluded := false
	err = filepath.WalkDir(target, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == "escluso è.tmp" {
			excluded = true
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".txt") {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			found[d.Name()] = data
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if excluded {
		t.Error("excluded file was restored")
	}
	for name, want := range files {
		if !bytes.Equal(found[name], want) {
			t.Errorf("%s: restored %d bytes, want %d", name, len(found[name]), len(want))
		}
	}
}
