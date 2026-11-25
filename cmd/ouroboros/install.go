package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/WildPino/ouroboros/internal/backend/restic"
	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/logging"
	"github.com/WildPino/ouroboros/internal/platform/windows"
	"github.com/WildPino/ouroboros/internal/secret"
)

// taskScheduler registers the scheduled task, replaceable in tests.
type taskScheduler interface {
	Create(ctx context.Context, name string, def []byte) error
	Delete(ctx context.Context, name string) error
	State(ctx context.Context, name string) (windows.TaskState, error)
	SetEnabled(ctx context.Context, name string, enabled bool) error
}

type systemTasks struct{}

func (systemTasks) Create(ctx context.Context, name string, def []byte) error {
	return windows.CreateTask(ctx, name, def)
}

func (systemTasks) Delete(ctx context.Context, name string) error {
	return windows.DeleteTask(ctx, name)
}

func (systemTasks) State(ctx context.Context, name string) (windows.TaskState, error) {
	return windows.QueryTask(ctx, name)
}

func (systemTasks) SetEnabled(ctx context.Context, name string, enabled bool) error {
	return windows.SetTaskEnabled(ctx, name, enabled)
}

type installDeps struct {
	isElevated       func() (bool, error)
	executable       func() (string, error)
	findRestic       func(configured string) (string, error)
	systemPath       func() (string, error)
	untrustedWriters func(path string) ([]string, error)
	tasks            taskScheduler
	now              func() time.Time
	tempDir          func() string
	registerSource   func() error
	unregisterSource func() error
}

func defaultInstallDeps() installDeps {
	return installDeps{
		isElevated:       windows.IsElevated,
		executable:       os.Executable,
		findRestic:       restic.FindExecutable,
		systemPath:       windows.SystemPath,
		untrustedWriters: windows.UntrustedWriters,
		tasks:            systemTasks{},
		now:              time.Now,
		tempDir:          os.TempDir,
		registerSource:   logging.RegisterSource,
		unregisterSource: logging.UnregisterSource,
	}
}

func newInstallCmd(opts *options) *cobra.Command {
	return supportsJSON(&cobra.Command{
		Use:   "install",
		Short: "Install the scheduled task that runs the backups",
		Long: "Register the scheduled task " + windows.TaskName + ", which runs \"ouroboros run\" as\n" +
			"SYSTEM every schedule.tick, catching up on runs missed while the computer was off,\n" +
			"and registers the Event Log source " + logging.Source + " when logging.event_log is on.\n" +
			"Running install again updates the task and keeps a pause. Must be run as Administrator.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(cmd.Context(), opts, defaultInstallDeps())
		},
	})
}

func newUninstallCmd(opts *options) *cobra.Command {
	return supportsJSON(&cobra.Command{
		Use:   "uninstall",
		Short: "Remove the scheduled task",
		Long: "Remove the scheduled task " + windows.TaskName + ". The repository, the configuration\n" +
			"and the stored password are left untouched.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUninstall(cmd.Context(), opts, defaultInstallDeps())
		},
	})
}

func requireElevation(d installDeps, what string) error {
	elevated, err := d.isElevated()
	if err != nil {
		return fmt.Errorf("check privileges: %w", err)
	}
	if !elevated {
		return fmt.Errorf("%s must be run as Administrator: %w", what, errAdminRequired)
	}
	return nil
}

