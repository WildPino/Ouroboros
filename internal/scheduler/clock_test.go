package scheduler

import (
	"path/filepath"
	"testing"
	"time"
	_ "time/tzdata" // Europe/Rome without depending on the system database

	"github.com/WildPino/ouroboros/internal/state"
)

func rome(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// roundTrip stores and reloads a job state, as between two runs: the
// monotonic clock reading is lost and only the wall time with its offset
// remains.
func roundTrip(t *testing.T, js state.JobState) state.JobState {
	t.Helper()
	p := filepath.Join(t.TempDir(), state.StateFile)
	if err := state.Save(p, state.State{Jobs: map[string]state.JobState{"j": js}}); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Jobs["j"]
}

// Intervals are measured in elapsed time, not in wall-clock hours: a
// daylight saving change neither delays nor brings forward a job.
func TestDueAcrossDaylightSaving(t *testing.T) {
	loc := rome(t)
	const interval = 6 * time.Hour
	tests := []struct {
		name string
		last time.Time
		// Wall-clock times, in Rome, just before and at the due time.
		before, due time.Time
	}{
		// 2026-03-29 02:00 CET -> 03:00 CEST: six real hours after 00:30
		// the wall clock reads 07:30.
		{"spring forward", time.Date(2026, 3, 29, 0, 30, 0, 0, loc),
			time.Date(2026, 3, 29, 7, 29, 0, 0, loc), time.Date(2026, 3, 29, 7, 30, 0, 0, loc)},
		// 2026-10-25 03:00 CEST -> 02:00 CET: the wall clock reads 05:30.
		{"fall back", time.Date(2026, 10, 25, 0, 30, 0, 0, loc),
			time.Date(2026, 10, 25, 5, 29, 0, 0, loc), time.Date(2026, 10, 25, 5, 30, 0, 0, loc)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			js := roundTrip(t, state.JobState{LastAttempt: tt.last, LastSuccess: tt.last})
			if JobDue(js, interval, time.Minute, tt.before) {
				t.Errorf("due at %s", tt.before)
			}
			if !JobDue(js, interval, time.Minute, tt.due) {
				t.Errorf("not due at %s", tt.due)
			}
			if got := tt.due.Sub(tt.last); got != interval {
				t.Fatalf("test data: %s elapsed", got)
			}
		})
	}
}

// A clock that ran ahead and was then corrected leaves timestamps in the
// future. Backups resume at once instead of waiting for the clock to
// catch up, and the schedule is normal again after the next run.
func TestDueAfterClockCorrection(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	wrong := now.Add(30 * 24 * time.Hour)
	js := roundTrip(t, state.JobState{LastAttempt: wrong, LastSuccess: wrong})
	if !JobDue(js, 6*time.Hour, 30*time.Minute, now) {
		t.Fatal("not due after the clock was set back")
	}
	if !MaintenanceDue(wrong, 7*24*time.Hour, now) {
		t.Fatal("maintenance not due after the clock was set back")
	}
	js = roundTrip(t, state.JobState{LastAttempt: now, LastSuccess: now})
	if JobDue(js, 6*time.Hour, 30*time.Minute, now.Add(time.Hour)) {
		t.Fatal("due again an hour after a normal run")
	}
}
