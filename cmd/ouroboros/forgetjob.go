package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/WildPino/ouroboros/internal/space"
	"github.com/WildPino/ouroboros/internal/state"
)

var errConfirmMismatch = errors.New("deletion not confirmed")

type forgetJobOptions struct {
	dryRun  bool
	confirm string
}

func newForgetJobCmd(opts *options) *cobra.Command {
	var fo forgetJobOptions
	cmd := &cobra.Command{
		Use:   "forget-job NAME (--dry-run | --confirm NAME)",
		Short: "Delete the backups of a job removed from the configuration",
		Long: "Delete every snapshot of a job that is no longer in the configuration, then prune\n" +
			"so that the space is free. Pinned snapshots are kept; unpin them first to delete\n" +
			"them too. A job still in the configuration is refused.\n\n" +
			"--dry-run lists what would be deleted. To delete, repeat the job name with\n" +
			"--confirm. Must be run as Administrator, and not during a backup.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runForgetJob(cmd.Context(), opts, defaultRepoDeps(), args[0], fo)
		},
	}
	cmd.Flags().BoolVar(&fo.dryRun, "dry-run", false, "only list what would be deleted")
	cmd.Flags().StringVar(&fo.confirm, "confirm", "", "the job name again, to confirm the deletion")
	return supportsJSON(cmd)
}

func runForgetJob(ctx context.Context, opts *options, d repoDeps, job string, fo forgetJobOptions) error {
	if !fo.dryRun && fo.confirm != job {
		return fmt.Errorf("%w: to delete the backups of %q, repeat the job name with --confirm %s, "+
			"or list them first with --dry-run", errConfirmMismatch, job, job)
	}
	var (
		s   *repoSession
		err error
	)
	if fo.dryRun {
		// Listing only reads, so it works during a run.
		s, err = openRepo(ctx, opts, d, "forget-job", false)
	} else {
		s, err = openRepoExclusive(ctx, opts, d, "forget-job")
	}
	if err != nil {
		return err
	}
	defer s.release()
	r, err := newReclaimer(s.cfg, s.backend, s.host)
	if err != nil {
		return err
	}
	configured := make([]string, len(s.cfg.Jobs))
	for i, j := range s.cfg.Jobs {
		configured[i] = j.Name
	}

	if fo.dryRun {
		plan, err := r.PlanJobDeletion(ctx, job, configured)
		if err != nil {
			return err
		}
		if opts.out != nil {
			opts.out.result(forgetJobPlanOut{Job: job, Snapshots: snapshotList(plan.Snapshots),
				Pinned: snapshotList(plan.Pinned), EstimatedFreedBytes: plan.EstimatedFreed})
			return nil
		}
		writeJobDeletionPlan(opts, plan)
		return nil
	}

	log, closeLog := openLog(s.cfg, opts.configPath, opts.stderr, d.openEvents)
	defer closeLog()
	r.Log = log
	start := time.Now()
	progress := func(p space.JobDeletionProgress) {
		if opts.out != nil {
			opts.out.event("delete", forgetJobProgressOut{Phase: p.Phase, Forgotten: p.Forgotten, Total: p.Total})
		}
	}
	plan, rep, err := r.DeleteJob(ctx, job, configured, progress)
	rec := state.Record{Time: time.Now(), Kind: state.KindJobDeletion, Job: job, OK: err == nil,
		Duration: time.Since(start).Seconds(), Forgotten: len(rep.Forgotten), Freed: rep.Freed()}
	if err != nil {
		rec.Error = err.Error()
	}
	// A refusal before anything was deleted is not an event worth keeping.
	if len(rep.Forgotten) > 0 || (err != nil && !errors.Is(err, space.ErrJobConfigured) && !errors.Is(err, space.ErrNoJobSnapshots)) {
		if aerr := state.Append(filepath.Join(filepath.Dir(opts.configPath), state.HistoryFile), rec); aerr != nil {
			log.Warn("cannot append to history", "err", aerr)
		}
	}
	if err != nil {
		return err
	}
	if opts.out != nil {
		opts.out.result(forgetJobOut{Job: job, Forgotten: len(rep.Forgotten), PinnedKept: len(plan.Pinned), FreedBytes: rep.Freed()})
		return nil
	}
	fmt.Fprintf(opts.stdout, "Deleted %d snapshots of job %q; %s freed.\n", len(rep.Forgotten), job, formatBytes(rep.Freed()))
	if len(plan.Pinned) > 0 {
		fmt.Fprintf(opts.stdout, "Kept %d pinned snapshots.\n", len(plan.Pinned))
	}
	return nil
}

func writeJobDeletionPlan(opts *options, plan space.JobDeletion) {
	w := opts.stdout
	if len(plan.Snapshots) == 0 {
		fmt.Fprintf(w, "Job %q has only pinned snapshots; nothing would be deleted.\n", plan.Job)
	} else {
		fmt.Fprintf(w, "Deleting the backups of job %q would remove %d snapshots and free about %s:\n",
			plan.Job, len(plan.Snapshots), formatBytes(plan.EstimatedFreed))
		for _, sn := range plan.Snapshots {
			fmt.Fprintf(w, "  %s  %s\n", sn.Time.Local().Format(time.DateTime), shortID(sn.ID))
		}
	}
	if len(plan.Pinned) > 0 {
		fmt.Fprintf(w, "Kept: %d pinned snapshots (unpin them first to delete them too).\n", len(plan.Pinned))
	}
	if len(plan.Snapshots) > 0 {
		fmt.Fprintln(w, "The space is an estimate: data shared with other snapshots stays.")
		fmt.Fprintf(w, "To delete them: ouroboros forget-job %s --confirm %s\n", plan.Job, plan.Job)
	}
}

// Schema of "forget-job --json"; documented in docs/JSON.md.
type forgetJobPlanOut struct {
	Job                 string        `json:"job"`
	Snapshots           []snapshotOut `json:"snapshots"`
	Pinned              []snapshotOut `json:"pinned"`
	EstimatedFreedBytes uint64        `json:"estimated_freed_bytes"`
}

type forgetJobProgressOut struct {
	Phase     string `json:"phase"`
	Forgotten int    `json:"forgotten"`
	Total     int    `json:"total"`
}

type forgetJobOut struct {
	Job        string `json:"job"`
	Forgotten  int    `json:"forgotten"`
	PinnedKept int    `json:"pinned_kept"`
	FreedBytes uint64 `json:"freed_bytes"`
}
