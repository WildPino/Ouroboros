//go:build windows

package lock

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
)

// Tests use the per-session namespace: creating Global objects needs a
// privilege that a normal user does not have.
func testName(t *testing.T) string {
	return fmt.Sprintf(`Local\OuroborosTest-%d-%s`, os.Getpid(), t.Name())
}

func TestAcquireExcludes(t *testing.T) {
	name := testName(t)
	l, err := Acquire(name)
	if err != nil {
		t.Fatal(err)
	}
	// Even from this same goroutine, a second acquisition must fail: the
	// mutex is held by a different OS thread.
	if _, err := Acquire(name); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Acquire: %v, want ErrLocked", err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
	l2, err := Acquire(name)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	l2.Release()
}

func TestAcquireConcurrent(t *testing.T) {
	name := testName(t)
	held, err := Acquire(name)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := Acquire(name)
			if err == nil {
				l.Release()
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, ErrLocked) {
			t.Fatalf("Acquire while held: %v, want ErrLocked", err)
		}
	}
}

func TestReleaseNil(t *testing.T) {
	var l *Lock
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestHeld(t *testing.T) {
	name := testName(t)
	if held, err := Held(name); err != nil || held {
		t.Fatalf("before Acquire: held=%v err=%v", held, err)
	}
	l, err := Acquire(name)
	if err != nil {
		t.Fatal(err)
	}
	if held, err := Held(name); err != nil || !held {
		t.Fatalf("while held: held=%v err=%v", held, err)
	}
	// The probe must not have taken the mutex.
	if _, err := Acquire(name); !errors.Is(err, ErrLocked) {
		t.Fatalf("Acquire after probe: %v, want ErrLocked", err)
	}
	l.Release()
	if held, err := Held(name); err != nil || held {
		t.Fatalf("after Release: held=%v err=%v", held, err)
	}
}
