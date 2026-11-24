package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/space"
	"github.com/WildPino/ouroboros/internal/state"
)

// RoutineMaxUnused is the unused space a routine prune may leave behind
// (DESIGN.md §3.6).
const RoutineMaxUnused = "5%"

// Backend is the part of the storage engine that a run uses directly; all
// deletions go through Space.
type Backend interface {
	Backup(ctx context.Context, job backend.Job) (backend.BackupResult, error)
	EstimateBackup(ctx context.Context, job backend.Job) (uint64, error)
	Check(ctx context.Context, opts backend.CheckOptions) error
}

// Space is the space manager (implemented by *space.Reclaimer).
type Space interface {
	ApplyRetention(ctx context.Context, k space.KeepRules, loc *time.Location) (space.Report, error)
	CheckFits(growth uint64) error
	Reclaim(ctx context.Context, growth uint64) (space.Report, error)
	DiskFull() (bool, error)
	RecoverFromFullDisk(ctx context.Context, predicted uint64) (space.Report, error)
	RoutinePrune(ctx context.Context, maxUnused string) (space.Report, error)
}

var _ Space = (*space.Reclaimer)(nil)

// Runner executes one "ouroboros run".
type Runner struct {
	Config      *config.Config
	Backend     Backend
	Space       Space
	StatePath   string
	HistoryPath string
	Clock       space.Clock
	Location    *time.Location // for time-based retention; nil = local time
	Log         *slog.Logger
}

// Options selects what a run does.
type Options struct {
	// Job restricts the run to one job; maintenance is then skipped.
	Job string
	// Force runs the selected jobs even if they are not due.
	Force bool
}

// JobResult is the outcome of one job.
type JobResult struct {
	Job       string
	Predicted uint64
	Backup    backend.BackupResult // valid when Err is nil
	Err       error
	Warnings  []string
}

// Result is the outcome of a run.
type Result struct {
	Jobs []JobResult
	// Errors are failures outside a job: retention and maintenance.
	Errors []error
	// Warnings are problems that did not stop anything.
	Warnings []string
}

// CannotReclaim reports whether some job was skipped for lack of space.
func (r Result) CannotReclaim() bool {
	for _, j := range r.Jobs {
		if errors.Is(j.Err, space.ErrCannotReclaim) {
			return true
		}
	}
	return false
}

// Failed reports whether anything failed for a reason other than lack of
// space.
func (r Result) Failed() bool {
	if len(r.Errors) > 0 {
		return true
	}
	for _, j := range r.Jobs {
		if j.Err != nil && !errors.Is(j.Err, space.ErrCannotReclaim) {
			return true
		}
	}
	return false
}

// HasWarnings reports whether anything completed with warnings.
func (r Result) HasWarnings() bool {
	if len(r.Warnings) > 0 {
		return true
	}
	for _, j := range r.Jobs {
		if len(j.Warnings) > 0 {
			return true
		}
	}
	return false
}

func (r *Runner) log() *slog.Logger {
	if r.Log == nil {
		return slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return r.Log
}

// Run performs the due work (DESIGN.md §4): time-based retention, then each
// due job in configuration order (space check, backup, post-backup check),
// then routine maintenance. The returned error is reserved for failures that
// make the run itself unreliable, such as being unable to save the state;
// everything else is reported in Result.
func (r *Runner) Run(ctx context.Context, opts Options) (Result, error) {
	var res Result
	st, err := state.Load(r.StatePath)
	if err != nil {
		// Losing the timestamps only makes every task due, which is safe.
		r.log().Warn("state file unreadable, starting from an empty state", "err", err)
		res.Warnings = append(res.Warnings, fmt.Sprintf("state file unreadable, starting from an empty state: %v", err))
	}
	history, skipped, err := state.ReadHistory(r.HistoryPath)
	if err != nil {
		return res, fmt.Errorf("read history: %w", err)
	}
	if skipped > 0 {
		r.log().Warn("history has unreadable lines", "skipped", skipped)
	}

	jobs, err := r.dueJobs(st, opts)
	if err != nil {
		return res, err
	}

	if len(jobs) > 0 {
		r.retention(ctx, &res)
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			break
		}
		jr, err := r.runJob(ctx, &st, history, job)
		res.Jobs = append(res.Jobs, jr)
		if err != nil {
			return res, err
		}
	}
	if opts.Job == "" && ctx.Err() == nil {
		if err := r.maintenance(ctx, &st, &res); err != nil {
			return res, err
		}
	}
	return res, ctx.Err()
}

