package gui

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"unicode"

	"github.com/WildPino/ouroboros/internal/config"
)

// AppInfo is the result of app.info.
type AppInfo struct {
	Version          string `json:"version"`
	InstalledVersion string `json:"installedVersion"`
	SetupNeeded      bool   `json:"setupNeeded"`
}

// table lists the calls of the contract (docs/DESIGN.md §12.8, question 7)
// that this version implements. The others come with the screens that use
// them.
func (b *Bridge) table() map[string]handler {
	return map[string]handler{
		"app.info":          b.appInfo,
		"dialog.pickFolder": b.pickFolder,
		"status.get":        b.statusGet,
		"job.backupNow":     b.backupNow,
		"task.pause":        b.pauseResume("pause"),
		"task.resume":       b.pauseResume("resume"),
		"snapshots.list":    b.snapshotsList,
		"snapshots.ls":      b.snapshotsLs,
	}
}

func (b *Bridge) appInfo(_ context.Context, raw json.RawMessage) (any, error) {
	if _, err := args[noArgs](raw); err != nil {
		return nil, err
	}
	return b.deps.Info()
}

func (b *Bridge) pickFolder(ctx context.Context, raw json.RawMessage) (any, error) {
	a, err := args[struct {
		Start *string `json:"start"`
	}](raw)
	if err != nil {
		return nil, err
	}
	start := ""
	if a.Start != nil {
		start = *a.Start
		if start != "" && !isWindowsPath(start) {
			return nil, invalid("start must be an absolute folder path")
		}
	}
	path, err := b.deps.PickFolder(ctx, start)
	if err != nil {
		return nil, err
	}
	return struct {
		Path string `json:"path"`
	}{path}, nil
}

func (b *Bridge) statusGet(ctx context.Context, raw json.RawMessage) (any, error) {
	a, err := args[struct {
		WithSnapshots *bool `json:"withSnapshots"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if a.WithSnapshots == nil {
		return nil, invalid("withSnapshots is required")
	}
	cmd := []string{"status", "--json"}
	if !*a.WithSnapshots {
		cmd = append(cmd, "--no-snapshots")
	}
	return b.deps.CLI.Run(ctx, cmd, nil, nil)
}

// backupNow starts "run" for one job, detached from the window
// (question 8). A run already holding the lock is reported as locked
// without starting another; if one takes the lock in between, the started
// command ends at once and its result arrives as a "backup" event.
func (b *Bridge) backupNow(ctx context.Context, raw json.RawMessage) (any, error) {
	a, err := args[struct {
		Job string `json:"job"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if !config.ValidJobName(a.Job) {
		return nil, invalid("job must be a job name")
	}
	st, err := b.deps.CLI.Run(ctx, []string{"status", "--json", "--no-snapshots"}, nil, nil)
	if err != nil {
		return nil, err
	}
	var s struct {
		Running *bool `json:"running"`
	}
	if err := json.Unmarshal(st, &s); err != nil {
		return nil, err
	}
	if s.Running != nil && *s.Running {
		return nil, &Error{Code: CodeLocked, Message: "A backup is running."}
	}
	job := a.Job
	err = b.deps.CLI.Start([]string{"run", "--json", "--force", "--job", job}, func(data json.RawMessage, err error) {
		if b.deps.Emit == nil {
			return
		}
		ev := struct {
			Job    string          `json:"job"`
			Result json.RawMessage `json:"result,omitempty"`
			Error  *Error          `json:"error,omitempty"`
		}{Job: job, Result: data}
		if err != nil {
			ev.Error = toError(err)
		}
		b.deps.Emit(Event{Event: "backup", Data: ev})
	})
	if err != nil {
		return nil, err
	}
	return struct {
		Started bool `json:"started"`
	}{true}, nil
}

func (b *Bridge) pauseResume(command string) handler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		if _, err := args[noArgs](raw); err != nil {
			return nil, err
		}
		return b.deps.CLI.Run(ctx, []string{command, "--json"}, nil, nil)
	}
}

func (b *Bridge) snapshotsList(ctx context.Context, raw json.RawMessage) (any, error) {
	a, err := args[struct {
		Job string `json:"job"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if !config.ValidJobName(a.Job) {
		return nil, invalid("job must be a job name")
	}
	return b.deps.CLI.Run(ctx, []string{"snapshots", "--json", "--job", a.Job}, nil, nil)
}

var snapshotID = regexp.MustCompile(`^[0-9a-f]{8,64}$`)

func (b *Bridge) snapshotsLs(ctx context.Context, raw json.RawMessage) (any, error) {
	a, err := args[struct {
		Snapshot string  `json:"snapshot"`
		Folder   *string `json:"folder"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if !snapshotID.MatchString(a.Snapshot) {
		return nil, invalid("snapshot must be a snapshot id")
	}
	cmd := []string{"ls", "--json", "--", a.Snapshot}
	if a.Folder != nil && *a.Folder != "" {
		if !isWindowsPath(*a.Folder) {
			return nil, invalid("folder must be an absolute path")
		}
		cmd = append(cmd, *a.Folder)
	}
	return b.deps.CLI.Run(ctx, cmd, nil, nil)
}

// isWindowsPath accepts a local absolute path such as C:\Users, without
// control characters.
func isWindowsPath(p string) bool {
	if len(p) < 3 || len(p) > 32767 || p[1] != ':' || p[2] != '\\' {
		return false
	}
	c := p[0] | 0x20
	if c < 'a' || c > 'z' {
		return false
	}
	return !strings.ContainsFunc(p, unicode.IsControl)
}
