package space

import (
	"slices"
	"strings"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
)

// Policy lists what must never be deleted automatically (DESIGN.md §3.2).
type Policy struct {
	KeepMin          int           // newest snapshots kept per job
	ProtectNewerThan time.Duration // snapshots younger than this are kept
}

// Reasons a snapshot is protected.
const (
	ReasonNewest = "newest of its job"
	ReasonRecent = "recent"
	ReasonPinned = "pinned"
)

// Protected is a snapshot excluded from deletion, with the reason.
type Protected struct {
	backend.Snapshot
	Reason string
}

// Selection splits snapshots into deletion candidates, oldest first, and
// protected ones.
type Selection struct {
	Candidates []backend.Snapshot
	Protected  []Protected
}

// SelectCandidates applies the policy at time now. Snapshots are grouped by
// job tag; snapshots without one form their own group.
func SelectCandidates(snaps []backend.Snapshot, p Policy, now time.Time) Selection {
	sorted := slices.Clone(snaps)
	slices.SortStableFunc(sorted, oldestFirst)

	// Newest KeepMin of each job: walk from the newest end.
	newest := make(map[string]bool)
	perJob := make(map[string]int)
	for i := len(sorted) - 1; i >= 0; i-- {
		job := sorted[i].Job()
		if perJob[job] < p.KeepMin {
			perJob[job]++
			newest[sorted[i].ID] = true
		}
	}

	var sel Selection
	for _, s := range sorted {
		switch {
		case newest[s.ID]:
			sel.Protected = append(sel.Protected, Protected{s, ReasonNewest})
		case s.Pinned():
			sel.Protected = append(sel.Protected, Protected{s, ReasonPinned})
		case p.ProtectNewerThan > 0 && now.Sub(s.Time) < p.ProtectNewerThan:
			sel.Protected = append(sel.Protected, Protected{s, ReasonRecent})
		default:
			sel.Candidates = append(sel.Candidates, s)
		}
	}
	return sel
}

// oldestFirst orders by snapshot time, ties broken by ID (DESIGN.md §3.5).
func oldestFirst(a, b backend.Snapshot) int {
	if c := a.Time.Compare(b.Time); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}
