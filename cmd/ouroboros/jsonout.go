package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/WildPino/ouroboros/internal/backend/restic"
	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/lock"
	"github.com/WildPino/ouroboros/internal/space"
)

// jsonAnnotation marks commands that support --json; the flag is refused
// elsewhere so a caller never parses human text by mistake.
const jsonAnnotation = "ouroboros/json"

func supportsJSON(cmd *cobra.Command) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[jsonAnnotation] = "yes"
	return cmd
}

// jsonLines writes the --json protocol (docs/JSON.md): any number of event
// lines, then exactly one "result" or "error" line.
type jsonLines struct {
	mu      sync.Mutex
	w       io.Writer
	command string
	ended   bool
}

type jsonLine struct {
	Type    string     `json:"type"`
	Command string     `json:"command"`
	Time    time.Time  `json:"time"`
	Event   string     `json:"event,omitempty"`
	Data    any        `json:"data,omitempty"`
	Error   *jsonError `json:"error,omitempty"`
}

type jsonError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (j *jsonLines) write(l jsonLine) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.ended {
		return
	}
	if l.Type != "event" {
		j.ended = true
	}
	l.Command, l.Time = j.command, time.Now().UTC()
	b, err := json.Marshal(l)
	if err != nil {
		b, _ = json.Marshal(jsonLine{Type: "error", Command: j.command, Time: l.Time,
			Error: &jsonError{Code: "failed", Message: "cannot encode output: " + err.Error()}})
		j.ended = true
	}
	j.w.Write(append(b, '\n'))
}

// event reports progress; it is ignored after the final line.
func (j *jsonLines) event(name string, data any) {
	j.write(jsonLine{Type: "event", Event: name, Data: data})
}

func (j *jsonLines) result(data any) { j.write(jsonLine{Type: "result", Data: data}) }

func (j *jsonLines) fail(err error) {
	j.write(jsonLine{Type: "error", Error: &jsonError{Code: errorCode(err), Message: err.Error()}})
}

// errorCode classifies an error for programs reading --json output.
func errorCode(err error) string {
	switch {
	case errors.Is(err, config.ErrInvalid):
		return "config_invalid"
	case errors.Is(err, errRunInProgress), errors.Is(err, lock.ErrLocked):
		return "locked"
	case errors.Is(err, restic.ErrWrongPassword):
		return "wrong_password"
	case errors.Is(err, restic.ErrRepoNotFound):
		return "repository_unavailable"
	case errors.Is(err, restic.ErrRepoLocked):
		return "repository_locked"
	case errors.Is(err, restic.ErrNothingMatched):
		return "nothing_matched"
	case errors.Is(err, restic.ErrTimeout):
		return "timeout"
	case errors.Is(err, space.ErrBackupTooLarge):
		return "backup_too_large"
	case errors.Is(err, space.ErrCannotReclaim):
		return "cannot_reclaim"
	case errors.Is(err, errCheckFailed):
		return "check_failed"
	case errors.Is(err, errNotElevated):
		return "not_elevated"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	default:
		return "failed"
	}
}

// timeOrNil keeps unknown times out of the output instead of writing
// Go's zero time.
func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}
