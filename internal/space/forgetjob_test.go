package space

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
)

// jobWorld has 150 snapshots of the removed job "old" (the last one pinned,
// the newest ones recent), and snapshots of the configured job "docs".
func jobWorld() *scripted {
	s := &scripted{frees: map[string]uint64{}, total: 1000 * MB, free: 300 * MB}
	for i := range 150 {
		id := fmt.Sprintf("old%03d", i)
		var extra []string
		if i == 149 {
			extra = append(extra, backend.TagPinned)
		}
		sn := snap(id, "old", t0.Add(time.Duration(i)*time.Hour), extra...)
		sn.AddedPacked = MB
		s.snaps = append(s.snaps, sn)
		s.frees[id] = MB / 2
	}
	s.snaps = append(s.snaps, snap("docs1", "docs", t0), snap("docs2", "docs", t0.Add(time.Hour)))
	// Another host's snapshot of the same job name is never touched.
	other := snap("elsewhere", "old", t0)
	other.Hostname = "other"
	s.snaps = append(s.snaps, other)
	return s
}

func TestPlanJobDeletion(t *testing.T) {
	s := jobWorld()
	r := reclaimerFor(s)
	// Everything is recent and within keep_min: the planner would protect
	// it all, the explicit deletion does not.
	r.Policy = Policy{KeepMin: 1000, ProtectNewerThan: 100000 * time.Hour}
	s.snaps = filterHost(s.snaps, "h")
	d, err := r.PlanJobDeletion(context.Background(), "old", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Snapshots) != 149 || len(d.Pinned) != 1 || d.Pinned[0].ID != "old149" || d.EstimatedFreed != 149*MB {
		t.Fatalf("plan: %d snapshots, %d pinned, %d bytes", len(d.Snapshots), len(d.Pinned), d.EstimatedFreed)
	}
	if len(s.forgets) != 0 || len(s.prunes) != 0 {
		t.Fatal("planning changed the repository")
	}

	for job, want := range map[string]error{"docs": ErrJobConfigured, "DOCS": ErrJobConfigured, "nope": ErrNoJobSnapshots} {
		if _, err := r.PlanJobDeletion(context.Background(), job, []string{"docs"}); !errors.Is(err, want) {
			t.Errorf("%s: got %v, want %v", job, err, want)
		}
	}
}

func filterHost(snaps []backend.Snapshot, host string) []backend.Snapshot {
	return slices.DeleteFunc(snaps, func(s backend.Snapshot) bool { return s.Hostname != host })
}

func TestDeleteJob(t *testing.T) {
	s := jobWorld()
	s.snaps = filterHost(s.snaps, "h")
	r := reclaimerFor(s)
	var steps []JobDeletionProgress
	d, rep, err := r.DeleteJob(context.Background(), "old", []string{"docs"}, func(p JobDeletionProgress) {
		steps = append(steps, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Snapshots) != 149 || len(rep.Forgotten) != 149 || rep.Freed() != 149*MB/2 {
		t.Fatalf("forgot %d, freed %d", len(rep.Forgotten), rep.Freed())
	}
	// Two forget batches, then one prune that frees everything at once.
	if want := []string{"forget 100", "forget 49", "prune"}; !slices.Equal(s.events, want) {
		t.Fatalf("events %v, want %v", s.events, want)
	}
	if want := []JobDeletionProgress{{PhaseForget, 0, 149}, {PhaseForget, 100, 149}, {PhasePrune, 149, 149}}; !slices.Equal(steps, want) {
		t.Fatalf("progress %v", steps)
	}
	left := ids(s.snaps)
	if want := []string{"old149", "docs1", "docs2"}; !slices.Equal(left, want) {
		t.Fatalf("left %v, want %v", left, want)
	}

	// Only pinned snapshots left: nothing to do, no prune.
	s.events = nil
	if _, rep, err := r.DeleteJob(context.Background(), "old", nil, nil); err != nil || len(rep.Forgotten) != 0 || len(s.events) != 0 {
		t.Fatalf("pinned only: %v %v %v", rep, err, s.events)
	}
	// A configured job is refused before anything happens.
	if _, _, err := r.DeleteJob(context.Background(), "docs", []string{"docs"}, nil); !errors.Is(err, ErrJobConfigured) || len(s.events) != 0 {
		t.Fatalf("configured job: %v %v", err, s.events)
	}
}

func TestDeleteJobUsesBallast(t *testing.T) {
	s := jobWorld()
	s.snaps = filterHost(s.snaps, "h")
	s.free = 10 * MB // below the 20 MB prune headroom
	s.ballast = 50 * MB
	for id := range s.frees {
		s.frees[id] = MB // 149 MB back, enough to put the ballast back
	}
	r := reclaimerFor(s)
	r.BallastSize = 50 * MB
	_, rep, err := r.DeleteJob(context.Background(), "old", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.BallastDeleted || !rep.BallastRecreated || s.ballast != 50*MB {
		t.Fatalf("ballast deleted %v recreated %v, on disk %d", rep.BallastDeleted, rep.BallastRecreated, s.ballast)
	}
	if want := []string{"forget 100", "forget 49", "ballast delete", "prune", "ballast create"}; !slices.Equal(s.events, want) {
		t.Fatalf("events %v", s.events)
	}
}
