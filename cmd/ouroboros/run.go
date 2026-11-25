package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/WildPino/ouroboros/internal/backend"
	"github.com/WildPino/ouroboros/internal/backend/restic"
	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/lock"
	"github.com/WildPino/ouroboros/internal/logging"
	"github.com/WildPino/ouroboros/internal/platform/windows"
	"github.com/WildPino/ouroboros/internal/scheduler"
	"github.com/WildPino/ouroboros/internal/space"
	"github.com/WildPino/ouroboros/internal/state"
)

// repoDeps holds what the repository commands need from the system,
// replaceable in tests.
type repoDeps struct {
	isElevated  func() (bool, error)
	acquire     func() (*lock.Lock, error)
	checkRestic func(ctx context.Context, cfg *config.Config) error
	newBackend  func(cfg *config.Config, configPath string) (backend.Backend, error)
	hostname    func() (string, error)
	openEvents  eventOpener
}

func defaultRepoDeps() repoDeps {
	return repoDeps{
		isElevated:  windows.IsElevated,
		acquire:     func() (*lock.Lock, error) { return lock.Acquire(lock.Name) },
		checkRestic: checkResticVersion,
		newBackend: func(cfg *config.Config, p string) (backend.Backend, error) {
			b, err := newResticBackend(cfg, p)
			if err != nil {
				return nil, err
			}
			return b, nil
		},
		hostname:   os.Hostname,
		openEvents: logging.OpenEventLog,
	}
}

func newRunCmd(opts *options) *cobra.Command {
	var ropts scheduler.Options
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the jobs that are due",
		Long: "Run the backup jobs that are due, making room on the repository partition first,\n" +
			"then the routine maintenance (prune, check) when due. The scheduled task runs\n" +
			"this command every few minutes. With --job only that job runs and maintenance\n" +
			"is skipped; --force runs the selected jobs even if they are not due.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRun(cmd.Context(), opts, ropts, defaultRepoDeps())
		},
	}
	cmd.Flags().StringVar(&ropts.Job, "job", "", "run only the job with this name")
	cmd.Flags().BoolVar(&ropts.Force, "force", false, "run even if not due")
	return supportsJSON(cmd)
}

func runRun(ctx context.Context, opts *options, ropts scheduler.Options, d repoDeps) error {
	sess, err := openRepo(ctx, opts, d, "run", true)
	if errors.Is(err, lock.ErrLocked) {
		// Not an error: the scheduled task finds the lock taken whenever a
		// run lasts longer than a tick.
		if opts.out != nil {
			opts.out.result(runOut{Locked: true, Jobs: []runJobOut{}, Errors: []string{}, Warnings: []string{}})
			return nil
		}
		fmt.Fprintln(opts.stdout, "Another ouroboros process is running; nothing to do.")
		return nil
	}
	if err != nil {
		return err
	}
	defer sess.release()
	cfg, b := sess.cfg, sess.backend
	r, err := newReclaimer(cfg, b, sess.host)
	if err != nil {
		return err
	}
	log, closeLog := openLog(cfg, opts.configPath, opts.stderr, d.openEvents)
	defer closeLog()
	r.Log = log
	dir := filepath.Dir(opts.configPath)
	// restic cannot write anything with less than about two packs free.
	_, packSizeMB := resolvePerformance(cfg)
	runner := &scheduler.Runner{
		StallFree:   2 * uint64(packSizeMB) << 20,
		Config:      cfg,
		Backend:     b,
		Space:       r,
		StatePath:   filepath.Join(dir, state.StateFile),
		HistoryPath: filepath.Join(dir, state.HistoryFile),
		Clock:       space.SystemClock{},
		Log:         log,
	}
	progress := newProgressReporter(filepath.Join(dir, state.ProgressFile), opts.out, opts.stderr, time.Now)
	runner.Progress = progress.report
	res, err := runner.Run(ctx, ropts)
	progress.finish()
	if opts.out != nil {
		if err == nil {
			opts.out.result(runJSON(res))
		}
	} else {
		writeRunResult(opts.stdout, res, ropts)
	}
	if err != nil {
		if ctx.Err() != nil {
			return errors.New("interrupted")
		}
		return err
	}
	if code := runExitCode(res); code != exitOK {
		return &codedError{code: code}
	}
	return nil
}

