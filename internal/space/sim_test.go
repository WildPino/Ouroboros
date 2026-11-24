package space

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
)

// The simulation models a deduplicating repository on a fixed-size disk and
// runs the space manager over many simulated days of random data churn,
// checking the invariants of DESIGN.md §11 on every step.

var errSimDiskFull = errors.New("simulated disk full")

type simSnapshot struct {
	backend.Snapshot
	chunks []int
}

type simJob struct {
	name    string
	chunks  []int   // current dataset
	churn   float64 // fraction of chunks replaced between backups
	growth  float64 // fractional dataset growth per backup
	history []uint64
}

type simWorld struct {
	t       *testing.T
	rng     *rand.Rand
	now     time.Time
	total   uint64
	other   uint64         // space used on the partition by other files
	sizes   map[int]uint64 // chunk id -> size, for every chunk ever created
	stored  map[int]bool   // chunks present in the repository
	snaps   []simSnapshot
	ballast uint64
	policy  Policy
	nextID  int
	snapSeq uint32
	busy    bool

	forgotten, prunes, fullDisks int
}

func (w *simWorld) enter(op string) func() {
	if w.busy {
		w.t.Fatalf("invariant 4 violated: %s started while another repository operation was running", op)
	}
	w.busy = true
	return func() { w.busy = false }
}

func (w *simWorld) repoBytes() uint64 {
	var n uint64
	for id := range w.stored {
		n += w.sizes[id]
	}
	return n
}

func (w *simWorld) free() uint64 {
	used := w.other + w.repoBytes() + w.ballast
	if used >= w.total {
		return 0
	}
	return w.total - used
}

func (w *simWorld) Usage(string) (Usage, error) { return Usage{Total: w.total, Free: w.free()}, nil }

func (w *simWorld) RepoSize(context.Context, string) (uint64, error) { return w.repoBytes(), nil }

func (w *simWorld) Now() time.Time { return w.now }

func (w *simWorld) Exists() (bool, error) { return w.ballast > 0, nil }

func (w *simWorld) Create(size uint64) error {
	if size > w.free() {
		return errSimDiskFull
	}
	w.ballast = size
	return nil
}

func (w *simWorld) Delete() (uint64, error) {
	n := w.ballast
	w.ballast = 0
	return n, nil
}

func (w *simWorld) list() []backend.Snapshot {
	out := make([]backend.Snapshot, len(w.snaps))
	for i, s := range w.snaps {
		out[i] = s.Snapshot
	}
	return out
}

