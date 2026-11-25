package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/WildPino/ouroboros/internal/backend"
	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/lock"
	"github.com/WildPino/ouroboros/internal/platform/windows"
	"github.com/WildPino/ouroboros/internal/scheduler"
	"github.com/WildPino/ouroboros/internal/space"
	"github.com/WildPino/ouroboros/internal/state"
)

type statusDeps struct {
	repo       repoDeps
	disk       diskUsager
	repoSize   func(ctx context.Context, dir string) (uint64, error)
	taskExists func(ctx context.Context, name string) (bool, error)
	lockHeld   func() (bool, error)
	now        func() time.Time
}

// statusOptions are the flags of status.
type statusOptions struct {
	noSnapshots bool
}

func newStatusCmd(opts *options) *cobra.Command {
	var sopts statusOptions
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show jobs, last results, space usage and history depth",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd.Context(), opts, sopts, statusDeps{
				repo:       defaultRepoDeps(),
				disk:       platformDisk{},
				repoSize:   space.DirSize,
				taskExists: windows.TaskExists,
				lockHeld:   func() (bool, error) { return lock.Held(lock.Name) },
				now:        time.Now,
			})
		},
	}
	cmd.Flags().BoolVar(&sopts.noSnapshots, "no-snapshots", false, "skip listing snapshots, which reads the repository")
	return supportsJSON(cmd)
}

// jobStatus is the status of one job.
type jobStatus struct {
	name       string
	state      state.JobState
	next       time.Time
	due        bool
	snapshots  int
	predicted  uint64
	hasHistory bool
	knowsSnaps bool
}

// statusReport is everything status shows. Unknown values are reported in
// problems instead of failing the command.
type statusReport struct {
	repoPath    string
	total, free uint64
	diskKnown   bool
	targets     space.Targets
	repoSize    uint64
	sizeKnown   bool
	snapshots   []backend.Snapshot
	snapsKnown  bool
	task        string
	interval    time.Duration
	st          state.State
	jobs        []jobStatus
	running     bool
	runKnown    bool
	progress    *state.Progress
	recent      []state.Record
	problems    []string
}

func runStatus(ctx context.Context, opts *options, sopts statusOptions, d statusDeps) error {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return err
	}
	rep := gatherStatus(ctx, opts, sopts, cfg, d)
	if opts.out != nil {
		opts.out.result(statusJSON(rep))
	} else {
		writeStatus(opts.stdout, rep, d.now())
	}
	if len(rep.problems) > 0 {
		for _, p := range rep.problems {
			fmt.Fprintln(opts.stderr, "warning:", p)
		}
		return &codedError{code: exitWarnings}
	}
	return nil
}

func gatherStatus(ctx context.Context, opts *options, sopts statusOptions, cfg *config.Config, d statusDeps) statusReport {
	rep := statusReport{repoPath: cfg.Repository.Path, interval: cfg.Schedule.Tick.Std()}
	problem := func(format string, a ...any) { rep.problems = append(rep.problems, fmt.Sprintf(format, a...)) }

	if total, free, err := d.disk.DiskUsage(cfg.Repository.Path); err != nil {
		problem("cannot measure the repository partition: %v", err)
	} else {
		rep.total, rep.free, rep.diskKnown = total, free, true
		rep.targets = space.NewTargets(cfg.Space, total)
	}
	if size, err := d.repoSize(ctx, cfg.Repository.Path); err != nil {
		problem("cannot measure the repository: %v", err)
	} else {
		rep.repoSize, rep.sizeKnown = size, true
	}

	switch ok, err := d.taskExists(ctx, windows.TaskName); {
	case err != nil:
		rep.task = "unknown"
		problem("cannot query the scheduled task: %v", err)
	case ok:
		rep.task = "installed"
	default:
		rep.task = "not installed (run \"ouroboros install\")"
	}

	if held, err := d.lockHeld(); err != nil {
		problem("cannot tell whether a run is in progress: %v", err)
	} else {
		rep.running, rep.runKnown = held, true
	}

	dir := filepath.Dir(opts.configPath)
	st, err := state.Load(filepath.Join(dir, state.StateFile))
	if err != nil {
		problem("%v", err)
	}
	rep.st = st
	history, _, err := state.ReadHistory(filepath.Join(dir, state.HistoryFile))
	if err != nil {
		problem("cannot read the history: %v", err)
	}
	rep.recent = recentProblems(history, recentProblemsMax)
	// A run that died leaves progress.json behind: only trust it while the
	// lock is held.
	if rep.running {
		if p, ok, err := state.LoadProgress(filepath.Join(dir, state.ProgressFile)); err == nil && ok {
			rep.progress = &p
		}
	}

	// Snapshots need the repository password, which only administrators
	// can read.
	if sopts.noSnapshots {
		// Not a problem: the caller asked for the quick answer.
	} else if elevated, err := d.repo.isElevated(); err == nil && elevated {
		if s, err := openRepo(ctx, opts, d.repo, "status", false); err != nil {
			problem("cannot open the repository: %v", err)
		} else {
			snaps, err := s.backend.Snapshots(ctx, backend.SnapshotFilter{Host: s.host, Tags: []string{backend.TagOuroboros}})
			s.release()
			if err != nil {
				problem("cannot list snapshots: %v", err)
			} else {
				rep.snapshots, rep.snapsKnown = snaps, true
			}
		}
	} else {
		problem("snapshots are only shown to administrators")
	}

	now := d.now()
	for _, j := range cfg.Jobs {
		js := st.Jobs[j.Name]
		g, ok := space.PredictGrowth(state.BackupGrowth(history, j.Name), cfg.Space.GrowthFactor)
		jst := jobStatus{
			name: j.Name, state: js,
			next:      scheduler.NextRun(js, j.Interval.Std(), cfg.Schedule.RetryAfter.Std()),
			due:       scheduler.JobDue(js, j.Interval.Std(), cfg.Schedule.RetryAfter.Std(), now),
			predicted: g, hasHistory: ok, knowsSnaps: rep.snapsKnown,
		}
		for _, sn := range rep.snapshots {
			if strings.EqualFold(sn.Job(), j.Name) {
				jst.snapshots++
			}
		}
		rep.jobs = append(rep.jobs, jst)
	}
	return rep
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Local().Format("2006-01-02 15:04")
}

