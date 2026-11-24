package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/WildPino/ouroboros/internal/backend/restic"
	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/lock"
	"github.com/WildPino/ouroboros/internal/platform/windows"
	"github.com/WildPino/ouroboros/internal/scheduler"
	"github.com/WildPino/ouroboros/internal/space"
	"github.com/WildPino/ouroboros/internal/state"
)

// runDeps holds what run needs from the system, replaceable in tests.
type runDeps struct {
	isElevated  func() (bool, error)
	acquire     func() (*lock.Lock, error)
	checkRestic func(ctx context.Context, cfg *config.Config) error
	newBackend  func(cfg *config.Config, configPath string) (*restic.Restic, error)
	hostname    func() (string, error)
}

func defaultRunDeps() runDeps {
	return runDeps{
		isElevated:  windows.IsElevated,
		acquire:     func() (*lock.Lock, error) { return lock.Acquire(lock.Name) },
		checkRestic: checkResticVersion,
		newBackend:  newResticBackend,
		hostname:    os.Hostname,
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
			return runRun(cmd.Context(), opts, ropts, defaultRunDeps())
		},
	}
	cmd.Flags().StringVar(&ropts.Job, "job", "", "run only the job with this name")
	cmd.Flags().BoolVar(&ropts.Force, "force", false, "run even if not due")
	return cmd
}

func runRun(ctx context.Context, opts *options, ropts scheduler.Options, d runDeps) error {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return err
	}
	elevated, err := d.isElevated()
	if err != nil {
		return fmt.Errorf("check privileges: %w", err)
	}
	if !elevated {
		return errors.New("run must be run as Administrator: shadow copies and the password file require it")
	}
	l, err := d.acquire()
	if errors.Is(err, lock.ErrLocked) {
		fmt.Fprintln(opts.stdout, "Another ouroboros process is running; nothing to do.")
		return nil
	}
	if err != nil {
		return err
	}
	defer l.Release()

	if err := d.checkRestic(ctx, cfg); err != nil {
		return err
	}
	b, err := d.newBackend(cfg, opts.configPath)
	if err != nil {
		return err
	}
	host, err := d.hostname()
	if err != nil {
		return err
	}
	r, err := newReclaimer(cfg, b, host)
	if err != nil {
		return err
	}
	log := newLogger(opts.stderr, cfg.Logging.Level)
	r.Log = log
	dir := filepath.Dir(opts.configPath)
	runner := &scheduler.Runner{
		Config:      cfg,
		Backend:     b,
		Space:       r,
		StatePath:   filepath.Join(dir, state.StateFile),
		HistoryPath: filepath.Join(dir, state.HistoryFile),
		Clock:       space.SystemClock{},
		Log:         log,
	}
	res, err := runner.Run(ctx, ropts)
	writeRunResult(opts.stdout, res, ropts)
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

// newLogger writes text logs to w. File logs and the Event Log are added
// by the logging package.
func newLogger(w io.Writer, level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: l}))
}
