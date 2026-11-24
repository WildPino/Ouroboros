package space

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
)

func keptIDs(keep map[string]bool) []string {
	var out []string
	for id := range keep {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

func TestRetentionKeepDaily(t *testing.T) {
	// Two snapshots a day for five days: s<day><n>, n=1 morning, 2 evening.
	var snaps []backend.Snapshot
	for d := 1; d <= 5; d++ {
		for n, h := range []int{8, 20} {
			at := time.Date(2026, 3, d, h, 0, 0, 0, time.UTC)
			snaps = append(snaps, snap(fmt.Sprintf("s%d%d", d, n+1), "docs", at))
		}
	}
	got := keptIDs(RetentionKeep(snaps, KeepRules{Daily: 3}, time.UTC))
	// Newest of each of the last three days.
	if want := []string{"s32", "s42", "s52"}; !slices.Equal(got, want) {
		t.Fatalf("kept %v, want %v", got, want)
	}

	got = keptIDs(RetentionKeep(snaps, KeepRules{Hourly: 3, Daily: 2}, time.UTC))
	// Hourly keeps the three newest (each in its own hour); daily adds
	// nothing beyond them.
	if want := []string{"s42", "s51", "s52"}; !slices.Equal(got, want) {
		t.Fatalf("kept %v, want %v", got, want)
	}
}

func TestRetentionKeepWeeklyMonthlyYearly(t *testing.T) {
	var snaps []backend.Snapshot
	// One snapshot every 10 days across two years.
	start := time.Date(2025, 1, 5, 12, 0, 0, 0, time.UTC)
	for i := range 73 {
		snaps = append(snaps, snap(fmt.Sprintf("s%03d", i), "docs", start.Add(time.Duration(i)*10*24*time.Hour)))
	}
	keep := RetentionKeep(snaps, KeepRules{Weekly: 4, Monthly: 6, Yearly: 5}, time.UTC)
	// Weekly: the 4 newest (each in a different week). Monthly: newest of
	// 6 months; years: newest of each year present (2025, 2026).
	if !keep["s072"] || !keep["s071"] || !keep["s070"] || !keep["s069"] {
		t.Fatal("weekly rule did not keep the newest four")
	}
	years := map[int]bool{}
	for _, s := range snaps {
		if keep[s.ID] {
			years[s.Time.Year()] = true
		}
	}
	if len(years) != 2 {
		t.Fatalf("yearly rule kept years %v", years)
	}
	if n := len(keep); n < 6 || n > 4+6+3 {
		t.Fatalf("kept %d snapshots", n)
	}
}

func TestRetentionKeepPerJobAndLocation(t *testing.T) {
	at := time.Date(2026, 3, 1, 23, 30, 0, 0, time.UTC)
	snaps := []backend.Snapshot{
		snap("a1", "a", at),
		snap("a2", "a", at.Add(time.Hour)), // next day in UTC
		snap("b1", "b", at),
	}
	keep := RetentionKeep(snaps, KeepRules{Daily: 1}, time.UTC)
	if got := keptIDs(keep); !slices.Equal(got, []string{"a2", "b1"}) {
		t.Fatalf("UTC: kept %v", got)
	}
	// In UTC-5 both "a" snapshots fall on March 1st.
	loc := time.FixedZone("UTC-5", -5*3600)
	keep = RetentionKeep(snaps, KeepRules{Daily: 2}, loc)
	if got := keptIDs(keep); !slices.Equal(got, []string{"a2", "b1"}) {
		t.Fatalf("UTC-5: kept %v", got)
	}
}

func TestApplyRetentionRespectsProtection(t *testing.T) {
	s := &scripted{frees: map[string]uint64{}, total: 1000 * MB, free: 900 * MB}
	day := func(d int) time.Time { return t0.Add(time.Duration(d) * 24 * time.Hour) }
	for d := range 10 {
		id := fmt.Sprintf("d%02d", d)
		s.snaps = append(s.snaps, snap(id, "j", day(d)))
		s.frees[id] = MB
	}
	s.snaps[2].Tags = append(s.snaps[2].Tags, backend.TagPinned) // d02
	r := reclaimerFor(s)
	r.Clock = fixedClock(day(10).Add(-time.Hour)) // d07 is just inside the window
	r.Policy = Policy{KeepMin: 2, ProtectNewerThan: 3 * 24 * time.Hour}

	rep, err := r.ApplyRetention(context.Background(), KeepRules{Daily: 1}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	// Daily 1 keeps only d09. Protected: d09, d08 (newest two), d07 (recent),
	// d02 (pinned). Everything else goes.
	want := []string{"d00", "d01", "d03", "d04", "d05", "d06"}
	if got := ids(rep.Forgotten); !slices.Equal(got, want) {
		t.Fatalf("forgotten %v, want %v", got, want)
	}
	if len(s.prunes) != 0 {
		t.Fatal("retention must not prune by itself")
	}

	// A following reclaim prunes the forgotten data first and, since that is
	// enough, deletes nothing else.
	s.free = 115 * MB // need 120 MB; retention's 6 MB pending cover it
	rep, err = r.Reclaim(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Forgotten) != 0 || len(s.prunes) != 1 || s.free != 121*MB {
		t.Fatalf("reclaim after retention: forgotten %v prunes %v free %d", ids(rep.Forgotten), s.prunes, s.free)
	}
}

func TestApplyRetentionDisabled(t *testing.T) {
	s := newScripted(5, MB)
	rep, err := reclaimerFor(s).ApplyRetention(context.Background(), KeepRules{}, time.UTC)
	if err != nil || len(rep.Forgotten) != 0 || len(s.forgets) != 0 {
		t.Fatalf("rep %+v err %v", rep, err)
	}
}

func TestApplyRetentionChunksLargeForgets(t *testing.T) {
	s := newScripted(250, 0)
	r := reclaimerFor(s)
	rep, err := r.ApplyRetention(context.Background(), KeepRules{Yearly: 1}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	var sizes []int
	for _, f := range s.forgets {
		sizes = append(sizes, len(f))
	}
	if !slices.Equal(sizes, []int{100, 100, 50}) || len(rep.Forgotten) != 250 {
		t.Fatalf("chunks %v forgotten %d", sizes, len(rep.Forgotten))
	}
}

func TestRoutinePrune(t *testing.T) {
	s := newScripted(1, MB)
	s.free = 500 * MB
	r := reclaimerFor(s)
	if _, err := r.RoutinePrune(context.Background(), "5%"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.prunes, []string{"5%"}) {
		t.Fatalf("prunes %v", s.prunes)
	}
}
