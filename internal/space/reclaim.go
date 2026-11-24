package space

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/WildPino/ouroboros/internal/backend"
)

// maxBatch caps the number of snapshots forgotten before each prune.
const maxBatch = 16

// ErrCannotReclaim means every deletable snapshot is gone and the space goal
// is still not met. Protected snapshots are never deleted to make room.
var ErrCannotReclaim = errors.New("cannot reclaim enough space")

// CannotReclaimError details an ErrCannotReclaim.
type CannotReclaimError struct {
	Freed   uint64 // bytes freed before giving up
	Missing uint64 // bytes of free space still missing
}

func (e *CannotReclaimError) Error() string {
	return fmt.Sprintf("%v: freed %d bytes, still missing %d bytes; only protected snapshots are left",
		ErrCannotReclaim, e.Freed, e.Missing)
}

// Is makes errors.Is(err, ErrCannotReclaim) true.
func (e *CannotReclaimError) Is(target error) bool { return target == ErrCannotReclaim }

// Repository is the part of the backend that reclaiming uses.
type Repository interface {
	Snapshots(ctx context.Context, filter backend.SnapshotFilter) ([]backend.Snapshot, error)
	Forget(ctx context.Context, ids []string) error
	Prune(ctx context.Context, opts backend.PruneOptions) (backend.PruneResult, error)
}

// Reclaimer frees space on the repository partition by forgetting the oldest
// deletable snapshots (DESIGN.md §3.5). It is the only code that deletes
// snapshots automatically.
type Reclaimer struct {
	Repo        Repository
	Disk        FreeSpaceProvider
	Sizer       RepoSizer
	Clock       Clock
	RepoPath    string
	Host        string // only this host's snapshots are considered
	Policy      Policy
	Targets     Targets
	Ballast     Ballast
	BallastSize uint64 // 0 = ballast disabled
	Log         *slog.Logger

	// prunePending is set when snapshots were forgotten without a prune
	// afterwards, so their space is not free yet.
	prunePending bool
}

// Report describes what a reclaim did.
type Report struct {
	FreeBefore       uint64
	FreeAfter        uint64
	Forgotten        []backend.Snapshot
	Prunes           int
	BallastDeleted   bool
	BallastRecreated bool
}

// Freed returns the free space gained.
func (r Report) Freed() uint64 {
	if r.FreeAfter > r.FreeBefore {
		return r.FreeAfter - r.FreeBefore
	}
	return 0
}

// goal is the state a reclaim must reach for a backup of the given growth.
type goal struct {
	needFree uint64
	maxRepo  uint64
	growth   uint64
}

func (r *Reclaimer) goal(growth uint64) goal {
	return goal{needFree: r.Targets.RequiredFree(growth), maxRepo: r.Targets.MaxRepoSize, growth: growth}
}

// shortfall returns the free space missing to meet g, and whether
// max_repo_size is exceeded.
func (g goal) shortfall(free, repoSize uint64) (missing uint64, overCap bool) {
	if free < g.needFree {
		missing = g.needFree - free
	}
	overCap = g.maxRepo > 0 && addSat(repoSize, g.growth) > g.maxRepo
	return missing, overCap
}

