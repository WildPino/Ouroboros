package restic

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/WildPino/ouroboros/internal/backend"
)

// Integration tests run the real restic against repositories in temporary
// directories. They are skipped when restic is not installed or with -short.

type integrationEnv struct {
	restic    string
	dir       string
	pwCommand string
}

func newIntegrationEnv(t *testing.T) *integrationEnv {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	exe, err := exec.LookPath("restic")
	if err != nil {
		t.Skip("restic not installed")
	}
	dir := t.TempDir()
	// A copy of the test binary in a directory with a space checks that the
	// password command survives restic's argument splitting.
	helperDir := filepath.Join(dir, "password helper")
	if err := os.Mkdir(helperDir, 0o755); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(helperDir, "ouroboros.exe")
	copyFile(t, os.Args[0], helper)
	t.Setenv(helperEnv, "password")
	return &integrationEnv{restic: exe, dir: dir, pwCommand: PasswordCommand(helper)}
}

func (e *integrationEnv) backend(t *testing.T, repo string) *Restic {
	t.Helper()
	b, err := New(Options{
		Executable:      e.restic,
		Repository:      repo,
		PasswordCommand: e.pwCommand,
		Hostname:        "testhost",
		Compression:     "max",
		CacheDir:        filepath.Join(e.dir, "cache"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationLifecycle(t *testing.T) {
	env := newIntegrationEnv(t)
	ctx := context.Background()
	src := filepath.Join(env.dir, "src")
	writeFile(t, filepath.Join(src, "a.txt"), "version one")
	writeFile(t, filepath.Join(src, "sub", "b.txt"), "unchanged")
	writeFile(t, filepath.Join(src, "skip.tmp"), "excluded")

	b := env.backend(t, filepath.Join(env.dir, "repo"))
	if v, err := b.Version(ctx); err != nil || v == "" {
		t.Fatalf("Version = %q, %v", v, err)
	}
	if err := b.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := b.Init(ctx); err == nil {
		t.Fatal("second Init should fail")
	}

	job := backend.Job{Name: "docs", Paths: []string{src}, Exclude: []string{"*.tmp"}}
	first, err := b.Backup(ctx, job)
	if err != nil {
		t.Fatalf("first backup: %v", err)
	}
	if first.FilesNew != 2 || first.BytesAddedPacked == 0 || first.Incomplete {
		t.Fatalf("first backup result %+v", first)
	}

	writeFile(t, filepath.Join(src, "a.txt"), "version two, longer")
	second, err := b.Backup(ctx, job)
	if err != nil {
		t.Fatalf("second backup: %v", err)
	}
	if second.FilesChanged != 1 || second.FilesUnmodified != 1 {
		t.Fatalf("second backup result %+v", second)
	}

	filter := backend.SnapshotFilter{Host: "testhost", Tags: []string{backend.TagOuroboros}}
	snaps, err := b.Snapshots(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 || snaps[0].ID != first.SnapshotID || snaps[1].ID != second.SnapshotID {
		t.Fatalf("snapshots %+v", snaps)
	}
	if snaps[0].Job() != "docs" {
		t.Fatalf("job tag missing: %v", snaps[0].Tags)
	}
	if other, err := b.Snapshots(ctx, backend.SnapshotFilter{Tags: []string{backend.JobTag("other")}}); err != nil || len(other) != 0 {
		t.Fatalf("filter by other job: %v, %v", other, err)
	}

	// Pin the first snapshot; restic gives it a new ID.
	if err := b.Tag(ctx, first.SnapshotID, []string{backend.TagPinned}, nil); err != nil {
		t.Fatalf("Tag: %v", err)
	}
	snaps, err = b.Snapshots(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	var pinned, latest backend.Snapshot
	for _, s := range snaps {
		if s.Pinned() {
			pinned = s
		} else {
			latest = s
		}
	}
	if pinned.ID == "" || latest.ID != second.SnapshotID {
		t.Fatalf("after tagging: %+v", snaps)
	}

	if err := b.Forget(ctx, []string{latest.ID}); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if _, err := b.Prune(ctx, backend.PruneOptions{MaxUnused: "0"}); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if err := b.Check(ctx, backend.CheckOptions{ReadDataSubset: "100%"}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	snaps, err = b.Snapshots(ctx, filter)
	if err != nil || len(snaps) != 1 || snaps[0].ID != pinned.ID {
		t.Fatalf("after forget: %+v, %v", snaps, err)
	}

	target := filepath.Join(env.dir, "restore")
	if err := b.Restore(ctx, backend.RestoreOptions{SnapshotID: pinned.ID, Target: target}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	restored := findFile(t, target, "a.txt")
	if got, _ := os.ReadFile(restored); string(got) != "version one" {
		t.Fatalf("restored a.txt = %q", got)
	}
	if findFileOrEmpty(t, target, "skip.tmp") != "" {
		t.Fatal("excluded file was backed up")
	}
}

func TestIntegrationErrors(t *testing.T) {
	env := newIntegrationEnv(t)
	ctx := context.Background()

	missing := env.backend(t, filepath.Join(env.dir, "missing"))
	if _, err := missing.Snapshots(ctx, backend.SnapshotFilter{}); !errors.Is(err, ErrRepoNotFound) {
		t.Fatalf("missing repo: %v", err)
	}

	repo := filepath.Join(env.dir, "repo")
	if err := env.backend(t, repo).Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Setenv(helperEnv, "wrong-password")
	if _, err := env.backend(t, repo).Snapshots(ctx, backend.SnapshotFilter{}); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong password: %v", err)
	}
}

func findFile(t *testing.T, root, name string) string {
	t.Helper()
	p := findFileOrEmpty(t, root, name)
	if p == "" {
		t.Fatalf("%s not found under %s", name, root)
	}
	return p
}

func findFileOrEmpty(t *testing.T, root, name string) string {
	t.Helper()
	var found string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == name {
			found = p
			return fs.SkipAll
		}
		return nil
	})
	return found
}
