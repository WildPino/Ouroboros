package scheduler

import (
	"time"

	"github.com/WildPino/ouroboros/internal/state"
)

// NextRun returns when a job is next due (DESIGN.md §4). After a failed
// attempt only retry_after counts: otherwise a job whose interval has long
// passed would be retried on every tick.
func NextRun(js state.JobState, interval, retryAfter time.Duration) time.Time {
	switch {
	case js.LastAttempt.IsZero() && js.LastSuccess.IsZero():
		return time.Time{} // never run: due immediately
	case js.Failed():
		return js.LastAttempt.Add(retryAfter)
	default:
		return js.LastSuccess.Add(interval)
	}
}

// JobDue reports whether a job is due at now.
func JobDue(js state.JobState, interval, retryAfter time.Duration, now time.Time) bool {
	if clockWentBack(now, js.LastAttempt, js.LastSuccess) {
		return true
	}
	return !now.Before(NextRun(js, interval, retryAfter))
}

// MaintenanceDue reports whether a periodic task last run at last is due.
func MaintenanceDue(last time.Time, interval time.Duration, now time.Time) bool {
	return last.IsZero() || clockWentBack(now, last) || !now.Before(last.Add(interval))
}

// clockSkew is how far in the future a stored timestamp may be before the
// clock is considered to have been set back.
const clockSkew = 5 * time.Minute

// clockWentBack reports whether a recorded time lies in the future. That
// happens when the system clock was wrong and then corrected; waiting for
// the clock to catch up could suspend backups for an arbitrary time, so the
// task runs instead.
func clockWentBack(now time.Time, recorded ...time.Time) bool {
	for _, t := range recorded {
		if t.After(now.Add(clockSkew)) {
			return true
		}
	}
	return false
}