func (r *Reclaimer) log() *slog.Logger {
	if r.Log == nil {
		return slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return r.Log
}

type measurement struct {
	usage    Usage
	repoSize uint64
}

func (r *Reclaimer) measure(ctx context.Context) (measurement, error) {
	u, err := r.Disk.Usage(r.RepoPath)
	if err != nil {
		return measurement{}, fmt.Errorf("measure free space: %w", err)
	}
	m := measurement{usage: u}
	if r.Targets.MaxRepoSize > 0 {
		if m.repoSize, err = r.Sizer.RepoSize(ctx, r.RepoPath); err != nil {
			return measurement{}, err
		}
	}
	return m, nil
}

// select lists this host's Ouroboros snapshots and applies the policy.
func (r *Reclaimer) selection(ctx context.Context) (Selection, error) {
	snaps, err := r.Repo.Snapshots(ctx, backend.SnapshotFilter{Host: r.Host, Tags: []string{backend.TagOuroboros}})
	if err != nil {
		return Selection{}, fmt.Errorf("list snapshots: %w", err)
	}
	return SelectCandidates(snaps, r.Policy, r.Clock.Now()), nil
}

// Reclaim makes room for a backup expected to add growth bytes. With
// growth 0 it restores min_free and prune_headroom after a backup.
func (r *Reclaimer) Reclaim(ctx context.Context, growth uint64) (Report, error) {
	return r.reclaim(ctx, growth, false)
}

func (r *Reclaimer) reclaim(ctx context.Context, growth uint64, ballastGone bool) (rep Report, err error) {
	g := r.goal(growth)
	m, err := r.measure(ctx)
	if err != nil {
		return rep, err
	}
	rep.FreeBefore, rep.FreeAfter = m.usage.Free, m.usage.Free
	rep.BallastDeleted = ballastGone

	defer func() {
		if rep.BallastDeleted {
			rep.BallastRecreated = r.restoreBallast(ctx, g, &rep)
		}
	}()

	if missing, over := g.shortfall(m.usage.Free, m.repoSize); missing == 0 && !over {
		return rep, nil
	}

	if r.prunePending {
		// Snapshots already forgotten (by retention) still hold their
		// space; free it before deleting more history.
		if err := r.prune(ctx, &rep, "0"); err != nil {
			return rep, err
		}
		if m, err = r.measure(ctx); err != nil {
			return rep, err
		}
		rep.FreeAfter = m.usage.Free
		if missing, over := g.shortfall(m.usage.Free, m.repoSize); missing == 0 && !over {
			return rep, nil
		}
	}

	sel, err := r.selection(ctx)
	if err != nil {
		return rep, err
	}
	candidates := sel.Candidates
	log := r.log()
	log.Info("reclaiming space", "free", m.usage.Free, "need", g.needFree,
		"candidates", len(candidates), "protected", len(sel.Protected))

	batch := 1
	for {
		if len(candidates) == 0 {
			missing, _ := g.shortfall(rep.FreeAfter, m.repoSize)
			return rep, &CannotReclaimError{Freed: rep.Freed(), Missing: missing}
		}
		n := min(batch, len(candidates))
		victims := candidates[:n]
		candidates = candidates[n:]

		ids := make([]string, len(victims))
		for i, s := range victims {
			ids[i] = s.ID
		}
		if err := r.Repo.Forget(ctx, ids); err != nil {
			return rep, fmt.Errorf("forget: %w", err)
		}
		rep.Forgotten = append(rep.Forgotten, victims...)
		log.Info("forgot snapshots", "count", len(ids), "ids", ids)

		if err := r.prune(ctx, &rep, "0"); err != nil {
			return rep, err
		}

		if m, err = r.measure(ctx); err != nil {
			return rep, err
		}
		rep.FreeAfter = m.usage.Free
		if missing, over := g.shortfall(m.usage.Free, m.repoSize); missing == 0 && !over {
			log.Info("space reclaimed", "freed", rep.Freed(), "free", rep.FreeAfter)
			return rep, nil
		}
		batch = min(batch*2, maxBatch)
	}
}

// prune runs restic prune, first deleting the ballast if prune would lack
// room to write.
func (r *Reclaimer) prune(ctx context.Context, rep *Report, maxUnused string) error {
	if err := r.ensurePruneRoom(ctx, rep); err != nil {
		return err
	}
	if _, err := r.Repo.Prune(ctx, backend.PruneOptions{MaxUnused: maxUnused}); err != nil {
		return fmt.Errorf("prune: %w", err)
	}
	r.prunePending = false
	rep.Prunes++
	return nil
}

// RoutinePrune is the periodic prune without space pressure
// (DESIGN.md §3.6), which leaves up to maxUnused of unused space to avoid
// heavy repacking.
func (r *Reclaimer) RoutinePrune(ctx context.Context, maxUnused string) (Report, error) {
	var rep Report
	err := r.prune(ctx, &rep, maxUnused)
	if rep.BallastDeleted {
		rep.BallastRecreated = r.restoreBallast(ctx, r.goal(0), &rep)
	}
	return rep, err
}

// ensurePruneRoom deletes the ballast when free space is below the headroom
// prune needs to write repacked data.
func (r *Reclaimer) ensurePruneRoom(ctx context.Context, rep *Report) error {
	if rep.BallastDeleted || r.Ballast == nil {
		return nil
	}
	m, err := r.measure(ctx)
	if err != nil {
		return err
	}
	if m.usage.Free >= r.Targets.PruneHeadroom {
		return nil
	}
	freed, err := r.Ballast.Delete()
	if err != nil {
		return fmt.Errorf("delete ballast: %w", err)
	}
	if freed > 0 {
		rep.BallastDeleted = true
		r.log().Warn("deleted ballast to let prune run", "freed", freed)
	}
	return nil
}

// restoreBallast recreates the ballast if it still leaves the goal met.
func (r *Reclaimer) restoreBallast(ctx context.Context, g goal, rep *Report) bool {
	if r.Ballast == nil || r.BallastSize == 0 {
		return false
	}
	m, err := r.measure(ctx)
	if err != nil {
		return false
	}
	rep.FreeAfter = m.usage.Free
	if m.usage.Free < r.BallastSize || m.usage.Free-r.BallastSize < g.needFree {
		r.log().Warn("ballast not recreated: not enough free space", "free", m.usage.Free, "ballast", r.BallastSize)
		return false
	}
	if err := r.Ballast.Create(r.BallastSize); err != nil {
		r.log().Warn("ballast not recreated", "err", err)
		return false
	}
	if m, err := r.measure(ctx); err == nil {
		rep.FreeAfter = m.usage.Free
	}
	return true
}

// Plan reports what Reclaim would do for growth without changing anything:
// the space missing and the deletion order (for "reclaim --dry-run").
func (r *Reclaimer) Plan(ctx context.Context, growth uint64) (PlanReport, error) {
	m, err := r.measure(ctx)
	if err != nil {
		return PlanReport{}, err
	}
	sel, err := r.selection(ctx)
	if err != nil {
		return PlanReport{}, err
	}
	g := r.goal(growth)
	missing, over := g.shortfall(m.usage.Free, m.repoSize)
	return PlanReport{
		Usage: m.usage, RepoSize: m.repoSize, NeedFree: g.needFree,
		Missing: missing, OverMaxRepoSize: over, Selection: sel,
	}, nil
}

// PlanReport is the result of Plan.
type PlanReport struct {
	Usage           Usage
	RepoSize        uint64 // measured only when max_repo_size is set
	NeedFree        uint64
	Missing         uint64
	OverMaxRepoSize bool
	Selection       Selection
}

// DiskFull reports whether a failed backup is explained by a full disk: free
// space below the prune headroom right after the failure. Measuring works
// in every Windows language, unlike matching restic's error text.
func (r *Reclaimer) DiskFull() (bool, error) {
	u, err := r.Disk.Usage(r.RepoPath)
	if err != nil {
		return false, err
	}
	return u.Free < r.Targets.PruneHeadroom, nil
}

// RecoverFromFullDisk runs DESIGN.md §3.9 steps 1-3 after a backup failed on
// a full disk: delete the ballast, prune the packs left by the interrupted
// backup, then reclaim for twice the predicted growth. The caller retries
// the backup once.
func (r *Reclaimer) RecoverFromFullDisk(ctx context.Context, predicted uint64) (Report, error) {
	ballastGone := false
	if r.Ballast != nil {
		freed, err := r.Ballast.Delete()
		if err != nil {
			return Report{}, fmt.Errorf("delete ballast: %w", err)
		}
		ballastGone = freed > 0
	}
	if _, err := r.Repo.Prune(ctx, backend.PruneOptions{}); err != nil {
		return Report{BallastDeleted: ballastGone}, fmt.Errorf("prune after full disk: %w", err)
	}
	r.prunePending = false
	rep, err := r.reclaim(ctx, addSat(predicted, predicted), ballastGone)
	rep.Prunes++
	return rep, err
}