// runExitCode maps a run outcome to the exit codes of DESIGN.md §9. A
// failure outranks a lack of space, which outranks warnings.
func runExitCode(res scheduler.Result) int {
	switch {
	case res.Failed():
		return exitError
	case res.CannotReclaim():
		return exitCannotReclaim
	case res.HasWarnings():
		return exitWarnings
	default:
		return exitOK
	}
}

func writeRunResult(w io.Writer, res scheduler.Result, ropts scheduler.Options) {
	if len(res.Jobs) == 0 && ropts.Job == "" {
		fmt.Fprintln(w, "No job is due.")
	}
	if len(res.Jobs) == 0 && ropts.Job != "" {
		fmt.Fprintf(w, "Job %s is not due; use --force to run it now.\n", ropts.Job)
	}
	for _, j := range res.Jobs {
		switch {
		case errors.Is(j.Err, space.ErrCannotReclaim):
			fmt.Fprintf(w, "%s: skipped, not enough space: %v\n", j.Job, j.Err)
		case j.Err != nil:
			fmt.Fprintf(w, "%s: failed: %v\n", j.Job, j.Err)
		default:
			b := j.Backup
			fmt.Fprintf(w, "%s: snapshot %s, %s added (predicted %s), %s\n", j.Job, shortID(b.SnapshotID),
				formatBytes(b.BytesAddedPacked), formatBytes(j.Predicted), b.End.Sub(b.Start).Round(time.Second))
		}
		for _, warn := range j.Warnings {
			fmt.Fprintf(w, "%s: warning: %s\n", j.Job, warn)
		}
	}
	for _, e := range res.Errors {
		fmt.Fprintf(w, "error: %v\n", e)
	}
	for _, warn := range res.Warnings {
		fmt.Fprintf(w, "warning: %s\n", warn)
	}
}

// checkResticVersion enforces the minimum restic version at startup
// (DESIGN.md §2.2).
func checkResticVersion(ctx context.Context, cfg *config.Config) error {
	exe, err := restic.FindExecutable(cfg.Repository.ResticPath)
	if err != nil {
		return err
	}
	v, err := restic.ExecutableVersion(ctx, exe)
	if err != nil {
		return err
	}
	if !v.AtLeast(restic.MinVersion) {
		return fmt.Errorf("restic %s is too old: %s or later is required", v, restic.MinVersion)
	}
	return nil
}

// Schema of "run --json"; documented in docs/JSON.md.
type runOut struct {
	// Locked is true when another process held the run lock and nothing
	// was done.
	Locked   bool        `json:"locked"`
	Jobs     []runJobOut `json:"jobs"`
	Errors   []string    `json:"errors"`
	Warnings []string    `json:"warnings"`
}

type runJobOut struct {
	Job             string   `json:"job"`
	OK              bool     `json:"ok"`
	Error           string   `json:"error,omitempty"`
	ErrorCode       string   `json:"error_code,omitempty"`
	SnapshotID      string   `json:"snapshot_id,omitempty"`
	PredictedBytes  uint64   `json:"predicted_bytes"`
	AddedBytes      uint64   `json:"added_bytes"`
	ProcessedBytes  uint64   `json:"processed_bytes"`
	FilesNew        uint64   `json:"files_new"`
	FilesChanged    uint64   `json:"files_changed"`
	DurationSeconds float64  `json:"duration_seconds"`
	Warnings        []string `json:"warnings"`
}

func runJSON(res scheduler.Result) runOut {
	out := runOut{Jobs: []runJobOut{}, Errors: []string{}, Warnings: append([]string{}, res.Warnings...)}
	for _, j := range res.Jobs {
		jo := runJobOut{Job: j.Job, OK: j.Err == nil, PredictedBytes: j.Predicted, Warnings: append([]string{}, j.Warnings...)}
		if j.Err != nil {
			jo.Error, jo.ErrorCode = j.Err.Error(), errorCode(j.Err)
		} else {
			b := j.Backup
			jo.SnapshotID, jo.AddedBytes, jo.ProcessedBytes = b.SnapshotID, b.BytesAddedPacked, b.BytesProcessed
			jo.FilesNew, jo.FilesChanged = b.FilesNew, b.FilesChanged
			jo.DurationSeconds = b.End.Sub(b.Start).Seconds()
		}
		out.Jobs = append(out.Jobs, jo)
	}
	for _, e := range res.Errors {
		out.Errors = append(out.Errors, e.Error())
	}
	return out
}
