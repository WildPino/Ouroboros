//go:build !windows

package logging

import "errors"

// RegisterSource is only implemented on Windows.
func RegisterSource() error { return errors.ErrUnsupported }

// UnregisterSource is only implemented on Windows.
func UnregisterSource() error { return errors.ErrUnsupported }

// OpenEventLog is only implemented on Windows.
func OpenEventLog() (EventSink, func() error, error) { return nil, nil, errors.ErrUnsupported }
