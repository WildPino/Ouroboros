package space

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/WildPino/ouroboros/internal/backend"
)

// ErrJobConfigured refuses to delete the backups of a job that is still in
// the configuration: only a removed job's backups can be deleted on request
// (DESIGN.md §12.5).
var ErrJobConfigured = errors.New("the job is still in the configuration; remove it first")

// ErrNoJobSnapshots means the repository has no snapshot of the job.
var ErrNoJobSnapshots = errors.New("no snapshots of this job")

// JobDeletion lists what deleting a removed job's backups does.
type JobDeletion struct {
	Job string
	// Snapshots are forgotten, oldest first. The newest and recent
	// snapshots are included: the user asked for all of them.
	Snapshots []backend.Snapshot
	// Pinned snapshots are kept; unpinning them first deletes them too.
	Pinned []backend.Snapshot
	// EstimatedFreed adds up the data each snapshot stored when it was
	// taken. Data shared with snapshots that stay is not freed, so the real
	// figure, known after pruning, can be lower.
	EstimatedFreed uint64
}

// Phases reported while deleting a job's backups.
const (
	PhaseForget = "forget"
	PhasePrune  = "prune"
)

// JobDeletionProgress is reported before each step of DeleteJob.
type JobDeletionProgress struct {
	Phase     string
	Forgotten int // snapshots forgotten so far
	Total     int
}

// PlanJobDeletion lists the snapshots DeleteJob would remove for job,
// without changing anything. configured holds the job names of the current
// configuration.
func (r *Reclaimer) PlanJobDeletion(ctx context.Context, job string, configured []string) (JobDeletion, error) {
	d := JobDeletion{Job: job}
	if job == "" {
		return d, errors.New("no job name given")
	}
	if slices.ContainsFunc(configured, func(c string) bool { return strings.EqualFold(c, job) }) {
		return d, fmt.Errorf("%w: %s", ErrJobConfigured, job)
	}
	snaps, err := r.Repo.Snapshots(ctx, backend.SnapshotFilter{Host: r.Host, Tags: []string{backend.TagOuroboros}})
	if err != nil {
		return d, fmt.Errorf("list snapshots: %w", err)
	}
	for _, s := range snaps {
		switch {
		case s.Job() != job:
		case s.Pinned():
			d.Pinned = append(d.Pinned, s)
		default:
			d.Snapshots = append(d.Snapshots, s)
			d.EstimatedFreed += s.AddedPacked
		}
	}
	if len(d.Snapshots) == 0 && len(d.Pinned) == 0 {
		return d, fmt.Errorf("%w: %s", ErrNoJobSnapshots, job)
	}
	return d, nil
}

// DeleteJob forgets every unpinned snapshot of a removed job and prunes, so
// the space is free when it returns. It plans again rather than taking a
// plan, so that it acts on the repository as it is now; the caller must
// hold the run lock. It is the only deletion that the space rules do not
// decide, and it happens only on the user's explicit request.
func (r *Reclaimer) DeleteJob(ctx context.Context, job string, configured []string,
	progress func(JobDeletionProgress)) (d JobDeletion, rep Report, err error) {
	if progress == nil {
		progress = func(JobDeletionProgress) {}
	}
	d, err = r.PlanJobDeletion(ctx, job, configured)
	if err != nil {
		return d, rep, err
	}
	m, err := r.measure(ctx)
	if err != nil {
		return d, rep, err
	}
	rep.FreeBefore, rep.FreeAfter = m.usage.Free, m.usage.Free
	if len(d.Snapshots) == 0 {
		return d, rep, nil
	}
	defer func() {
		if rep.BallastDeleted {
			rep.BallastRecreated = r.restoreBallast(ctx, r.goal(0), &rep)
		}
	}()

	log := r.log()
	log.Warn("deleting the backups of a removed job on request", "job", job,
		"snapshots", len(d.Snapshots), "pinned_kept", len(d.Pinned))
	total := len(d.Snapshots)
	for chunk := range slices.Chunk(d.Snapshots, forgetChunk) {
		progress(JobDeletionProgress{Phase: PhaseForget, Forgotten: len(rep.Forgotten), Total: total})
		ids := make([]string, len(chunk))
		for i, s := range chunk {
			ids[i] = s.ID
		}
		if err := r.Repo.Forget(ctx, ids); err != nil {
			return d, rep, fmt.Errorf("forget: %w", err)
		}
		rep.Forgotten = append(rep.Forgotten, chunk...)
		r.prunePending = true
		log.Info("forgot snapshots", "job", job, "count", len(ids), "ids", ids)
	}
	progress(JobDeletionProgress{Phase: PhasePrune, Forgotten: len(rep.Forgotten), Total: total})
	if err := r.prune(ctx, &rep, "0"); err != nil {
		return d, rep, err
	}
	if m, err = r.measure(ctx); err != nil {
		return d, rep, err
	}
	rep.FreeAfter = m.usage.Free
	log.Info("deleted the backups of a removed job", "job", job, "forgotten", len(rep.Forgotten), "freed", rep.Freed())
	return d, rep, nil
}
