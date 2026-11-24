package space

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
	"github.com/WildPino/ouroboros/internal/config"
)

const (
	KB = 1 << 10
	MB = 1 << 20
	GB = 1 << 30
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestNewTargets(t *testing.T) {
	s := config.Default().Space // min_free 10%, prune_headroom 2%
	tg := NewTargets(s, 1000*GB)
	if tg.MinFree != 100*GB || tg.PruneHeadroom != 20*GB || tg.MaxRepoSize != 0 {
		t.Fatalf("targets %+v", tg)
	}
	// prune_headroom never goes below 1 GB.
	if small := NewTargets(s, 10*GB); small.PruneHeadroom != GB {
		t.Fatalf("small partition headroom %d", small.PruneHeadroom)
	}
	s.MaxRepoSize = config.Bytes(500 * GB)
	if tg := NewTargets(s, 1000*GB); tg.MaxRepoSize != 500*GB {
		t.Fatalf("max repo %d", tg.MaxRepoSize)
	}
	if got := tg.RequiredFree(5 * GB); got != 125*GB {
		t.Fatalf("RequiredFree = %d", got)
	}
}

func TestCheckFits(t *testing.T) {
	tg := Targets{MinFree: 10 * GB, PruneHeadroom: 2 * GB}
	if err := tg.CheckFits(80*GB, 100*GB); err != nil {
		t.Fatalf("fits: %v", err)
	}
	if err := tg.CheckFits(89*GB, 100*GB); !errors.Is(err, ErrBackupTooLarge) {
		t.Fatalf("too large: %v", err)
	}
	tg.MaxRepoSize = 50 * GB
	if err := tg.CheckFits(60*GB, 1000*GB); !errors.Is(err, ErrBackupTooLarge) {
		t.Fatalf("over max_repo_size: %v", err)
	}
}

func TestPredictGrowth(t *testing.T) {
	if _, ok := PredictGrowth(nil, 1.5); ok {
		t.Fatal("no history must report false")
	}
	h := []uint64{1000, 50, 60, 70, 80, 90, 100, 110, 120, 130, 140}
	// 1000 is the 11th most recent run: outside the window.
	if g, ok := PredictGrowth(h, 1.5); !ok || g != 210 {
		t.Fatalf("got %d, %v; want 210", g, ok)
	}
	if g, _ := PredictGrowth([]uint64{0, 0}, 1.5); g != 0 {
		t.Fatalf("zero growth: %d", g)
	}
	if g, _ := PredictGrowth([]uint64{3}, 1.5); g != 5 {
		t.Fatalf("rounding: %d, want ceil(4.5)=5", g)
	}
	if g := FirstRunGrowth(100, 1.0); g != 100 {
		t.Fatalf("first run: %d", g)
	}
}

func snap(id, job string, at time.Time, extra ...string) backend.Snapshot {
	tags := append([]string{backend.TagOuroboros, backend.JobTag(job)}, extra...)
	return backend.Snapshot{ID: id, Time: at, Hostname: "h", Tags: tags}
}

func ids(snaps []backend.Snapshot) []string {
	out := make([]string, len(snaps))
	for i, s := range snaps {
		out[i] = s.ID
	}
	return out
}

func TestSelectCandidates(t *testing.T) {
	now := t0.Add(100 * 24 * time.Hour)
	day := func(d int) time.Time { return t0.Add(time.Duration(d) * 24 * time.Hour) }
	snaps := []backend.Snapshot{
		snap("a5", "a", day(99)), // recent
		snap("a1", "a", day(1)),
		snap("b1", "b", day(2)),
		snap("a2", "a", day(3), backend.TagPinned),
		snap("a3", "a", day(5)),
		snap("b2", "b", day(4)),
		snap("a4", "a", day(50)),
		snap("b3", "b", day(60)),
		snap("x1", "", day(0)), // no job tag: its own group, newest of it
		snap("c1", "c", day(7)),
		snap("c0", "c", day(7)), // same time as c1: tie broken by ID
		{ID: "n1", Time: day(0), Tags: []string{backend.TagOuroboros}},
	}
	sel := SelectCandidates(snaps, Policy{KeepMin: 2, ProtectNewerThan: 48 * time.Hour}, now)

	// job a: newest two are a5, a4. b: b3, b2. c: c1, c0 (only two). no-job: x1, n1.
	want := []string{"a1", "b1", "a3"}
	if got := ids(sel.Candidates); !slices.Equal(got, want) {
		t.Fatalf("candidates %v, want %v", got, want)
	}
	reasons := map[string]string{}
	for _, p := range sel.Protected {
		reasons[p.ID] = p.Reason
	}
	wantReasons := map[string]string{
		"a5": ReasonNewest, "a4": ReasonNewest, "b3": ReasonNewest, "b2": ReasonNewest,
		"c1": ReasonNewest, "c0": ReasonNewest, "x1": ReasonNewest, "n1": ReasonNewest,
		"a2": ReasonPinned,
	}
	for id, r := range wantReasons {
		if reasons[id] != r {
			t.Errorf("%s: reason %q, want %q", id, reasons[id], r)
		}
	}
	if len(sel.Candidates)+len(sel.Protected) != len(snaps) {
		t.Fatal("snapshots lost")
	}

	// KeepMin 1 with a 60-day window: a4 and b3 stay as recent; in the job-less
	// group x1 wins the tie with n1 by ID, so n1 becomes a candidate.
	sel = SelectCandidates(snaps, Policy{KeepMin: 1, ProtectNewerThan: 60 * 24 * time.Hour}, now)
	if got := ids(sel.Candidates); !slices.Equal(got, []string{"n1", "a1", "b1", "b2", "a3", "c0"}) {
		t.Fatalf("candidates %v", got)
	}
}

func TestSelectCandidatesTieBreak(t *testing.T) {
	snaps := []backend.Snapshot{snap("bb", "j", t0), snap("aa", "j", t0), snap("zz", "j", t0.Add(time.Hour))}
	sel := SelectCandidates(snaps, Policy{KeepMin: 1}, t0.Add(48*time.Hour))
	if got := ids(sel.Candidates); !slices.Equal(got, []string{"aa", "bb"}) {
		t.Fatalf("got %v", got)
	}
}

// scripted is a fake repository and disk: each snapshot frees a fixed
// amount of space when it is forgotten and a prune runs.
type scripted struct {
	snaps    []backend.Snapshot
	frees    map[string]uint64
	free     uint64
	total    uint64
	pending  uint64
	forgets  [][]string
	prunes   []string
	ballast  uint64 // size of the ballast on disk, 0 = none
	events   []string
	snapErr  error
	repoSize uint64
}

func (s *scripted) Snapshots(_ context.Context, f backend.SnapshotFilter) ([]backend.Snapshot, error) {
	if s.snapErr != nil {
		return nil, s.snapErr
	}
	return slices.Clone(s.snaps), nil
}

func (s *scripted) Forget(_ context.Context, ids []string) error {
	s.forgets = append(s.forgets, ids)
	s.events = append(s.events, fmt.Sprintf("forget %d", len(ids)))
	for _, id := range ids {
		i := slices.IndexFunc(s.snaps, func(x backend.Snapshot) bool { return x.ID == id })
		if i < 0 {
			return fmt.Errorf("unknown snapshot %s", id)
		}
		s.snaps = slices.Delete(s.snaps, i, i+1)
		s.pending += s.frees[id]
	}
	return nil
}

func (s *scripted) Prune(_ context.Context, o backend.PruneOptions) (backend.PruneResult, error) {
	s.prunes = append(s.prunes, o.MaxUnused)
	s.events = append(s.events, "prune")
	s.free += s.pending
	s.repoSize -= min(s.repoSize, s.pending)
	s.pending = 0
	return backend.PruneResult{}, nil
}

func (s *scripted) Usage(string) (Usage, error) { return Usage{Total: s.total, Free: s.free}, nil }

func (s *scripted) RepoSize(context.Context, string) (uint64, error) { return s.repoSize, nil }

func (s *scripted) Exists() (bool, error) { return s.ballast > 0, nil }

func (s *scripted) Create(size uint64) error {
	if size > s.free {
		return errors.New("disk full")
	}
	s.free -= size
	s.ballast = size
	s.events = append(s.events, "ballast create")
	return nil
}

func (s *scripted) Delete() (uint64, error) {
	n := s.ballast
	s.free += n
	s.ballast = 0
	if n > 0 {
		s.events = append(s.events, "ballast delete")
	}
	return n, nil
}

type fixedClock time.Time

func (c fixedClock) Now() time.Time { return time.Time(c) }

// newScripted builds n deletable snapshots (s00 oldest) that free each MB
// apiece, plus one protected newest snapshot.
func newScripted(n int, each uint64) *scripted {
	s := &scripted{frees: map[string]uint64{}, total: 1000 * MB}
	for i := range n {
		id := fmt.Sprintf("s%02d", i)
		s.snaps = append(s.snaps, snap(id, "j", t0.Add(time.Duration(i)*time.Hour)))
		s.frees[id] = each
	}
	s.snaps = append(s.snaps, snap("newest", "j", t0.Add(1000*time.Hour)))
	return s
}

func reclaimerFor(s *scripted) *Reclaimer {
	return &Reclaimer{
		Repo: s, Disk: s, Sizer: s, Ballast: s,
		Clock:   fixedClock(t0.Add(2000 * time.Hour)),
		Host:    "h",
		Policy:  Policy{KeepMin: 1},
		Targets: Targets{MinFree: 100 * MB, PruneHeadroom: 20 * MB},
	}
}

func TestReclaimNothingToDo(t *testing.T) {
	s := newScripted(5, MB)
	s.free = 500 * MB
	rep, err := reclaimerFor(s).Reclaim(context.Background(), 10*MB)
	if err != nil || len(s.forgets) != 0 || len(s.prunes) != 0 || len(rep.Forgotten) != 0 {
		t.Fatalf("rep %+v err %v forgets %v", rep, err, s.forgets)
	}
}

func TestReclaimExponentialBatches(t *testing.T) {
	s := newScripted(60, MB)
	// need = 10 + 100 + 20 = 130 MB, so 30 snapshots must go.
	s.free = 100 * MB
	rep, err := reclaimerFor(s).Reclaim(context.Background(), 10*MB)
	if err != nil {
		t.Fatal(err)
	}
	var sizes []int
	for _, f := range s.forgets {
		sizes = append(sizes, len(f))
	}
	// 1+2+4+8+16 = 31 >= 30: five rounds, the last one capped at 16.
	if !slices.Equal(sizes, []int{1, 2, 4, 8, 16}) {
		t.Fatalf("batch sizes %v", sizes)
	}
	if !slices.Equal(s.prunes, []string{"0", "0", "0", "0", "0"}) {
		t.Fatalf("prunes %v", s.prunes)
	}
	// Strictly oldest first.
	var all []string
	for _, f := range s.forgets {
		all = append(all, f...)
	}
	for i, id := range all {
		if id != fmt.Sprintf("s%02d", i) {
			t.Fatalf("deletion order %v", all)
		}
	}
	if rep.FreeAfter < 130*MB || rep.Freed() != 31*MB || len(rep.Forgotten) != 31 {
		t.Fatalf("report %+v", rep)
	}
}

func TestReclaimBatchCap(t *testing.T) {
	s := newScripted(100, MB)
	s.free = 0
	_, err := reclaimerFor(s).Reclaim(context.Background(), 0) // need 120 MB, only 100 available
	if !errors.Is(err, ErrCannotReclaim) {
		t.Fatalf("err %v", err)
	}
	var sizes []int
	for _, f := range s.forgets {
		sizes = append(sizes, len(f))
	}
	if !slices.Equal(sizes, []int{1, 2, 4, 8, 16, 16, 16, 16, 16, 5}) {
		t.Fatalf("batch sizes %v", sizes)
	}
	var ce *CannotReclaimError
	if !errors.As(err, &ce) || ce.Freed != 100*MB || ce.Missing != 20*MB {
		t.Fatalf("details %+v", ce)
	}
	if len(s.snaps) != 1 || s.snaps[0].ID != "newest" {
		t.Fatalf("protected snapshot touched: %v", ids(s.snaps))
	}
}

func TestReclaimNoCandidates(t *testing.T) {
	s := newScripted(0, MB)
	_, err := reclaimerFor(s).Reclaim(context.Background(), 0)
	if !errors.Is(err, ErrCannotReclaim) || len(s.forgets) != 0 {
		t.Fatalf("err %v forgets %v", err, s.forgets)
	}
}

func TestReclaimUsesBallastWhenBelowHeadroom(t *testing.T) {
	s := newScripted(10, 50*MB)
	s.ballast = 30 * MB
	s.free = 5 * MB // below prune_headroom (20 MB)
	r := reclaimerFor(s)
	r.BallastSize = 30 * MB
	rep, err := r.Reclaim(context.Background(), 0) // need 120 MB
	if err != nil {
		t.Fatal(err)
	}
	if !rep.BallastDeleted || !rep.BallastRecreated || s.ballast != 30*MB {
		t.Fatalf("report %+v ballast %d", rep, s.ballast)
	}
	// Ballast removed before the first prune, recreated at the end.
	if s.events[0] != "forget 1" || s.events[1] != "ballast delete" || s.events[2] != "prune" ||
		s.events[len(s.events)-1] != "ballast create" {
		t.Fatalf("events %v", s.events)
	}
	if s.free < 120*MB {
		t.Fatalf("recreating the ballast broke the goal: free %d", s.free)
	}
}

func TestReclaimKeepsBallastWhenAboveHeadroom(t *testing.T) {
	s := newScripted(10, 50*MB)
	s.ballast = 30 * MB
	s.free = 50 * MB
	r := reclaimerFor(s)
	r.BallastSize = 30 * MB
	rep, err := r.Reclaim(context.Background(), 0)
	if err != nil || rep.BallastDeleted || s.ballast != 30*MB {
		t.Fatalf("rep %+v err %v", rep, err)
	}
}

func TestReclaimDoesNotRecreateBallastIfItBreaksGoal(t *testing.T) {
	s := newScripted(3, 50*MB)
	s.ballast = 30 * MB
	s.free = 0
	r := reclaimerFor(s)
	r.BallastSize = 30 * MB
	rep, err := r.Reclaim(context.Background(), 0) // after 3 deletions free = 180, need 120: 180-30 = 150 ok
	if err != nil || !rep.BallastRecreated {
		t.Fatalf("rep %+v err %v", rep, err)
	}

	s = newScripted(2, 50*MB)
	s.ballast = 30 * MB
	s.free = 0
	r = reclaimerFor(s)
	r.BallastSize = 30 * MB
	rep, err = r.Reclaim(context.Background(), 0) // free 130 at most: recreating leaves 100 < 120
	if !errors.Is(err, ErrCannotReclaim) && err != nil {
		t.Fatal(err)
	}
	if rep.BallastRecreated || s.ballast != 0 {
		t.Fatalf("ballast recreated although it breaks the goal: %+v", rep)
	}
}

func TestReclaimMaxRepoSize(t *testing.T) {
	s := newScripted(10, 10*MB)
	s.free = 900 * MB // plenty of free space
	s.repoSize = 200 * MB
	r := reclaimerFor(s)
	r.Targets.MaxRepoSize = 180 * MB
	// repo 200 + growth 5 must drop to <= 180: 25 MB, three snapshots.
	rep, err := r.Reclaim(context.Background(), 5*MB)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Forgotten) != 3 || s.repoSize != 170*MB {
		t.Fatalf("forgotten %d, repo size %d", len(rep.Forgotten), s.repoSize)
	}
}