func (r *Runner) dueJobs(st state.State, opts Options) ([]config.Job, error) {
	now := r.Clock.Now()
	retry := r.Config.Schedule.RetryAfter.Std()
	var out []config.Job
	found := false
	for _, j := range r.Config.Jobs {
		if opts.Job != "" && !strings.EqualFold(j.Name, opts.Job) {
			continue
		}
		found = true
		if opts.Force || JobDue(st.Jobs[j.Name], j.Interval.Std(), retry, now) {
			out = append(out, j)
		}
	}
	if opts.Job != "" && !found {
		return nil, fmt.Errorf("no job named %q in the configuration", opts.Job)
	}
	return out, nil
}

func (r *Runner) retention(ctx context.Context, res *Result) {
	rc := r.Config.Retention
	k := space.KeepRules{Hourly: rc.KeepHourly, Daily: rc.KeepDaily, Weekly: rc.KeepWeekly, Monthly: rc.KeepMonthly, Yearly: rc.KeepYearly}
	if !k.Enabled() {
		return
	}
	loc := r.Location
	if loc == nil {
		loc = time.Local
	}
	start := r.Clock.Now()
	rep, err := r.Space.ApplyRetention(ctx, k, loc)
	rec := state.Record{Kind: state.KindRetention, Forgotten: len(rep.Forgotten)}
	if err != nil {
		res.Errors = append(res.Errors, fmt.Errorf("retention: %w", err))
		r.log().Error("retention failed", "err", err)
	}
	r.appendRecord(rec, start, err)
}

// runJob runs one job. Its error return is reserved for state persistence
// failures; the job's own outcome is in JobResult.
func (r *Runner) runJob(ctx context.Context, st *state.State, history []state.Record, job config.Job) (JobResult, error) {
	jr := JobResult{Job: job.Name}
	log := r.log().With("job", job.Name)

	// Record the attempt first: if the process dies during the backup, the
	// job counts as failed and is retried after retry_after.
	start := r.Clock.Now()
	js := st.Jobs[job.Name]
	js.LastAttempt = start
	st.Jobs[job.Name] = js
	if err := state.Save(r.StatePath, *st); err != nil {
		return jr, fmt.Errorf("save state: %w", err)
	}

	log.Info("job started")
	attempted, err := r.backupJob(ctx, job, history, &jr)
	end := r.Clock.Now()
	rec := state.Record{Kind: state.KindBackup, Job: job.Name, PredictedGrowth: jr.Predicted}
	if err != nil {
		jr.Err = err
		js.LastError = err.Error()
		log.Error("job failed", "err", err)
	} else {
		b := jr.Backup
		js.LastSuccess, js.LastError = end, ""
		rec.SnapshotID = b.SnapshotID
		rec.BytesProcessed, rec.BytesAdded, rec.BytesAddedPacked = b.BytesProcessed, b.BytesAdded, b.BytesAddedPacked
		rec.FilesNew, rec.FilesChanged = b.FilesNew, b.FilesChanged
		rec.Incomplete, rec.Warnings = b.Incomplete, len(b.Warnings)
		if b.Incomplete {
			jr.Warnings = append(jr.Warnings, fmt.Sprintf("%d files or folders could not be read", len(b.Warnings)))
			for _, w := range b.Warnings {
				log.Warn("unreadable source", "detail", w)
			}
		}
		log.Info("job finished", "snapshot", b.SnapshotID, "added_packed", b.BytesAddedPacked, "predicted", jr.Predicted)
	}
	st.Jobs[job.Name] = js
	r.appendRecord(rec, start, err)
	if err := state.Save(r.StatePath, *st); err != nil {
		return jr, fmt.Errorf("save state: %w", err)
	}

	// After any backup attempt, restore min_free and the prune headroom:
	// the backup may have grown more than predicted, and a failed one may
	// have left unused data.
	if attempted && ctx.Err() == nil {
		start := r.Clock.Now()
		rep, err := r.Space.Reclaim(ctx, 0)
		r.recordReclaim(rep, start, err)
		if err != nil {
			jr.Warnings = append(jr.Warnings, fmt.Sprintf("post-backup space check: %v", err))
			log.Warn("post-backup space check failed", "err", err)
		}
	}
	return jr, nil
}

