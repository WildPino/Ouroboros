package restic

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
)

// writeRandomTree writes n incompressible files of size bytes each, so
// that backup and prune take long enough to be interrupted.
func writeRandomTree(t *testing.T, dir string, n, size int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, size)
	for i := range n {
		rand.Read(buf)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d.bin", i)), buf, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestIntegrationInterrupted kills restic in the middle of backups and
// prunes, as a crash or power loss would, and checks that the repository
// stays consistent and usable.
func TestIntegrationInterrupted(t *testing.T) {
	e := newIntegrationEnv(t)
	ctx := context.Background()
	repo := filepath.Join(e.dir, "repo")
	b := e.backend(t, repo)
	if err := b.Init(ctx); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(e.dir, "src")
	job := backend.Job{Name: "data", Paths: []string{src}}

	mustCheck := func(when string) {
		t.Helper()
		if err := b.Check(ctx, backend.CheckOptions{ReadDataSubset: "100%"}); err != nil {
			t.Fatalf("check after %s: %v", when, err)
		}
	}

	for i, delay := range []time.Duration{200 * time.Millisecond, 700 * time.Millisecond, 1500 * time.Millisecond} {
		writeRandomTree(t, filepath.Join(src, fmt.Sprint(i)), 40, 2<<20)
		cctx, cancel := context.WithTimeout(ctx, delay)
		_, err := b.Backup(cctx, job)
		cancel()
		if err == nil {
			t.Logf("backup finished before the %s interrupt", delay)
		} else if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("interrupted backup: %v", err)
		}
		mustCheck(fmt.Sprintf("backup killed after %s", delay))
		// The lock of the killed process must not block the next run.
		if _, err := b.Backup(ctx, job); err != nil {
			t.Fatalf("backup after interrupt: %v", err)
		}
	}

	snaps, err := b.Snapshots(ctx, backend.SnapshotFilter{})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(snaps)-1)
	for _, s := range snaps[:len(snaps)-1] {
		ids = append(ids, s.ID)
	}
	if err := os.RemoveAll(filepath.Join(src, "0")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Backup(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := b.Forget(ctx, append(ids, snaps[len(snaps)-1].ID)); err != nil {
		t.Fatal(err)
	}
	for _, delay := range []time.Duration{300 * time.Millisecond, 1 * time.Second} {
		cctx, cancel := context.WithTimeout(ctx, delay)
		_, err := b.Prune(cctx, backend.PruneOptions{MaxUnused: "0"})
		cancel()
		if err == nil {
			t.Logf("prune finished before the %s interrupt", delay)
		} else if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("interrupted prune: %v", err)
		}
		mustCheck(fmt.Sprintf("prune killed after %s", delay))
	}
	if _, err := b.Prune(ctx, backend.PruneOptions{MaxUnused: "0"}); err != nil {
		t.Fatalf("prune after interrupts: %v", err)
	}
	mustCheck("final prune")
	if snaps, err := b.Snapshots(ctx, backend.SnapshotFilter{}); err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots: %d, %v", len(snaps), err)
	}
}