func writeStatus(w io.Writer, r statusReport, now time.Time) {
	if r.diskKnown {
		fmt.Fprintf(w, "Partition:       %s of %s free (target at least %s: min_free %s + prune headroom %s)\n",
			formatBytes(r.free), formatBytes(r.total), formatBytes(r.targets.RequiredFree(0)),
			formatBytes(r.targets.MinFree), formatBytes(r.targets.PruneHeadroom))
	} else {
		fmt.Fprintln(w, "Partition:       unknown")
	}
	repo := r.repoPath
	if r.sizeKnown {
		repo += ", " + formatBytes(r.repoSize)
	}
	if r.targets.MaxRepoSize > 0 {
		repo += " (max_repo_size " + formatBytes(r.targets.MaxRepoSize) + ")"
	}
	fmt.Fprintf(w, "Repository:      %s\n", repo)
	if r.snapsKnown {
		if len(r.snapshots) == 0 {
			fmt.Fprintln(w, "History:         no snapshots yet")
		} else {
			oldest := r.snapshots[0].Time
			days := int(now.Sub(oldest).Hours() / 24)
			fmt.Fprintf(w, "History:         %d snapshots, oldest %s (%d days)\n", len(r.snapshots), formatTime(oldest), days)
		}
	}
	fmt.Fprintf(w, "Scheduled task:  %s, every %s\n", r.task, shortDuration(r.interval))
	if r.running {
		fmt.Fprintln(w, "Run:             in progress")
	}
	fmt.Fprintf(w, "Maintenance:     last prune %s, last check %s, last read-data check %s\n",
		formatTime(r.st.LastPrune), formatTime(r.st.LastCheck), formatTime(r.st.LastReadDataCheck))

	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "Job\tLast success\tLast attempt\tNext run\tSnapshots\tPredicted growth")
	for _, j := range r.jobs {
		attempt := "-"
		if !j.state.LastAttempt.IsZero() {
			attempt = "ok"
			if j.state.Failed() {
				attempt = "FAILED " + formatTime(j.state.LastAttempt)
			}
		}
		next := formatTime(j.next)
		if j.due {
			next = "due now"
		}
		snaps := "?"
		if j.knowsSnaps {
			snaps = fmt.Sprint(j.snapshots)
		}
		growth := "after first backup"
		if j.hasHistory {
			growth = formatBytes(j.predicted)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", j.name, formatTime(j.state.LastSuccess), attempt, next, snaps, growth)
	}
	tw.Flush()
	for _, j := range r.jobs {
		if j.state.Failed() && j.state.LastError != "" {
			fmt.Fprintf(w, "\n%s: %s\n", j.name, j.state.LastError)
		}
	}
}

// shortDuration drops zero trailing units: "15m" instead of "15m0s".
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// recentProblemsMax bounds the failures and warnings status reports.
const recentProblemsMax = 50

// recentProblems returns the newest history records that failed or ended
// with warnings, newest first.
func recentProblems(history []state.Record, max int) []state.Record {
	var out []state.Record
	for i := len(history) - 1; i >= 0 && len(out) < max; i-- {
		r := history[i]
		if !r.OK || r.Warnings > 0 || r.Incomplete {
			out = append(out, r)
		}
	}
	return out
}
