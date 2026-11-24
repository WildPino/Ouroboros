//go:build windows

package lock

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/windows"
)

// Acquire takes the named mutex without waiting, or returns ErrLocked.
//
// A Windows mutex belongs to the thread that acquired it and only that
// thread can release it, so the whole lifetime runs on one goroutine pinned
// to its OS thread. A mutex abandoned by a crashed process is acquired
// normally.
func Acquire(name string) (*Lock, error) {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	l := &Lock{release: make(chan struct{}), done: make(chan error, 1)}
	acquired := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		h, err := windows.CreateMutex(nil, false, n)
		if h == 0 {
			acquired <- fmt.Errorf("create mutex %s: %w", name, err)
			return
		}
		defer windows.CloseHandle(h)
		ev, err := windows.WaitForSingleObject(h, 0)
		switch {
		case err != nil:
			acquired <- fmt.Errorf("wait for mutex %s: %w", name, err)
			return
		case ev == uint32(windows.WAIT_TIMEOUT):
			acquired <- ErrLocked
			return
		case ev != windows.WAIT_OBJECT_0 && ev != windows.WAIT_ABANDONED:
			acquired <- fmt.Errorf("wait for mutex %s: unexpected result %#x", name, ev)
			return
		}
		acquired <- nil
		<-l.release
		l.done <- windows.ReleaseMutex(h)
	}()
	if err := <-acquired; err != nil {
		return nil, err
	}
	return l, nil
}