func runInstall(ctx context.Context, opts *options, d installDeps) error {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return err
	}
	if err := requireElevation(d, "install"); err != nil {
		return err
	}
	cfgPath, err := filepath.Abs(opts.configPath)
	if err != nil {
		return err
	}
	if exists, err := secret.NewStore(secretPath(cfgPath)).Exists(); err != nil {
		return err
	} else if !exists {
		return errors.New("no repository password is stored: run \"ouroboros init\" first")
	}
	exe, err := d.executable()
	if err != nil {
		return fmt.Errorf("locate ouroboros executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	// "go run" and similar leave the binary in a temporary directory that
	// disappears; the task would then fail on every tick.
	// Compare with the temporary directory as given and resolved: either
	// path may use short 8.3 names (C:\Users\RUNNER~1) and only paths that
	// exist can be resolved.
	tmp := d.tempDir()
	inTemp := config.IsWithin(exe, tmp)
	if resolved, err := filepath.EvalSymlinks(tmp); err == nil && config.IsWithin(exe, resolved) {
		inTemp = true
	}
	if inTemp {
		return fmt.Errorf("%s is in a temporary directory: copy ouroboros.exe to a permanent location first", exe)
	}
	resticExe, err := d.findRestic(cfg.Repository.ResticPath)
	if err != nil {
		return err
	}
	if cfg.Repository.ResticPath == "" {
		if resticExe, err = resticForSystem(d.systemPath, resticExe, cfgPath); err != nil {
			return err
		}
	}

	warnings := installWarnings(d, exe, resticExe, cfgPath)
	if cfg.Logging.EventLog {
		if err := d.registerSource(); err != nil {
			warnings = append(warnings, fmt.Sprintf("cannot register the Event Log source %s: %v", logging.Source, err))
		}
	}

	// Reinstalling, for example after a change of schedule.tick, must not
	// silently resume backups the user paused.
	prev, err := d.tasks.State(ctx, windows.TaskName)
	if err != nil {
		return err
	}
	paused := prev == windows.TaskDisabled
	def, err := windows.TaskXML(windows.TaskSpec{
		Command:      exe,
		Arguments:    `run --config "` + cfgPath + `"`,
		Interval:     cfg.Schedule.Tick.Std(),
		RunOnBattery: cfg.Schedule.RunOnBattery,
		Start:        d.now().Truncate(time.Minute),
		Description:  "Runs due Ouroboros backups and maintenance.",
		Paused:       paused,
	})
	if err != nil {
		return err
	}
	if err := d.tasks.Create(ctx, windows.TaskName, def); err != nil {
		return err
	}
	if opts.out != nil {
		if warnings == nil {
			warnings = []string{}
		}
		opts.out.result(installOut{Task: windows.TaskName, Command: exe, Paused: paused, Warnings: warnings})
	} else {
		fmt.Fprintf(opts.stdout, "Installed scheduled task %s: runs every %s as SYSTEM.\n", windows.TaskName, cfg.Schedule.Tick.Std())
		fmt.Fprintf(opts.stdout, "  %s run --config \"%s\"\n", exe, cfgPath)
		if paused {
			fmt.Fprintln(opts.stdout, `The task stays paused, as it was; "ouroboros resume" restarts automatic backups.`)
		}
	}
	if len(warnings) > 0 {
		for _, w := range warnings {
			fmt.Fprintln(opts.stderr, "warning:", w)
		}
		return &codedError{code: exitWarnings}
	}
	return nil
}

// resticForSystem returns the restic that the scheduled task will run when
// restic_path is empty. The task runs as SYSTEM, which searches only the
// machine PATH: restic installed for one user, as winget does by default,
// is found from that user's console but not by the task, which would then
// fail on every tick.
func resticForSystem(systemPath func() (string, error), found, cfgPath string) (string, error) {
	sp, err := systemPath()
	if err != nil {
		return "", err
	}
	exe, err := restic.FindOnPath(sp)
	if err != nil {
		return "", fmt.Errorf("restic was found at %s, but not on the system PATH that the scheduled task uses (it runs as SYSTEM): "+
			"set restic_path = '%s' in %s, preferably after moving restic.exe to a folder only administrators can change, "+
			"or install restic for all users", found, found, cfgPath)
	}
	return exe, nil
}

// installWarnings reports files that the SYSTEM task executes or trusts
// and that accounts other than the administrators can modify: whatever
// they put there would run as SYSTEM.
func installWarnings(d installDeps, exe, resticExe, cfgPath string) []string {
	paths := []string{exe, filepath.Dir(exe), resticExe, filepath.Dir(resticExe), cfgPath, filepath.Dir(cfgPath)}
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		writers, err := d.untrustedWriters(p)
		if err != nil {
			out = append(out, fmt.Sprintf("cannot check the permissions of %s: %v", p, err))
			continue
		}
		if len(writers) > 0 {
			out = append(out, fmt.Sprintf("%s can be modified by %v, and the task runs it as SYSTEM: "+
				"move it to a folder only administrators can change, such as Program Files", p, writers))
		}
	}
	return out
}

func runUninstall(ctx context.Context, opts *options, d installDeps) error {
	if err := requireElevation(d, "uninstall"); err != nil {
		return err
	}
	st, err := d.tasks.State(ctx, windows.TaskName)
	if err != nil {
		return err
	}
	if st == windows.TaskMissing {
		if opts.out != nil {
			opts.out.result(uninstallOut{Removed: false, Warnings: []string{}})
		} else {
			fmt.Fprintf(opts.stdout, "Scheduled task %s is not installed.\n", windows.TaskName)
		}
		return nil
	}
	if err := d.tasks.Delete(ctx, windows.TaskName); err != nil {
		return err
	}
	var warnings []string
	if err := d.unregisterSource(); err != nil {
		warnings = append(warnings, fmt.Sprintf("cannot remove the Event Log source %s: %v", logging.Source, err))
	}
	if opts.out != nil {
		opts.out.result(uninstallOut{Removed: true, Warnings: append([]string{}, warnings...)})
	} else {
		fmt.Fprintf(opts.stdout, "Removed scheduled task %s.\n", windows.TaskName)
	}
	for _, w := range warnings {
		fmt.Fprintln(opts.stderr, "warning:", w)
	}
	if len(warnings) > 0 {
		return &codedError{code: exitWarnings}
	}
	return nil
}

// Schema of "install --json" and "uninstall --json"; documented in
// docs/JSON.md.
type installOut struct {
	Task     string   `json:"task"`
	Command  string   `json:"command"`
	Paused   bool     `json:"paused"`
	Warnings []string `json:"warnings"`
}

type uninstallOut struct {
	Removed  bool     `json:"removed"`
	Warnings []string `json:"warnings"`
}
