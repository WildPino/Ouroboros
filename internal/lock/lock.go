package lock

import "errors"

// Name is the machine-wide mutex that serializes Ouroboros runs
// (DESIGN.md §4). The Global namespace makes it shared across sessions, so
// a scheduled run as SYSTEM and an interactive run exclude each other.
const Name = `Global\Ouroboros`

// ErrLocked means another process holds the lock.
var ErrLocked = errors.New("another ouroboros process is running")

// Lock is a held named mutex.
type Lock struct {
	release chan struct{}
	done    chan error
}

// Release frees the lock. It is safe to call more than once.
func (l *Lock) Release() error {
	if l == nil || l.release == nil {
		return nil
	}
	close(l.release)
	l.release = nil
	return <-l.done
}
