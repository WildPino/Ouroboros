package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/lock"
	"github.com/WildPino/ouroboros/internal/scheduler"
	"github.com/WildPino/ouroboros/internal/space"
)

func failingRepoDeps(t *testing.T) repoDeps {
	fail := func() { t.Helper(); t.Fatal("must not be reached") }
	return repoDeps{
		isElevated:  func() (bool, error) { return true, nil },
		acquire:     func() (*lock.Lock, error) { fail(); return nil, nil },
		checkRestic: func(context.Context, *config.Config) error { fail(); return nil },
	}
}

func TestRunRefusesWithoutElevation(t *testing.T) {
	var out bytes.Buffer
	d := failingRepoDeps(t)
	d.isElevated = func() (bool, error) { return false, nil }
	opts := &options{configPath: writeConfig(t, testConfig), stdout: &out, stderr: &out}
	err := runRun(context.Background(), opts, scheduler.Options{}, d)
	if err == nil || !strings.Contains(err.Error(), "Administrator") {
		t.Fatalf("got %v", err)
	}
}

func TestRunExitsQuietlyWhenLocked(t *testing.T) {
	var out bytes.Buffer
	d := failingRepoDeps(t)
	d.acquire = func() (*lock.Lock, error) { return nil, lock.ErrLocked }
	opts := &options{configPath: writeConfig(t, testConfig), stdout: &out, stderr: &out}
	if err := runRun(context.Background(), opts, scheduler.Options{}, d); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Another ouroboros process is running") {
		t.Fatalf("output %q", out.String())
	}
}

func TestRunExitCode(t *testing.T) {
	cannot := scheduler.JobResult{Err: &space.CannotReclaimError{}}
	failed := scheduler.JobResult{Err: errors.New("boom")}
	warned := scheduler.JobResult{Warnings: []string{"w"}}
	tests := []struct {
		res  scheduler.Result
		want int
	}{
		{scheduler.Result{}, exitOK},
		{scheduler.Result{Jobs: []scheduler.JobResult{{}}}, exitOK},
		{scheduler.Result{Jobs: []scheduler.JobResult{warned}}, exitWarnings},
		{scheduler.Result{Warnings: []string{"state"}}, exitWarnings},
		{scheduler.Result{Jobs: []scheduler.JobResult{warned, cannot}}, exitCannotReclaim},
		{scheduler.Result{Jobs: []scheduler.JobResult{cannot, failed}}, exitError},
		{scheduler.Result{Errors: []error{errors.New("check")}}, exitError},
	}
	for i, tt := range tests {
		if got := runExitCode(tt.res); got != tt.want {
			t.Errorf("%d: code %d, want %d", i, got, tt.want)
		}
	}
}

func TestWriteRunResult(t *testing.T) {
	at := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	res := scheduler.Result{
		Jobs: []scheduler.JobResult{
			{Job: "docs", Predicted: 15 << 20, Backup: backend.BackupResult{
				SnapshotID: "0123456789abcdef", BytesAddedPacked: 10 << 20, Start: at, End: at.Add(90 * time.Second),
			}, Warnings: []string{"2 files or folders could not be read"}},
			{Job: "photos", Err: &space.CannotReclaimError{Missing: 1 << 30}},
			{Job: "music", Err: errors.New("backup: source missing")},
		},
		Errors:   []error{errors.New("repository check: pack damaged")},
		Warnings: []string{"state file unreadable"},
	}
	var out bytes.Buffer
	writeRunResult(&out, res, scheduler.Options{})
	got := out.String()
	for _, want := range []string{
		"docs: snapshot 01234567, 10.0 MB added (predicted 15.0 MB), 1m30s",
		"docs: warning: 2 files or folders could not be read",
		"photos: skipped, not enough space: cannot reclaim enough space",
		"music: failed: backup: source missing",
		"error: repository check: pack damaged",
		"warning: state file unreadable",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}

	out.Reset()
	writeRunResult(&out, scheduler.Result{}, scheduler.Options{})
	if out.String() != "No job is due.\n" {
		t.Errorf("got %q", out.String())
	}
	out.Reset()
	writeRunResult(&out, scheduler.Result{}, scheduler.Options{Job: "docs"})
	if !strings.Contains(out.String(), "--force") {
		t.Errorf("got %q", out.String())
	}
}
