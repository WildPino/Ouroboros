package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"

	"github.com/WildPino/ouroboros/internal/config"
	platform "github.com/WildPino/ouroboros/internal/platform/windows"
)

// UninstallResult describes a completed uninstallation.
type UninstallResult struct {
	// RestartNeeded means some files, such as the running Ouroboros.exe,
	// are deleted at the next restart.
	RestartNeeded bool
	// DataRemoved means the configuration, password, state and logs are
	// gone.
	DataRemoved bool
	// Notes are things the user should know; none of them is an error.
	Notes []string
}

// Uninstall removes the scheduled task, the Event Log source, the PATH
// entry, the shortcut, the "Installed apps" entry and the installed files.
// With removeData it also removes DataDir: the configuration, the
// password, the state and the logs. The repository is never touched.
func (in *Installer) Uninstall(ctx context.Context, removeData bool) (res UninstallResult, err error) {
	l, err := in.AcquireLock(ctx, in.LockName)
	if err != nil {
		return res, err
	}
	defer l.Release()

	// A task left behind would fail on every tick once the files are gone,
	// so nothing is removed unless the task is.
	st, err := in.TaskState(ctx)
	if err != nil {
		return res, fmt.Errorf("check the scheduled task: %w", err)
	}
	if st != platform.TaskMissing {
		if err := in.DeleteTask(ctx); err != nil {
			return res, fmt.Errorf("remove the scheduled task: %w", err)
		}
	}
	note := func(format string, args ...any) { res.Notes = append(res.Notes, fmt.Sprintf(format, args...)) }
	if err := in.UnregisterSource(); err != nil {
		note("the Event Log source could not be removed: %v", err)
	}
	if changed, err := in.editPath(removePathEntry, false); err != nil {
		note("%v", err)
	} else if changed {
		in.EnvironmentChanged()
	}
	if err := os.Remove(in.shortcut()); err != nil && !os.IsNotExist(err) {
		note("the Start menu shortcut could not be removed: %v", err)
	}
	if err := registry.DeleteKey(in.Registry, in.UninstallKey); err != nil && !errors.Is(err, registry.ErrNotExist) {
		note("the Installed apps entry could not be removed: %v", err)
	}

	// Files in use, such as this Ouroboros.exe when it runs from ProgramDir,
	// go at the next restart, then the folders they leave empty.
	var busy []string
	for _, p := range installedFiles {
		if err := os.Remove(in.path(p)); err != nil && !os.IsNotExist(err) {
			busy = append(busy, in.path(p))
		}
	}
	dirs := []string{in.binDir(), filepath.Join(in.ProgramDir, "restic"), in.ProgramDir}
	for _, d := range dirs {
		busy = append(busy, cleanLeftovers(d, true)...)
	}
	for _, p := range busy {
		if err := in.DeleteAtReboot(p); err != nil {
			note("%s could not be removed: %v", p, err)
			continue
		}
		res.RestartNeeded = true
	}
	for _, d := range dirs {
		err := os.Remove(d)
		if err == nil || os.IsNotExist(err) {
			continue
		}
		if res.RestartNeeded && in.DeleteAtReboot(d) == nil {
			continue
		}
		note("%s was kept: it holds other files", d)
	}

	if removeData {
		removed, why := in.removeData()
		res.DataRemoved = removed
		if why != "" {
			note("%s", why)
		}
	}
	return res, nil
}

// removeData removes DataDir unless the repository is inside it.
func (in *Installer) removeData() (bool, string) {
	if _, err := os.Stat(in.DataDir); os.IsNotExist(err) {
		return true, ""
	}
	cfg := in.configPath()
	if _, err := os.Stat(cfg); err == nil {
		c, err := config.Load(cfg)
		if err != nil {
			return false, fmt.Sprintf("%s was kept: the configuration cannot be read to make sure the repository is not inside it: %v", in.DataDir, err)
		}
		if config.IsWithin(c.Repository.Path, in.DataDir) {
			return false, fmt.Sprintf("%s was kept: the repository is inside it", in.DataDir)
		}
	}
	if err := os.RemoveAll(in.DataDir); err != nil {
		return false, fmt.Sprintf("%s could not be removed: %v", in.DataDir, err)
	}
	return true, ""
}
