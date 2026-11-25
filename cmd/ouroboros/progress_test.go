package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
	"github.com/WildPino/ouroboros/internal/lock"
	"github.com/WildPino/ouroboros/internal/scheduler"
	"github.com/WildPino/ouroboros/internal/state"
)

func TestProgressReporter(t *testing.T) {
	path := filepath.Join(t.TempDir(), state.ProgressFile)
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	var out, errOut bytes.Buffer
	p := newProgressReporter(path, &jsonLines{w: &out, command: "run"}, &errOut, func() time.Time { return now })

	load := func() state.Progress {
		t.Helper()
		pr, ok, err := state.LoadProgress(path)
		if err != nil || !ok {
			t.Fatalf("progress.json: ok=%v err=%v", ok, err)
		}
		return pr
	}
	p.report(scheduler.Progress{Phase: scheduler.PhasePrepare, Job: "docs"})
	if pr := load(); pr.Phase != "prepare" || pr.Job != "docs" || !pr.Started.Equal(now) {
		t.Fatalf("%+v", pr)
	}

	// A new phase is written at once; reports within the interval are not.
	now = now.Add(time.Second)
	p.report(scheduler.Progress{Phase: scheduler.PhaseBackup, Job: "docs"})
	now = now.Add(time.Second)
	p.report(scheduler.Progress{Phase: scheduler.PhaseBackup, Job: "docs", Backup: &backend.BackupProgress{PercentDone: 0.1}})
	if pr := load(); pr.Phase != "backup" || pr.Backup != nil {
		t.Fatalf("throttled report written: %+v", pr)
	}
	now = now.Add(time.Second)
	p.report(scheduler.Progress{Phase: scheduler.PhaseBackup, Job: "docs", Backup: &backend.BackupProgress{PercentDone: 0.4, BytesDone: 4}})
	pr := load()
	if pr.Backup == nil || pr.Backup.PercentDone != 0.4 || !pr.PhaseStarted.Equal(pr.Started.Add(time.Second)) {
		t.Fatalf("%+v", pr)
	}

	// The backup figures do not carry over into the next phase.
	p.report(scheduler.Progress{Phase: scheduler.PhaseSpaceCheck, Job: "docs"})
	if pr := load(); pr.Backup != nil || pr.Phase != "space_check" {
		t.Fatalf("%+v", pr)
	}
	p.finish()
	if _, ok, _ := state.LoadProgress(path); ok {
		t.Fatal("progress.json not removed")
	}
	if n := strings.Count(out.String(), `"type":"event","command":"run"`); n != 4 {
		t.Fatalf("%d events:\n%s", n, &out)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr %q", &errOut)
	}
}

func TestProgressReporterWarnsOnce(t *testing.T) {
	// A path in a missing directory under a file cannot be written.
	file := filepath.Join(t.TempDir(), "file")
	if err := writeFile(file); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	now := time.Now()
	p := newProgressReporter(filepath.Join(file, "sub", state.ProgressFile), nil, &errOut, func() time.Time { return now })
	p.report(scheduler.Progress{Phase: scheduler.PhasePrepare})
	p.report(scheduler.Progress{Phase: scheduler.PhaseBackup})
	if n := strings.Count(errOut.String(), "warning: cannot write progress.json"); n != 1 {
		t.Fatalf("stderr %q", &errOut)
	}
}

func TestRunJSONWhenLocked(t *testing.T) {
	var out bytes.Buffer
	d := failingRepoDeps(t)
	d.acquire = func() (*lock.Lock, error) { return nil, lock.ErrLocked }
	opts := &options{configPath: writeConfig(t, testConfig), stdout: &out, stderr: &out, json: true}
	opts.out = &jsonLines{w: &out, command: "run"}
	if err := runRun(context.Background(), opts, scheduler.Options{}, d); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Type string `json:"type"`
		Data runOut `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, &out)
	}
	if got.Type != "result" || !got.Data.Locked || got.Data.Jobs == nil {
		t.Fatalf("%+v", got)
	}
}

func TestRunJSON(t *testing.T) {
	start := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	res := scheduler.Result{
		Jobs: []scheduler.JobResult{
			{Job: "docs", Predicted: 15, Warnings: []string{"2 files or folders could not be read"},
				Backup: backend.BackupResult{SnapshotID: "abc", Start: start, End: start.Add(90 * time.Second), BytesAddedPacked: 10, FilesNew: 3}},
			{Job: "photos", Predicted: 99, Err: errRunInProgress},
		},
		Warnings: []string{"state file unreadable"},
	}
	out := runJSON(res)
	d, p := out.Jobs[0], out.Jobs[1]
	if out.Locked || !d.OK || d.SnapshotID != "abc" || d.AddedBytes != 10 || d.DurationSeconds != 90 || len(d.Warnings) != 1 {
		t.Fatalf("docs %+v", d)
	}
	if p.OK || p.ErrorCode != "locked" || p.Error == "" || p.Warnings == nil {
		t.Fatalf("photos %+v", p)
	}
	if len(out.Warnings) != 1 || out.Errors == nil {
		t.Fatalf("%+v", out)
	}
}

func writeFile(path string) error { return os.WriteFile(path, []byte("x"), 0o644) }