// backupJob predicts growth, makes room and backs up, retrying once after a
// full disk (DESIGN.md §3.9). attempted reports whether restic backup ran.
func (r *Runner) backupJob(ctx context.Context, job config.Job, history []state.Record, jr *JobResult) (attempted bool, err error) {
	bj := backend.Job{Name: job.Name, Paths: job.Paths, Exclude: job.Exclude, ExcludeFile: job.ExcludeFile, VSS: job.UseVSS()}

	predicted, err := r.predict(ctx, bj, history)
	if err != nil {
		return false, err
	}
	jr.Predicted = predicted
	if err := r.Space.CheckFits(predicted); err != nil {
		return false, err
	}
	start := r.Clock.Now()
	rep, err := r.Space.Reclaim(ctx, predicted)
	r.recordReclaim(rep, start, err)
	if err != nil {
		return false, fmt.Errorf("make room for the backup: %w", err)
	}

	res, err := r.Backend.Backup(ctx, bj)
	if err != nil && ctx.Err() == nil {
		if full, ferr := r.Space.DiskFull(); ferr == nil && full {
			r.log().Warn("backup failed on a full disk, recovering", "job", job.Name, "err", err)
			start := r.Clock.Now()
			rep, rerr := r.Space.RecoverFromFullDisk(ctx, predicted)
			r.recordReclaim(rep, start, rerr)
			if rerr != nil {
				return true, fmt.Errorf("backup failed on a full disk: %w; recovery failed: %w", err, rerr)
			}
			res, err = r.Backend.Backup(ctx, bj)
		}
	}
	if err != nil {
		return true, fmt.Errorf("backup: %w", err)
	}
	jr.Backup = res
	return true, nil
}

// predict estimates the growth of the next backup of job (DESIGN.md §3.3).
func (r *Runner) predict(ctx context.Context, job backend.Job, history []state.Record) (uint64, error) {
	s := r.Config.Space
	if g, ok := space.PredictGrowth(state.BackupGrowth(history, job.Name), s.GrowthFactor); ok {
		return g, nil
	}
	est, err := r.Backend.EstimateBackup(ctx, job)
	if err != nil {
		return 0, fmt.Errorf("estimate first backup: %w", err)
	}
	return space.FirstRunGrowth(est, s.FirstRunFactor), nil
}

// maintenance runs the routine prune and the integrity checks when due
// (DESIGN.md §3.6, §8). Their timestamps record the attempt, not the
// success: a failing check is reported, not repeated on every tick.
func (r *Runner) maintenance(ctx context.Context, st *state.State, res *Result) error {
	now := r.Clock.Now()
	changed := false
	if MaintenanceDue(st.LastPrune, r.Config.Space.PruneInterval.Std(), now) {
		rep, err := r.Space.RoutinePrune(ctx, RoutineMaxUnused)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("routine prune: %w", err))
			r.log().Error("routine prune failed", "err", err)
		}
		r.appendRecord(state.Record{Kind: state.KindPrune, Freed: rep.Freed()}, now, err)
		st.LastPrune, changed = now, true
	}

	ic := r.Config.Integrity
	readData := MaintenanceDue(st.LastReadDataCheck, ic.CheckReadDataInterval.Std(), now)
	if ctx.Err() == nil && (readData || MaintenanceDue(st.LastCheck, ic.CheckInterval.Std(), now)) {
		start := r.Clock.Now()
		var opts backend.CheckOptions
		if readData {
			opts.ReadDataSubset = fmt.Sprintf("%g%%", ic.CheckReadDataPercent)
		}
		err := r.Backend.Check(ctx, opts)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("repository check: %w", err))
			r.log().Error("repository check failed", "err", err)
		}
		r.appendRecord(state.Record{Kind: state.KindCheck, ReadData: opts.ReadDataSubset}, start, err)
		st.LastCheck, changed = now, true
		if readData {
			st.LastReadDataCheck = now
		}
	}
	if !changed {
		return nil
	}
	if err := state.Save(r.StatePath, *st); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}

func (r *Runner) recordReclaim(rep space.Report, start time.Time, err error) {
	if err == nil && len(rep.Forgotten) == 0 && rep.Prunes == 0 {
		return
	}
	r.appendRecord(state.Record{Kind: state.KindReclaim, Forgotten: len(rep.Forgotten), Freed: rep.Freed()}, start, err)
}

// appendRecord completes rec and appends it to the history. A failure is
// only logged: the history feeds predictions and reports, and a missing
// line must not fail a backup that succeeded.
func (r *Runner) appendRecord(rec state.Record, start time.Time, err error) {
	end := r.Clock.Now()
	rec.Time, rec.OK = end, err == nil
	rec.Duration = end.Sub(start).Seconds()
	if err != nil {
		rec.Error = err.Error()
	}
	if aerr := state.Append(r.HistoryPath, rec); aerr != nil {
		r.log().Warn("cannot append to history", "err", aerr)
	}
}