func (w *simWorld) Snapshots(_ context.Context, f backend.SnapshotFilter) ([]backend.Snapshot, error) {
	defer w.enter("snapshots")()
	var out []backend.Snapshot
	for _, s := range w.list() {
		if f.Host != "" && s.Hostname != f.Host {
			continue
		}
		ok := true
		for _, tag := range f.Tags {
			ok = ok && slices.Contains(s.Tags, tag)
		}
		if ok {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, oldestFirst)
	return out, nil
}

// Forget checks invariants 2 and 3: the IDs must be exactly the oldest
// deletion candidates under the policy at this moment.
func (w *simWorld) Forget(_ context.Context, forget []string) error {
	defer w.enter("forget")()
	sel := SelectCandidates(w.list(), w.policy, w.now)
	protected := map[string]string{}
	for _, p := range sel.Protected {
		protected[p.ID] = p.Reason
	}
	for i, id := range forget {
		if r, ok := protected[id]; ok {
			w.t.Fatalf("invariant 2 violated: forgetting protected snapshot %s (%s)", id, r)
		}
		if i >= len(sel.Candidates) || sel.Candidates[i].ID != id {
			w.t.Fatalf("invariant 3 violated: forget %v is not the oldest candidates %v", forget, ids(sel.Candidates[:min(len(forget), len(sel.Candidates))]))
		}
	}
	w.snaps = slices.DeleteFunc(w.snaps, func(s simSnapshot) bool { return slices.Contains(forget, s.ID) })
	w.forgotten += len(forget)
	return nil
}

// Prune drops every chunk no snapshot references, including the garbage of
// an interrupted backup.
func (w *simWorld) Prune(context.Context, backend.PruneOptions) (backend.PruneResult, error) {
	defer w.enter("prune")()
	live := map[int]bool{}
	for _, s := range w.snaps {
		for _, c := range s.chunks {
			live[c] = true
		}
	}
	for id := range w.stored {
		if !live[id] {
			delete(w.stored, id)
		}
	}
	w.prunes++
	return backend.PruneResult{}, nil
}

func (w *simWorld) newChunk() int {
	w.nextID++
	// Sizes vary widely, like files of different kinds.
	w.sizes[w.nextID] = uint64(1 + w.rng.IntN(40))
	return w.nextID
}

// mutate produces the job's next dataset: some chunks replaced, some added,
// occasionally one taken from another job (cross-job deduplication).
func (w *simWorld) mutate(j *simJob, others []*simJob) {
	next := slices.Clone(j.chunks)
	for i := range next {
		if w.rng.Float64() < j.churn {
			next[i] = w.newChunk()
		}
	}
	add := int(float64(len(next))*j.growth) + w.rng.IntN(2)
	for range add {
		if len(others) > 0 && w.rng.IntN(10) == 0 {
			o := others[w.rng.IntN(len(others))]
			if len(o.chunks) > 0 {
				next = append(next, o.chunks[w.rng.IntN(len(o.chunks))])
				continue
			}
		}
		next = append(next, w.newChunk())
	}
	j.chunks = next
}

func (w *simWorld) missing(chunks []int) (ids []int, bytes uint64) {
	seen := map[int]bool{}
	for _, c := range chunks {
		if !w.stored[c] && !seen[c] {
			seen[c] = true
			ids = append(ids, c)
			bytes += w.sizes[c]
		}
	}
	return ids, bytes
}

func (w *simWorld) estimate(j *simJob) uint64 {
	_, n := w.missing(j.chunks)
	return n
}

// backup stores the job's dataset as a snapshot. If the disk fills up it
// leaves the chunks it managed to write as garbage and fails.
func (w *simWorld) backup(j *simJob) (uint64, error) {
	defer w.enter("backup")()
	ids, bytes := w.missing(j.chunks)
	if bytes > w.free() {
		for _, c := range ids {
			if w.sizes[c] > w.free() {
				break
			}
			w.stored[c] = true
		}
		w.fullDisks++
		return 0, errSimDiskFull
	}
	for _, c := range ids {
		w.stored[c] = true
	}
	// Multiplying by an odd constant is a bijection modulo 2^32: IDs are
	// unique, like restic's, but not ordered by time, so tie-breaks matter.
	w.snapSeq++
	id := fmt.Sprintf("%08x", w.snapSeq*2654435761)
	w.snaps = append(w.snaps, simSnapshot{
		Snapshot: snap(id, j.name, w.now),
		chunks:   slices.Clone(j.chunks),
	})
	return bytes, nil
}

type simStats struct {
	runs, backups, skippedTooLarge, cannotReclaim, retries, retriedOK int
}

// runJob mirrors the orchestration of DESIGN.md §3 for one due job.
func runJob(t *testing.T, w *simWorld, r *Reclaimer, j *simJob, st *simStats) {
	ctx := context.Background()
	st.runs++
	growth, ok := PredictGrowth(j.history, 1.5)
	if !ok {
		growth = FirstRunGrowth(w.estimate(j), 1.0)
	}
	if err := r.Targets.CheckFits(growth, w.total); err != nil {
		st.skippedTooLarge++
		return
	}
	reclaimFailed := false
	if _, err := r.Reclaim(ctx, growth); err != nil {
		if !errors.Is(err, ErrCannotReclaim) {
			t.Fatal(err)
		}
		st.cannotReclaim++
		return
	}
	// Sometimes other files fill the partition between the space check and
	// the backup, e.g. the user copies something onto the backup drive.
	if w.rng.IntN(25) == 0 {
		if f := w.free(); f > 50 {
			w.other += f - uint64(w.rng.IntN(50))
		}
	}
	added, err := w.backup(j)
	if errors.Is(err, errSimDiskFull) {
		full, derr := r.DiskFull()
		if derr != nil || !full {
			t.Fatalf("backup failed on a full disk but DiskFull = %v, %v", full, derr)
		}
		st.retries++
		if _, rerr := r.RecoverFromFullDisk(ctx, growth); rerr != nil {
			if !errors.Is(rerr, ErrCannotReclaim) {
				t.Fatal(rerr)
			}
			st.cannotReclaim++
			reclaimFailed = true
		} else {
			added, err = w.backup(j)
			if err == nil {
				st.retriedOK++
			}
		}
	}
	if err == nil {
		st.backups++
		j.history = append(j.history, added)
	}
	// Post-backup check restores the margins if the backup used more than
	// predicted.
	if _, err := r.Reclaim(ctx, 0); err != nil {
		if !errors.Is(err, ErrCannotReclaim) {
			t.Fatal(err)
		}
		st.cannotReclaim++
		reclaimFailed = true
	}
	if !reclaimFailed && w.free() < r.Targets.MinFree {
		t.Fatalf("invariant 1 violated: free %d < min_free %d after a completed run", w.free(), r.Targets.MinFree)
	}
}

func TestSimulation(t *testing.T) {
	var total simStats
	var forgotten, fullDisks int
	seeds := uint64(6)
	if v, err := strconv.ParseUint(os.Getenv("OUROBOROS_SIM_SEEDS"), 10, 64); err == nil && v > 0 {
		seeds = v // e.g. OUROBOROS_SIM_SEEDS=200 for a long run
	}
	for seed := uint64(1); seed <= seeds; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			w := &simWorld{
				t:      t,
				rng:    rand.New(rand.NewPCG(seed, seed*31)),
				now:    t0,
				total:  20000,
				other:  500,
				sizes:  map[int]uint64{},
				stored: map[int]bool{},
				policy: Policy{KeepMin: 3, ProtectNewerThan: 24 * time.Hour},
			}
			targets := Targets{MinFree: 2000, PruneHeadroom: 400}
			if seed%3 == 0 {
				targets.MaxRepoSize = 12000
			}
			r := &Reclaimer{
				Repo: w, Disk: w, Sizer: w, Clock: w, Ballast: w,
				Host: "h", Policy: w.policy, Targets: targets, BallastSize: 300,
			}
			if err := w.Create(300); err != nil {
				t.Fatal(err)
			}

			jobs := []*simJob{
				{name: "docs", churn: 0.05, growth: 0.002},
				{name: "photos", churn: 0.01, growth: 0.005},
				{name: "volume", churn: 0.15, growth: 0.0005},
			}
			for _, j := range jobs {
				for range 20 + w.rng.IntN(40) {
					j.chunks = append(j.chunks, w.newChunk())
				}
			}

			var st simStats
			for day := 0; day < 400; day++ {
				for _, j := range jobs {
					if w.rng.IntN(3) == 0 {
						continue // not due today
					}
					var others []*simJob
					for _, o := range jobs {
						if o != j {
							others = append(others, o)
						}
					}
					w.mutate(j, others)
					runJob(t, w, r, j, &st)
					w.now = w.now.Add(time.Duration(1+w.rng.IntN(8)) * time.Hour)
				}
				// Occasionally the user pins a snapshot or the dataset is
				// trimmed by hand.
				if len(w.snaps) > 0 && w.rng.IntN(40) == 0 {
					s := &w.snaps[w.rng.IntN(len(w.snaps))]
					s.Tags = append(s.Tags, backend.TagPinned)
				}
				if w.rng.IntN(60) == 0 {
					j := jobs[w.rng.IntN(len(jobs))]
					j.chunks = j.chunks[:len(j.chunks)/2]
				}
				// Files copied onto the partition are cleaned up again.
				if w.other > 500 && w.rng.IntN(2) == 0 {
					w.other = 500
				}
				w.now = w.now.Add(24 * time.Hour)
			}

			t.Logf("runs %d backups %d forgotten %d prunes %d full-disk %d retries %d (ok %d) cannot-reclaim %d too-large %d",
				st.runs, st.backups, w.forgotten, w.prunes, w.fullDisks, st.retries, st.retriedOK, st.cannotReclaim, st.skippedTooLarge)
			total.runs += st.runs
			total.retriedOK += st.retriedOK
			total.backups += st.backups
			total.retries += st.retries
			total.cannotReclaim += st.cannotReclaim
			forgotten += w.forgotten
			fullDisks += w.fullDisks
		})
	}
	// The simulation is only meaningful if it reached the interesting states.
	// Some seeds legitimately end with more protected data than the disk
	// holds; overall most runs must still succeed.
	if total.backups < total.runs*6/10 || forgotten == 0 || fullDisks == 0 || total.retriedOK == 0 || total.cannotReclaim == 0 {
		t.Fatalf("simulation did not exercise every path: %+v forgotten %d full disks %d", total, forgotten, fullDisks)
	}
}
