package scheduler

import (
	"testing"
	"time"

	"github.com/WildPino/ouroboros/internal/state"
)

var t0 = time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

func TestJobDue(t *testing.T) {
	const interval, retry = 6 * time.Hour, 30 * time.Minute
	at := func(d time.Duration) time.Time { return t0.Add(d) }
	ok := state.JobState{LastAttempt: t0, LastSuccess: t0}
	failed := state.JobState{LastAttempt: at(10 * time.Hour), LastSuccess: t0, LastError: "x"}
	crashed := state.JobState{LastAttempt: at(time.Hour), LastSuccess: t0}
	tests := []struct {
		name string
		js   state.JobState
		now  time.Time
		want bool
		next time.Time
	}{
		{"never run", state.JobState{}, t0, true, time.Time{}},
		{"before interval", ok, at(6*time.Hour - time.Second), false, at(6 * time.Hour)},
		{"at interval", ok, at(6 * time.Hour), true, at(6 * time.Hour)},
		// Interval long past, but the last attempt failed: wait retry_after.
		{"failed, retry pending", failed, at(10*time.Hour + 10*time.Minute), false, at(10*time.Hour + 30*time.Minute)},
		{"failed, retry due", failed, at(10*time.Hour + 30*time.Minute), true, at(10*time.Hour + 30*time.Minute)},
		{"died mid-run, retry pending", crashed, at(time.Hour + time.Minute), false, at(time.Hour + 30*time.Minute)},
		{"died mid-run, retry due", crashed, at(2 * time.Hour), true, at(time.Hour + 30*time.Minute)},
		{"first run failed", state.JobState{LastAttempt: t0, LastError: "x"}, at(29 * time.Minute), false, at(30 * time.Minute)},
		{"clock set back", ok, t0.Add(-24 * time.Hour), true, at(6 * time.Hour)},
		{"small skew tolerated", ok, t0.Add(-time.Minute), false, at(6 * time.Hour)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := JobDue(tt.js, interval, retry, tt.now); got != tt.want {
				t.Errorf("JobDue = %v, want %v", got, tt.want)
			}
			if got := NextRun(tt.js, interval, retry); !got.Equal(tt.next) {
				t.Errorf("NextRun = %v, want %v", got, tt.next)
			}
		})
	}
}

func TestMaintenanceDue(t *testing.T) {
	week := 7 * 24 * time.Hour
	tests := []struct {
		last, now time.Time
		want      bool
	}{
		{time.Time{}, t0, true},
		{t0, t0.Add(week - time.Second), false},
		{t0, t0.Add(week), true},
		{t0, t0.Add(-48 * time.Hour), true},
	}
	for _, tt := range tests {
		if got := MaintenanceDue(tt.last, week, tt.now); got != tt.want {
			t.Errorf("MaintenanceDue(%v, %v) = %v", tt.last, tt.now, got)
		}
	}
}
