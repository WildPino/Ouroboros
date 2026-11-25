package main

import (
	"context"
	"encoding/json"
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
		lockHeld:   func() (bool, error) { return false, nil },
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

	if err := runStatus(context.Background(), c.opts, statusOptions{}, d); err != nil {
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
	err := runStatus(context.Background(), c.opts, statusOptions{}, d)
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

// decodeJSONLines splits --json output into its lines and checks the
// protocol: events first, then exactly one final line.
func decodeJSONLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line %q: %v", l, err)
		}
		lines = append(lines, m)
	}
	for i, m := range lines {
		final := m["type"] == "result" || m["type"] == "error"
		if final != (i == len(lines)-1) {
			t.Fatalf("line %d has type %v:\n%s", i, m["type"], out)
		}
	}
	return lines
}

func TestStatusJSON(t *testing.T) {
	c, d, now := newStatusCase(t)
	st := state.State{Jobs: map[string]state.JobState{
		"docs": {LastAttempt: now.Add(-time.Hour), LastSuccess: now.Add(-2 * time.Hour), LastError: "backup: source missing"},
	}}
	if err := state.Save(filepath.Join(c.dir, state.StateFile), st); err != nil {
		t.Fatal(err)
	}
	hist := filepath.Join(c.dir, state.HistoryFile)
	state.Append(hist, state.Record{Time: now.Add(-3 * time.Hour), Kind: state.KindBackup, Job: "docs", OK: true, Warnings: 2})
	state.Append(hist, state.Record{Time: now.Add(-2 * time.Hour), Kind: state.KindBackup, Job: "docs", OK: true})
	state.Append(hist, state.Record{Time: now.Add(-time.Hour), Kind: state.KindBackup, Job: "docs", Error: "source missing"})
	d.lockHeld = func() (bool, error) { return true, nil }
	c.opts.out = &jsonLines{w: c.out, command: "status"}

	if err := runStatus(context.Background(), c.opts, statusOptions{}, d); err != nil {
		t.Fatalf("%v\n%s", err, c.errOut)
	}
	lines := decodeJSONLines(t, c.out.String())
	var got struct {
		Data statusOut `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(c.out.String())), &got); err != nil {
		t.Fatal(err)
	}
	s := got.Data
	if lines[0]["command"] != "status" || s.Running == nil || !*s.Running {
		t.Errorf("command %v running %v", lines[0]["command"], s.Running)
	}
	if s.Partition == nil || s.Partition.FreeBytes != 40<<30 || s.Partition.RequiredFreeBytes != 12<<30 {
		t.Errorf("partition %+v", s.Partition)
	}
	if s.Snapshots == nil || s.Snapshots.Count != 2 || s.Task.Installed == nil || !*s.Task.Installed {
		t.Errorf("snapshots %+v task %+v", s.Snapshots, s.Task)
	}
	if len(s.Jobs) != 1 || !s.Jobs[0].Failed || s.Jobs[0].LastError != "backup: source missing" ||
		s.Jobs[0].Snapshots == nil || *s.Jobs[0].Snapshots != 2 || s.Maintenance.LastPrune != nil {
		t.Errorf("jobs %+v maintenance %+v", s.Jobs, s.Maintenance)
	}
	if len(s.Recent) != 2 || s.Recent[0].Error != "source missing" || s.Recent[1].Warnings != 2 {
		t.Errorf("recent problems %+v", s.Recent)
	}
}

func TestStatusJSONWithoutSnapshots(t *testing.T) {
	c, d, _ := newStatusCase(t)
	d.repo.newBackend = nil // must not be reached
	c.opts.out = &jsonLines{w: c.out, command: "status"}
	if err := runStatus(context.Background(), c.opts, statusOptions{noSnapshots: true}, d); err != nil {
		t.Fatalf("%v\n%s", err, c.errOut)
	}
	var got struct {
		Data statusOut `json:"data"`
	}
	if err := json.Unmarshal(c.out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Data.Snapshots != nil || got.Data.Jobs[0].Snapshots != nil || len(got.Data.Problems) != 0 {
		t.Errorf("%+v", got.Data)
	}
}