func TestReclaimPropagatesErrors(t *testing.T) {
	s := newScripted(3, MB)
	s.snapErr = errors.New("repository locked")
	if _, err := reclaimerFor(s).Reclaim(context.Background(), 0); err == nil || len(s.forgets) != 0 {
		t.Fatalf("err %v", err)
	}
}

func TestPlanChangesNothing(t *testing.T) {
	s := newScripted(5, MB)
	s.free = 10 * MB
	before := slices.Clone(s.snaps)
	plan, err := reclaimerFor(s).Plan(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Missing != 110*MB || plan.NeedFree != 120*MB || len(plan.Selection.Candidates) != 5 || len(plan.Selection.Protected) != 1 {
		t.Fatalf("plan %+v", plan)
	}
	if len(s.forgets) != 0 || len(s.prunes) != 0 || !slices.EqualFunc(before, s.snaps, func(a, b backend.Snapshot) bool { return a.ID == b.ID }) {
		t.Fatal("Plan modified the repository")
	}
}

func TestDiskFullAndRecovery(t *testing.T) {
	s := newScripted(10, 20*MB)
	s.ballast = 30 * MB
	s.free = 5 * MB
	s.pending = 8 * MB // packs left by the interrupted backup
	r := reclaimerFor(s)
	r.BallastSize = 30 * MB

	full, err := r.DiskFull()
	if err != nil || !full {
		t.Fatalf("DiskFull = %v, %v", full, err)
	}
	// Growth 15 MB doubled: need 30 + 100 + 20 = 150 MB.
	rep, err := r.RecoverFromFullDisk(context.Background(), 15*MB)
	if err != nil {
		t.Fatal(err)
	}
	if s.prunes[0] != "" {
		t.Fatalf("first prune must use restic defaults, got max-unused %q", s.prunes[0])
	}
	if s.events[0] != "ballast delete" || s.events[1] != "prune" {
		t.Fatalf("events %v", s.events)
	}
	if !rep.BallastDeleted || s.free < 150*MB {
		t.Fatalf("report %+v free %d", rep, s.free)
	}
	if full, _ := r.DiskFull(); full {
		t.Fatal("still full after recovery")
	}
}

func TestDirSize(t *testing.T) {
	dir := t.TempDir()
	for name, n := range map[string]int{"a": 10, "sub/b": 20, "sub/deep/c": 30} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := DirSize(context.Background(), dir); err != nil || got != 60 {
		t.Fatalf("DirSize = %d, %v", got, err)
	}
	if got, err := DirSize(context.Background(), filepath.Join(dir, "missing")); err != nil || got != 0 {
		t.Fatalf("missing dir: %d, %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := range 2000 {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d", i)), nil, 0o644)
	}
	if _, err := DirSize(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}

func TestBallastFile(t *testing.T) {
	b := BallastFile{Path: filepath.Join(t.TempDir(), "r.ballast")}
	if ok, err := b.Exists(); ok || err != nil {
		t.Fatal(ok, err)
	}
	if err := b.Create(MB); err != nil {
		t.Fatal(err)
	}
	if ok, _ := b.Exists(); !ok {
		t.Fatal("not created")
	}
	if n, err := b.Delete(); n != MB || err != nil {
		t.Fatal(n, err)
	}
}

func TestReclaimerCheckFits(t *testing.T) {
	s := newScripted(1, MB)
	r := reclaimerFor(s)
	if err := r.CheckFits(10 * MB); err != nil {
		t.Fatal(err)
	}
	if err := r.CheckFits(s.total); !errors.Is(err, ErrBackupTooLarge) {
		t.Fatalf("got %v", err)
	}
}
