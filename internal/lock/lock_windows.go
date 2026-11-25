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
	release, done := make(chan struct{}), make(chan error, 1)
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
		<-release
		done <- windows.ReleaseMutex(h)
	}()
	if err := <-acquired; err != nil {
		return nil, err
	}
	return &Lock{release: release, done: done}, nil
}

// Held reports whether some process holds the named mutex, without taking
// it: a probe that acquired the mutex, even briefly, could make a scheduled
// run starting at that moment skip its work. A named mutex exists only while
// a handle to it is open, and holders keep theirs open for the whole run.
func Held(name string) (bool, error) {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false, err
	}
	h, err := windows.OpenMutex(windows.SYNCHRONIZE, false, n)
	if err == windows.ERROR_FILE_NOT_FOUND {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open mutex %s: %w", name, err)
	}
	windows.CloseHandle(h)
	return true, nil
}
