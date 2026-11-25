package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
	"github.com/WildPino/ouroboros/internal/space"
	"github.com/WildPino/ouroboros/internal/state"
)

// newForgetJobCase adds two snapshots of the removed job "old" to the
// repository case; the configuration has only "docs".
func newForgetJobCase(t *testing.T) *repoCase {
	c := newRepoCase(t)
	if err := os.MkdirAll(filepath.Join(c.dir, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	for i, id := range []string{"0001aaaa", "0002bbbb"} {
		c.repo.snaps = append(c.repo.snaps, backend.Snapshot{ID: id, Time: at.Add(time.Duration(i) * time.Hour),
			Hostname: "host", Tags: []string{backend.TagOuroboros, backend.JobTag("old")}, AddedPacked: 3 << 20})
	}
	for i := range c.repo.snaps {
		c.repo.snaps[i].Hostname = "host"
	}
	c.repo.deletes = true
	return c
}

func TestForgetJobDryRun(t *testing.T) {
	c := newForgetJobCase(t)
	ctx := context.Background()
	if err := runForgetJob(ctx, c.opts, c.deps, "old", forgetJobOptions{dryRun: true}); err != nil {
		t.Fatal(err)
	}
	out := c.out.String()
	if !strings.Contains(out, "remove 2 snapshots and free about 6.0 MB") || !strings.Contains(out, "--confirm old") {
		t.Fatalf("%s", out)
	}
	if c.locked != 0 || slices.ContainsFunc(c.repo.calls, func(s string) bool { return s != "snapshots" }) {
		t.Fatalf("dry run changed something: calls %v, locked %d", c.repo.calls, c.locked)
	}

	c.out.Reset()
	c.opts.out = &jsonLines{w: c.out, command: "forget-job"}
	if err := runForgetJob(ctx, c.opts, c.deps, "photos", forgetJobOptions{dryRun: true}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Data forgetJobPlanOut `json:"data"`
	}
	decodeLast(t, c.out.String(), &got)
	if got.Data.Job != "photos" || len(got.Data.Snapshots) != 0 || len(got.Data.Pinned) != 1 {
		t.Fatalf("%+v", got.Data)
	}
}

func TestForgetJob(t *testing.T) {
	c := newForgetJobCase(t)
	ctx := context.Background()

	// Without the name repeated nothing happens.
	for _, confirm := range []string{"", "OLD", "docs"} {
		err := runForgetJob(ctx, c.opts, c.deps, "old", forgetJobOptions{confirm: confirm})
		if !errors.Is(err, errConfirmMismatch) || errorCode(err) != "not_confirmed" {
			t.Fatalf("confirm %q: %v", confirm, err)
		}
	}
	// A configured job is refused.
	err := runForgetJob(ctx, c.opts, c.deps, "docs", forgetJobOptions{confirm: "docs"})
	if !errors.Is(err, space.ErrJobConfigured) || errorCode(err) != "job_configured" {
		t.Fatalf("configured job: %v", err)
	}
	if len(c.repo.snaps) != 4 || slices.ContainsFunc(c.repo.calls, func(s string) bool { return s != "snapshots" }) {
		t.Fatalf("refusals changed something: %v", c.repo.calls)
	}

	c.out.Reset()
	c.opts.out = &jsonLines{w: c.out, command: "forget-job"}
	if err := runForgetJob(ctx, c.opts, c.deps, "old", forgetJobOptions{confirm: "old"}); err != nil {
		t.Fatalf("%v\n%s", err, c.errOut)
	}
	if !slices.Contains(c.repo.calls, "forget 0001aaaa,0002bbbb") || c.repo.calls[len(c.repo.calls)-1] != "prune" || c.locked == 0 {
		t.Fatalf("calls %v, locked %d", c.repo.calls, c.locked)
	}
	lines := decodeJSONLines(t, c.out.String())
	if len(lines) != 3 || lines[0]["event"] != "delete" || lines[1]["event"] != "delete" || lines[2]["type"] != "result" {
		t.Fatalf("lines %v", lines)
	}
	var got struct {
		Data forgetJobOut `json:"data"`
	}
	decodeLast(t, c.out.String(), &got)
	if got.Data.Forgotten != 2 || got.Data.PinnedKept != 0 {
		t.Fatalf("%+v", got.Data)
	}
	hist, _, err := state.ReadHistory(filepath.Join(c.dir, state.HistoryFile))
	if err != nil || len(hist) != 1 || hist[0].Kind != state.KindJobDeletion || hist[0].Job != "old" || hist[0].Forgotten != 2 {
		t.Fatalf("history %+v, %v", hist, err)
	}

	// Nothing left to delete.
	err = runForgetJob(ctx, c.opts, c.deps, "old", forgetJobOptions{confirm: "old"})
	if errorCode(err) != "no_snapshots" {
		t.Fatalf("second deletion: %v", err)
	}
}

// decodeLast decodes the last line of --json output into v.
func decodeLast(t *testing.T, out string, v any) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), v); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}
