package restic

import (
	"errors"
	"fmt"
)

// Errors matched with errors.Is against failures returned by this package.
var (
	ErrRepoNotFound  = errors.New("repository does not exist")
	ErrRepoLocked    = errors.New("repository is locked")
	ErrWrongPassword = errors.New("wrong repository password")
	// ErrIncomplete is restic exit code 3: some source files could not be
	// read, but the snapshot was created. Backup reports it as a warning
	// in BackupResult instead of returning it.
	ErrIncomplete  = errors.New("some source files could not be read")
	ErrInterrupted = errors.New("restic was interrupted")
	ErrTimeout     = errors.New("restic timed out")
	// ErrNothingMatched is a restore whose include patterns matched no file.
	ErrNothingMatched = errors.New("nothing in the snapshot matches")
)

// restic exit codes (restic 0.17+).
const (
	exitFatal       = 1
	exitIncomplete  = 3
	exitNoRepo      = 10
	exitLocked      = 11
	exitBadPassword = 12
	exitInterrupted = 130
)

// ExitError is a restic process that ended with a non-zero exit code.
type ExitError struct {
	Command string // restic subcommand, e.g. "backup"
	Code    int
	Message string // restic's own error message, if it printed one
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("restic %s failed with exit code %d", e.Command, e.Code)
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// Is maps exit codes to the sentinel errors of this package.
func (e *ExitError) Is(target error) bool {
	switch target {
	case ErrIncomplete:
		return e.Code == exitIncomplete
	case ErrRepoNotFound:
		return e.Code == exitNoRepo
	case ErrRepoLocked:
		return e.Code == exitLocked
	case ErrWrongPassword:
		return e.Code == exitBadPassword
	case ErrInterrupted:
		return e.Code == exitInterrupted
	}
	return false
}
