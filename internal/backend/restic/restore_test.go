package restic

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WildPino/ouroboros/internal/backend"
)

func TestIncludePattern(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Users\me\Documents`:  "/C/Users/me/Documents",
		`c:\users\me`:            "/C/users/me",
		`D:/Projects/x`:          "/D/Projects/x",
		`E:\`:                    "/E/",
		`E:`:                     "/E",
		`**\node_modules`:        "**/node_modules",
		`Documents\taxes`:        "Documents/taxes",
		`*.txt`:                  "*.txt",
		`/C/Users/me`:            "/C/Users/me",
		`C:relative`:             "C:relative",
		`\\server\share\folder`:  "//server/share/folder",
		`ünï:\not\a\drive\below`: "ünï:/not/a/drive/below",
	} {
		if got := includePattern(in); got != want {
			t.Errorf("includePattern(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestIntegrationRestoreInclude restores parts of a snapshot selected by
// Windows paths written as a user would: drive letter, backslashes, any
// letter case.
func TestIntegrationRestoreInclude(t *testing.T) {
	e := newIntegrationEnv(t)
	ctx := context.Background()
	b := e.backend(t, filepath.Join(e.dir, "repo"))
	if err := b.Init(ctx); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(e.dir, "src")
	writeFile(t, filepath.Join(src, "keep", "sub", "a.txt"), "keep me")
	writeFile(t, filepath.Join(src, "other", "b.txt"), "not me")
	res, err := b.Backup(ctx, backend.Job{Name: "inc", Paths: []string{src}})
	if err != nil {
		t.Fatal(err)
	}

	for i, inc := range []string{
		filepath.Join(src, "keep"),
		strings.ToUpper(filepath.Join(src, "keep")),
		filepath.ToSlash(filepath.Join(src, "keep", "sub", "a.txt")),
		`keep\sub`,
	} {
		target := filepath.Join(e.dir, "out", string(rune('a'+i)))
		if err := b.Restore(ctx, backend.RestoreOptions{SnapshotID: res.SnapshotID, Target: target, Include: []string{inc}}); err != nil {
			t.Fatalf("include %q: %v", inc, err)
		}
		if findFileOrEmpty(t, target, "a.txt") == "" || findFileOrEmpty(t, target, "b.txt") != "" {
			t.Errorf("include %q restored the wrong files", inc)
		}
	}

	target := filepath.Join(e.dir, "out", "none")
	err = b.Restore(ctx, backend.RestoreOptions{SnapshotID: res.SnapshotID, Target: target, Include: []string{filepath.Join(src, "missing")}})
	if !errors.Is(err, ErrNothingMatched) {
		t.Fatalf("include of a missing path: %v", err)
	}
	if entries, _ := os.ReadDir(target); len(entries) > 0 {
		t.Errorf("files restored for a missing path: %v", entries)
	}
}
