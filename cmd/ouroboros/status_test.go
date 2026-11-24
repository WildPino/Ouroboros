package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
	"github.com/WildPino/ouroboros/internal/state"
)

func newStatusCase(t *testing.T) (*repoCase, statusDeps, time.Time) {
	c := newRepoCase(t)
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.Local)
	c.repo.snaps = []backend.Snapshot{
		{ID: "aaaa1111", Time: now.AddDate(0, 0, -30), Tags: []string{backend.TagOuroboros, backend.JobTag("docs")}},
		{ID: "bbbb2222", Time: now.AddDate(0, 0, -1), Tags: []string{backend.TagOuroboros, backend.JobTag("docs")}},
	}
	d := statusDeps{
		repo:       c.deps,
		disk:       fakeDisk{total: 100 << 30, free: 40 << 30},
		repoSize:   func(context.Context, string) (uint64, error) { return 55 << 30, nil },
		taskExists: func(context.Context, string) (bool, error) { return true, nil },
		now:        func() time.Time { return now },
	}
	return c, d, now
}

func TestStatus(t *testing.T) {
	c, d, now := newStatusCase(t)
	st := state.State{Jobs: map[string]state.JobState{
		"docs": {LastAttempt: now.Add(-time.Hour), LastSuccess: now.Add(-2 * time.Hour), LastError: "backup: source missing"},
	}, LastPrune: now.AddDate(0, 0, -3)}
	if err := state.Save(filepath.Join(c.dir, state.StateFile), st); err != nil {
		t.Fatal(err)
	}
	state.Append(filepath.Join(c.dir, state.HistoryFile), state.Record{Kind: state.KindBackup, Job: "docs", OK: true, BytesAddedPacked: 100 << 20})

	if err := runStatus(context.Background(), c.opts, d); err != nil {
		t.Fatalf("%v\n%s", err, c.errOut)
	}
	out := c.out.String()
	for _, want := range []string{
		"Partition:       40.0 GB of 100.0 GB free (target at least 12.0 GB: min_free 10.0 GB + prune headroom 2.0 GB)",
		"Repository:      " + filepath.Join(c.dir, "repo") + ", 55.0 GB",
		"History:         2 snapshots, oldest " + formatTime(now.AddDate(0, 0, -30)) + " (30 days)",
		"Scheduled task:  installed, every 15m\n",
		"last prune " + formatTime(now.AddDate(0, 0, -3)) + ", last check never",
		"FAILED " + formatTime(now.Add(-time.Hour)),
		"150.0 MB", // 100 MB × growth_factor 1.5
		"docs: backup: source missing",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// docs failed an hour ago: the retry (30m) is due.
	if !strings.Contains(out, "due now") {
		t.Errorf("retry not shown as due:\n%s", out)
	}
}

func TestStatusWithoutElevationIsPartial(t *testing.T) {
	c, d, _ := newStatusCase(t)
	d.repo.isElevated = func() (bool, error) { return false, nil }
	d.taskExists = func(context.Context, string) (bool, error) { return false, nil }
	d.repoSize = func(context.Context, string) (uint64, error) { return 0, errors.New("access denied") }
	err := runStatus(context.Background(), c.opts, d)
	if exitCode(err) != exitWarnings {
		t.Fatalf("got %v", err)
	}
	out := c.out.String()
	if strings.Contains(out, "History:") || !strings.Contains(out, "not installed") || !strings.Contains(out, "after first backup") {
		t.Errorf("output:\n%s", out)
	}
	if !strings.Contains(c.errOut.String(), "only shown to administrators") || !strings.Contains(c.errOut.String(), "access denied") {
		t.Errorf("stderr %q", c.errOut)
	}
}

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		15 * time.Minute: "15m", time.Hour: "1h", 90 * time.Minute: "1h30m", 90 * time.Second: "1m30s",
	} {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%s) = %q, want %q", d, got, want)
		}
	}
}
