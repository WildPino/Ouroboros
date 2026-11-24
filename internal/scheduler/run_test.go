package scheduler

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/space"
	"github.com/WildPino/ouroboros/internal/state"
)

const MB = 1 << 20

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

// world fakes both restic and the space manager and logs every call in
// order, so tests can assert on the sequence of operations.
type world struct {
	clock *clock
	calls []string

	estimate    uint64
	estimateErr error
	backups     []func() (backend.BackupResult, error) // consumed in order
	added       uint64                                 // default packed growth
	checkErr    error

	fitsErr      error
	reclaimErr   map[uint64]error // by growth
	diskFull     bool
	recoverErr   error
	retentionErr error
	pruneErr     error
}

func (w *world) log(format string, a ...any) { w.calls = append(w.calls, fmt.Sprintf(format, a...)) }

func (w *world) Backup(ctx context.Context, job backend.Job) (backend.BackupResult, error) {
	w.log("backup %s", job.Name)
	w.clock.now = w.clock.now.Add(time.Minute)
	if err := ctx.Err(); err != nil {
		return backend.BackupResult{}, err
	}
	if len(w.backups) > 0 {
		f := w.backups[0]
		w.backups = w.backups[1:]
		return f()
	}
	return backend.BackupResult{SnapshotID: "snap-" + job.Name, BytesAddedPacked: w.added}, nil
}

func (w *world) EstimateBackup(ctx context.Context, job backend.Job) (uint64, error) {
	w.log("estimate %s", job.Name)
	return w.estimate, w.estimateErr
}

func (w *world) Check(ctx context.Context, opts backend.CheckOptions) error {
	w.log("check %s", opts.ReadDataSubset)
	return w.checkErr
}

func (w *world) ApplyRetention(ctx context.Context, k space.KeepRules, loc *time.Location) (space.Report, error) {
	w.log("retention")
	return space.Report{}, w.retentionErr
}

func (w *world) CheckFits(growth uint64) error {
	w.log("fits %d", growth/MB)
	return w.fitsErr
}

func (w *world) Reclaim(ctx context.Context, growth uint64) (space.Report, error) {
	w.log("reclaim %d", growth/MB)
	return space.Report{}, w.reclaimErr[growth]
}

func (w *world) DiskFull() (bool, error) {
	w.log("diskfull")
	return w.diskFull, nil
}

func (w *world) RecoverFromFullDisk(ctx context.Context, predicted uint64) (space.Report, error) {
	w.log("recover %d", predicted/MB)
	w.diskFull = false
	return space.Report{Prunes: 1}, w.recoverErr
}

func (w *world) RoutinePrune(ctx context.Context, maxUnused string) (space.Report, error) {
	w.log("prune %s", maxUnused)
	return space.Report{Prunes: 1}, w.pruneErr
}

type fixture struct {
	t      *testing.T
	w      *world
	runner *Runner
}

func newFixture(t *testing.T) *fixture {
	cfg := config.Default()
	cfg.Repository.Path = `B:\repo`
	cfg.Jobs = []config.Job{
		{Name: "docs", Paths: []string{`C:\docs`}, Interval: config.Duration(6 * time.Hour)},
		{Name: "photos", Paths: []string{`C:\photos`}, Interval: config.Duration(24 * time.Hour)},
	}
	dir := t.TempDir()
	w := &world{clock: &clock{now: t0}, reclaimErr: map[uint64]error{}, added: 10 * MB, estimate: 100 * MB}
	return &fixture{t: t, w: w, runner: &Runner{
		Config: &cfg, Backend: w, Space: w, Clock: w.clock,
		StatePath:   filepath.Join(dir, state.StateFile),
		HistoryPath: filepath.Join(dir, state.HistoryFile),
	}}
}

// quiet marks maintenance as just done, so tests about jobs only see jobs.
func (f *fixture) quiet() {
	st := f.state()
	st.LastPrune, st.LastCheck, st.LastReadDataCheck = f.w.clock.now, f.w.clock.now, f.w.clock.now
	if err := state.Save(f.runner.StatePath, st); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) state() state.State {
	st, err := state.Load(f.runner.StatePath)
	if err != nil {
		f.t.Fatal(err)
	}
	return st
}

