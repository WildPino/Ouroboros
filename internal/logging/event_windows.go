//go:build windows

package logging

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/eventlog"
)

const eventTypes = eventlog.Error | eventlog.Warning | eventlog.Info

// RegisterSource registers the event source in the Application log. It
// requires elevation; an existing registration is kept.
func RegisterSource() error {
	err := eventlog.InstallAsEventCreate(Source, eventTypes)
	if err != nil && isAlreadyRegistered(err) {
		return nil
	}
	return err
}

// UnregisterSource removes the event source; a missing one is not an error.
func UnregisterSource() error {
	err := eventlog.Remove(Source)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	return err
}

func isAlreadyRegistered(err error) bool {
	// eventlog.Install reports an existing key only through this message,
	// which is not localized.
	return strings.HasSuffix(err.Error(), `\`+Source+" registry key already exists")
}

// OpenEventLog opens the event source for reporting.
func OpenEventLog() (EventSink, func() error, error) {
	l, err := eventlog.Open(Source)
	if err != nil {
		return nil, nil, err
	}
	return l, l.Close, nil
}
