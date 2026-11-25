//go:build windows

package restic

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/WildPino/ouroboros/internal/backend"
	platform "github.com/WildPino/ouroboros/internal/platform/windows"
)

// adminTestsEnv enables tests that need an elevated console and change
// system state, such as creating Volume Shadow Copies. Run them with
// scripts/admin-tests.ps1.
const adminTestsEnv = "OUROBOROS_ADMIN_TESTS"

func requireAdminTests(t *testing.T) {
	t.Helper()
	if os.Getenv(adminTestsEnv) != "1" {
		t.Skip("set " + adminTestsEnv + "=1 in an elevated console to run")
	}
	if ok, err := platform.IsElevated(); err != nil || !ok {
		t.Fatalf("%s=1 requires an elevated console (elevated %v, %v)", adminTestsEnv, ok, err)
	}
}

// lockExclusively opens path with no sharing, as applications such as
// Outlook do with their data files: no other process can read it until
// the handle is closed.
func lockExclusively(t *testing.T, path string) {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("lock %s: %v", path, err)
	}
	t.Cleanup(func() { windows.CloseHandle(h) })
	if _, err := os.ReadFile(path); err == nil {
		t.Fatal("the locked file is still readable")
	}
}

// TestAdminLockedFileVSS backs up a file held open without sharing: without
// a shadow copy restic cannot read it and the snapshot is incomplete; with
// one the file is read from the snapshot of the volume.
func TestAdminLockedFileVSS(t *testing.T) {
	requireAdminTests(t)
	e := newIntegrationEnv(t)
	ctx := context.Background()
	b := e.backend(t, filepath.Join(e.dir, "repo"))
	if err := b.Init(ctx); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(e.dir, "src")
	content := bytes.Repeat([]byte("locked mailbox data\n"), 5000)
	writeFile(t, filepath.Join(src, "free.txt"), "readable")
	if err := os.WriteFile(filepath.Join(src, "mailbox.pst"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	lockExclusively(t, filepath.Join(src, "mailbox.pst"))

	noVSS, err := b.Backup(ctx, backend.Job{Name: "novss", Paths: []string{src}, VSS: false})
	if err != nil {
		t.Fatalf("backup without VSS: %v", err)
	}
	if !noVSS.Incomplete || !strings.Contains(strings.Join(noVSS.Warnings, "\n"), "mailbox.pst") {
		t.Fatalf("without VSS: incomplete %v, warnings %v", noVSS.Incomplete, noVSS.Warnings)
	}
	t.Logf("without VSS: incomplete as expected: %v", noVSS.Warnings)

	withVSS, err := b.Backup(ctx, backend.Job{Name: "vss", Paths: []string{src}, VSS: true})
	if err != nil {
		t.Fatalf("backup with VSS: %v", err)
	}
	if withVSS.Incomplete {
		t.Fatalf("with VSS: incomplete: %v", withVSS.Warnings)
	}
	target := filepath.Join(e.dir, "restored")
	if err := b.Restore(ctx, backend.RestoreOptions{SnapshotID: withVSS.SnapshotID, Target: target}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(findFile(t, target, "mailbox.pst"))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("restored locked file: %d bytes, %v", len(got), err)
	}
	t.Logf("with VSS: locked file backed up and restored (%d bytes)", len(got))
}
