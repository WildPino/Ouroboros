//go:build !windows

package lock

import "errors"

// Acquire is only implemented on Windows.
func Acquire(name string) (*Lock, error) { return nil, errors.ErrUnsupported }

// Held is only implemented on Windows.
func Held(name string) (bool, error) { return false, errors.ErrUnsupported }
