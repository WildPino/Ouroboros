package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/WildPino/ouroboros/internal/platform/windows"
)

func TestPauseResume(t *testing.T) {
	ctx := context.Background()
	c := newInstallCase(t)
	if err := runPause(ctx, c.opts, c.deps, true); !errors.Is(err, errTaskNotInstalled) {
		t.Fatalf("pause without task: %v", err)
	}
	if err := runInstall(ctx, c.opts, c.deps); err != nil {
		t.Fatal(err)
	}
	state := func() windows.TaskState {
		st, _ := c.tasks.State(ctx, windows.TaskName)
		return st
	}

	if err := runPause(ctx, c.opts, c.deps, true); err != nil || state() != windows.TaskDisabled {
		t.Fatalf("pause: %v, state %v", err, state())
	}
	// Reinstalling keeps the pause.
	c.out.Reset()
	if err := runInstall(ctx, c.opts, c.deps); err != nil || state() != windows.TaskDisabled ||
		!strings.Contains(c.out.String(), "stays paused") {
		t.Fatalf("reinstall: %v, state %v, %s", err, state(), c.out)
	}
	// Pausing twice is not an error.
	c.errOut.Reset()
	if err := runPause(ctx, c.opts, c.deps, true); err != nil || !strings.Contains(c.errOut.String(), "already paused") {
		t.Fatalf("second pause: %v, %s", err, c.errOut)
	}

	c.out.Reset()
	c.opts.out = &jsonLines{w: c.out, command: "resume"}
	if err := runPause(ctx, c.opts, c.deps, false); err != nil || state() != windows.TaskEnabled {
		t.Fatalf("resume: %v, state %v", err, state())
	}
	var line struct {
		Data pauseOut `json:"data"`
	}
	if err := json.Unmarshal(c.out.Bytes(), &line); err != nil || line.Data.Paused {
		t.Fatalf("json %s: %v", c.out, err)
	}

	c.deps.isElevated = func() (bool, error) { return false, nil }
	err := runPause(ctx, c.opts, c.deps, true)
	if errorCode(err) != "not_elevated" || state() != windows.TaskEnabled {
		t.Fatalf("not elevated: %v (%s)", err, errorCode(err))
	}
	if errorCode(errTaskNotInstalled) != "task_not_installed" {
		t.Fatal("task_not_installed code")
	}
}