func (f *fixture) history() []state.Record {
	recs, _, err := state.ReadHistory(f.runner.HistoryPath)
	if err != nil {
		f.t.Fatal(err)
	}
	return recs
}

func (f *fixture) run(opts Options) Result {
	f.t.Helper()
	f.w.calls = nil
	res, err := f.runner.Run(context.Background(), opts)
	if err != nil {
		f.t.Fatal(err)
	}
	return res
}

func (f *fixture) wantCalls(want ...string) {
	f.t.Helper()
	if !slices.Equal(f.w.calls, want) {
		f.t.Fatalf("calls:\n  %s\nwant:\n  %s", strings.Join(f.w.calls, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestRunFirstAndSecondBackup(t *testing.T) {
	f := newFixture(t)
	f.quiet()
	res := f.run(Options{})
	// First run of each job: dry-run estimate × first_run_factor (1.0).
	f.wantCalls(
		"estimate docs", "fits 100", "reclaim 100", "backup docs", "reclaim 0",
		"estimate photos", "fits 100", "reclaim 100", "backup photos", "reclaim 0",
	)
	if res.Failed() || res.HasWarnings() || len(res.Jobs) != 2 || res.Jobs[0].Backup.SnapshotID != "snap-docs" {
		t.Fatalf("result %+v", res)
	}
	st := f.state()
	if js := st.Jobs["docs"]; js.Failed() || js.LastSuccess.IsZero() || js.LastError != "" {
		t.Fatalf("docs state %+v", js)
	}
	recs := f.history()
	if len(recs) != 2 || recs[0].Kind != state.KindBackup || !recs[0].OK || recs[0].BytesAddedPacked != 10*MB || recs[0].PredictedGrowth != 100*MB {
		t.Fatalf("history %+v", recs)
	}

	// Nothing is due a minute later.
	f.run(Options{})
	f.wantCalls()

	// Six hours later only docs is due, predicted from history: 10 MB × 1.5.
	f.w.clock.now = f.w.clock.now.Add(6 * time.Hour)
	f.quiet()
	f.run(Options{})
	f.wantCalls("fits 15", "reclaim 15", "backup docs", "reclaim 0")
}

func TestRunCannotReclaimSkipsBackup(t *testing.T) {
	f := newFixture(t)
	f.quiet()
	f.runner.Config.Jobs = f.runner.Config.Jobs[:1]
	f.w.reclaimErr[100*MB] = &space.CannotReclaimError{Missing: 5 * MB}
	res := f.run(Options{})
	f.wantCalls("estimate docs", "fits 100", "reclaim 100")
	if !res.CannotReclaim() || res.Failed() {
		t.Fatalf("result %+v", res)
	}
	js := f.state().Jobs["docs"]
	if !js.Failed() || !strings.Contains(js.LastError, "cannot reclaim") {
		t.Fatalf("state %+v", js)
	}
	if recs := f.history(); len(recs) != 2 || recs[0].Kind != state.KindReclaim || recs[0].OK || recs[1].Kind != state.KindBackup || recs[1].OK {
		t.Fatalf("history %+v", recs)
	}
}

func TestRunTooLarge(t *testing.T) {
	f := newFixture(t)
	f.quiet()
	f.runner.Config.Jobs = f.runner.Config.Jobs[:1]
	f.w.fitsErr = space.ErrBackupTooLarge
	res := f.run(Options{})
	// Nothing is deleted for a backup that could never fit.
	f.wantCalls("estimate docs", "fits 100")
	if !errors.Is(res.Jobs[0].Err, space.ErrBackupTooLarge) || !res.Failed() {
		t.Fatalf("result %+v", res)
	}
}

func TestRunFullDiskRecovery(t *testing.T) {
	f := newFixture(t)
	f.quiet()
	f.runner.Config.Jobs = f.runner.Config.Jobs[:1]
	f.w.diskFull = true
	f.w.backups = []func() (backend.BackupResult, error){
		func() (backend.BackupResult, error) {
			return backend.BackupResult{}, errors.New("write: not enough space")
		},
	}
	res := f.run(Options{})
	f.wantCalls("estimate docs", "fits 100", "reclaim 100", "backup docs", "diskfull", "recover 100", "backup docs", "reclaim 0")
	if res.Failed() || f.state().Jobs["docs"].Failed() {
		t.Fatalf("result %+v", res)
	}
	recs := f.history()
	if len(recs) != 2 || recs[0].Kind != state.KindReclaim || recs[1].Kind != state.KindBackup || !recs[1].OK {
		t.Fatalf("history %+v", recs)
	}
}

func TestRunFullDiskRecoveryFails(t *testing.T) {
	f := newFixture(t)
	f.quiet()
	f.runner.Config.Jobs = f.runner.Config.Jobs[:1]
	f.w.diskFull = true
	f.w.recoverErr = &space.CannotReclaimError{}
	f.w.backups = []func() (backend.BackupResult, error){
		func() (backend.BackupResult, error) { return backend.BackupResult{}, errors.New("disk full") },
	}
	res := f.run(Options{})
	// The backup is not retried, but the post-backup check still runs.
	f.wantCalls("estimate docs", "fits 100", "reclaim 100", "backup docs", "diskfull", "recover 100", "reclaim 0")
	if !res.CannotReclaim() {
		t.Fatalf("result %+v", res)
	}
}

func TestRunFailureRetriesAfterRetryAfter(t *testing.T) {
	f := newFixture(t)
	f.quiet()
	f.runner.Config.Jobs = f.runner.Config.Jobs[:1]
	f.w.backups = []func() (backend.BackupResult, error){
		func() (backend.BackupResult, error) { return backend.BackupResult{}, errors.New("source vanished") },
	}
	res := f.run(Options{})
	f.wantCalls("estimate docs", "fits 100", "reclaim 100", "backup docs", "diskfull", "reclaim 0")
	if !res.Failed() || res.CannotReclaim() {
		t.Fatalf("result %+v", res)
	}

	f.w.clock.now = f.w.clock.now.Add(20 * time.Minute)
	f.run(Options{})
	f.wantCalls()

	f.w.clock.now = f.w.clock.now.Add(10 * time.Minute)
	res = f.run(Options{})
	f.wantCalls("estimate docs", "fits 100", "reclaim 100", "backup docs", "reclaim 0")
	if res.Failed() || f.state().Jobs["docs"].Failed() {
		t.Fatalf("retry: %+v", res)
	}
}

func TestRunIncompleteIsWarning(t *testing.T) {
	f := newFixture(t)
	f.quiet()
	f.runner.Config.Jobs = f.runner.Config.Jobs[:1]
	f.w.backups = []func() (backend.BackupResult, error){
		func() (backend.BackupResult, error) {
			return backend.BackupResult{SnapshotID: "s", Incomplete: true, Warnings: []string{"a: denied", "b: denied"}}, nil
		},
	}
	res := f.run(Options{})
	if res.Failed() || !res.HasWarnings() || !strings.Contains(res.Jobs[0].Warnings[0], "2 files") {
		t.Fatalf("result %+v", res)
	}
	if f.state().Jobs["docs"].Failed() {
		t.Fatal("an incomplete snapshot is a success")
	}
	if recs := f.history(); !recs[0].Incomplete || recs[0].Warnings != 2 {
		t.Fatalf("history %+v", recs)
	}
}

func TestRunPostCheckFailureIsWarning(t *testing.T) {
	f := newFixture(t)
	f.quiet()
	f.runner.Config.Jobs = f.runner.Config.Jobs[:1]
	f.w.reclaimErr[0] = &space.CannotReclaimError{}
	res := f.run(Options{})
	if res.Failed() || res.CannotReclaim() || !res.HasWarnings() {
		t.Fatalf("result %+v", res)
	}
}

func TestRunSelectedJob(t *testing.T) {
	f := newFixture(t)
	// Maintenance is due, but a run restricted to one job skips it.
	res := f.run(Options{Job: "PHOTOS"})
	f.wantCalls("estimate photos", "fits 100", "reclaim 100", "backup photos", "reclaim 0")
	if len(res.Jobs) != 1 {
		t.Fatalf("result %+v", res)
	}
	// Not due any more: without --force nothing happens.
	f.run(Options{Job: "photos"})
	f.wantCalls()
	f.run(Options{Job: "photos", Force: true})
	f.wantCalls("fits 15", "reclaim 15", "backup photos", "reclaim 0")

	if _, err := f.runner.Run(context.Background(), Options{Job: "nope"}); err == nil || !strings.Contains(err.Error(), "no job named") {
		t.Fatalf("unknown job: %v", err)
	}
}

func TestRunForceAll(t *testing.T) {
	f := newFixture(t)
	f.quiet()
	f.run(Options{})
	f.run(Options{Force: true})
	f.wantCalls("fits 15", "reclaim 15", "backup docs", "reclaim 0", "fits 15", "reclaim 15", "backup photos", "reclaim 0")
}

func TestRunRetention(t *testing.T) {
	f := newFixture(t)
	f.quiet()
	f.runner.Config.Jobs = f.runner.Config.Jobs[:1]
	f.runner.Config.Retention.KeepDaily = 7
	f.run(Options{})
	f.wantCalls("retention", "estimate docs", "fits 100", "reclaim 100", "backup docs", "reclaim 0")

	// No job due: retention does not run either.
	f.run(Options{})
	f.wantCalls()

	// A retention failure is reported but does not stop the backup.
	f.w.retentionErr = errors.New("repository locked")
	f.w.clock.now = f.w.clock.now.Add(6 * time.Hour)
	f.quiet()
	res := f.run(Options{})
	f.wantCalls("retention", "fits 15", "reclaim 15", "backup docs", "reclaim 0")
	if !res.Failed() || res.Jobs[0].Err != nil {
		t.Fatalf("result %+v", res)
	}
}

func TestRunMaintenance(t *testing.T) {
	f := newFixture(t)
	f.runner.Config.Jobs = nil
	// Fresh state: prune, and a check that also reads back 5% of the data.
	res := f.run(Options{})
	f.wantCalls("prune 5%", "check 5%")
	if res.Failed() {
		t.Fatalf("result %+v", res)
	}
	st := f.state()
	if !st.LastPrune.Equal(t0) || !st.LastCheck.Equal(t0) || !st.LastReadDataCheck.Equal(t0) {
		t.Fatalf("state %+v", st)
	}

	// After the check interval (7d) only a plain check is due.
	f.w.clock.now = t0.Add(7 * 24 * time.Hour)
	f.run(Options{})
	f.wantCalls("prune 5%", "check ")

	// A failing check is reported once, not repeated on every tick.
	f.w.checkErr = errors.New("pack damaged")
	f.w.clock.now = t0.Add(30 * 24 * time.Hour)
	res = f.run(Options{})
	f.wantCalls("prune 5%", "check 5%")
	if !res.Failed() {
		t.Fatal("check failure not reported")
	}
	f.w.clock.now = f.w.clock.now.Add(15 * time.Minute)
	f.run(Options{})
	f.wantCalls()

	kinds := []string{}
	for _, r := range f.history() {
		kinds = append(kinds, r.Kind)
	}
	if want := []string{"prune", "check", "prune", "check", "prune", "check"}; !slices.Equal(kinds, want) {
		t.Fatalf("history kinds %v", kinds)
	}
}

func TestRunCanceled(t *testing.T) {
	f := newFixture(t)
	f.quiet()
	ctx, cancel := context.WithCancel(context.Background())
	f.w.backups = []func() (backend.BackupResult, error){
		func() (backend.BackupResult, error) { cancel(); return backend.BackupResult{}, context.Canceled },
	}
	res, err := f.runner.Run(ctx, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	// The second job is not started; the first counts as failed so that it
	// is retried.
	if len(res.Jobs) != 1 || !f.state().Jobs["docs"].Failed() {
		t.Fatalf("result %+v state %+v", res, f.state())
	}
	if slices.Contains(f.w.calls, "diskfull") || slices.Contains(f.w.calls, "reclaim 0") {
		t.Fatalf("work after cancel: %v", f.w.calls)
	}
}

func TestRunCorruptStateIsWarning(t *testing.T) {
	f := newFixture(t)
	f.runner.Config.Jobs = f.runner.Config.Jobs[:1]
	if err := state.Save(f.runner.StatePath, state.State{}); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(f.runner.StatePath, "{broken"); err != nil {
		t.Fatal(err)
	}
	res := f.run(Options{})
	if !res.HasWarnings() || len(res.Jobs) != 1 || res.Jobs[0].Err != nil {
		t.Fatalf("result %+v", res)
	}
	if _, err := state.Load(f.runner.StatePath); err != nil {
		t.Fatalf("state not rewritten: %v", err)
	}
}
