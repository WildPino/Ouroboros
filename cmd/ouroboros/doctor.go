package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/WildPino/ouroboros/internal/backend/restic"
	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/platform/windows"
)

type checkStatus int

const (
	statusOK checkStatus = iota
	statusWarn
	statusFail
)

func (s checkStatus) String() string {
	switch s {
	case statusOK:
		return " ok "
	case statusWarn:
		return "warn"
	default:
		return "FAIL"
	}
}

type checkResult struct {
	name   string
	status checkStatus
	detail string
}

// doctorEnv is everything doctor reads from the system, so that the checks
// can be tested without touching the real machine.
type doctorEnv interface {
	diskUsager
	IsElevated() (bool, error)
	FindRestic(configured string) (string, error)
	// FindResticForSystem finds restic on the machine PATH, which the
	// scheduled task searches when restic_path is empty.
	FindResticForSystem() (string, error)
	ResticVersion(ctx context.Context, exe string) (restic.Version, error)
	// CheckWritable verifies that a file can be created in dir, or in its
	// closest existing ancestor when dir does not exist yet.
	CheckWritable(dir string) error
	CheckService(name string) error
}

func newDoctorCmd(opts *options) *cobra.Command {
	return supportsJSON(&cobra.Command{
		Use:   "doctor",
		Short: "Check restic, permissions, VSS, configuration and free space",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			results, cfgErr := runDoctor(cmd.Context(), opts.configPath, platformEnv{})
			if opts.out != nil {
				opts.out.result(doctorJSON(results))
				return doctorOutcome(results, cfgErr)
			}
			return reportDoctor(opts.stdout, results, cfgErr)
		},
	})
}

// runDoctor performs every check. The returned error is the configuration
// error, if any, so that the exit code can tell it apart.
func runDoctor(ctx context.Context, cfgPath string, env doctorEnv) ([]checkResult, error) {
	var results []checkResult
	add := func(name string, st checkStatus, format string, args ...any) {
		results = append(results, checkResult{name, st, fmt.Sprintf(format, args...)})
	}

	cfg, cfgErr := config.Load(cfgPath)
	if cfgErr != nil {
		add("config", statusFail, "%v", cfgErr)
	} else {
		add("config", statusOK, "%s", cfgPath)
	}

	switch elevated, err := env.IsElevated(); {
	case err != nil:
		add("admin", statusWarn, "cannot determine privileges: %v", err)
	case !elevated:
		add("admin", statusWarn, "not running as Administrator; VSS and the scheduled task need it")
	default:
		add("admin", statusOK, "running as Administrator")
	}

	configured := ""
	if cfg != nil {
		configured = cfg.Repository.ResticPath
	}
	if exe, err := env.FindRestic(configured); err != nil {
		add("restic", statusFail, "%v", err)
	} else if v, err := env.ResticVersion(ctx, exe); err != nil {
		add("restic", statusFail, "%v", err)
	} else if !v.AtLeast(restic.MinVersion) {
		add("restic", statusFail, "%s is version %s, need %s or later", exe, v, restic.MinVersion)
	} else {
		add("restic", statusOK, "%s (%s)", exe, v)
		if configured == "" {
			switch sysExe, err := env.FindResticForSystem(); {
			case errors.Is(err, restic.ErrNotOnPath):
				add("task restic", statusFail, "not on the system PATH, so the scheduled task (it runs as SYSTEM) "+
					"would not find it: set restic_path = '%s' or install restic for all users", exe)
			case err != nil:
				add("task restic", statusWarn, "cannot check the system PATH: %v", err)
			default:
				add("task restic", statusOK, "the scheduled task will run %s", sysExe)
			}
		}
	}

	if cfg == nil {
		return results, cfgErr
	}

	repo := cfg.Repository.Path
	if err := env.CheckWritable(repo); err != nil {
		add("repository", statusFail, "%s is not writable: %v", repo, err)
	} else {
		add("repository", statusOK, "%s is writable", repo)
	}

	if total, free, err := env.DiskUsage(repo); err != nil {
		add("free space", statusFail, "cannot read partition of %s: %v", repo, err)
	} else if err := cfg.ValidateDisk(total); err != nil {
		add("free space", statusFail, "%v", err)
		cfgErr = err
	} else if minFree := cfg.Space.MinFree.Resolve(total); free < minFree {
		add("free space", statusWarn, "%s free of %s, below min_free %s; old snapshots will be removed",
			formatBytes(free), formatBytes(total), formatBytes(minFree))
	} else {
		add("free space", statusOK, "%s free of %s (min_free %s)",
			formatBytes(free), formatBytes(total), formatBytes(minFree))
	}

	if needsVSS(cfg) {
		switch err := env.CheckService("VSS"); {
		case errors.Is(err, windows.ErrServiceDisabled):
			add("vss", statusFail, "Volume Shadow Copy service is disabled; enable it or set vss = false")
		case err != nil:
			add("vss", statusFail, "Volume Shadow Copy service unavailable: %v", err)
		default:
			add("vss", statusOK, "Volume Shadow Copy service available")
		}
	}

	return results, cfgErr
}

func needsVSS(cfg *config.Config) bool {
	for _, j := range cfg.Jobs {
		if j.UseVSS() {
			return true
		}
	}
	return false
}

func reportDoctor(w io.Writer, results []checkResult, cfgErr error) error {
	for _, r := range results {
		// Multi-line details (validation problems) are indented under the check.
		detail := strings.ReplaceAll(r.detail, "\n", "\n"+strings.Repeat(" ", 20))
		fmt.Fprintf(w, "[%s] %-12s %s\n", r.status, r.name, detail)
	}
	return doctorOutcome(results, cfgErr)
}

// doctorOutcome maps the checks to the exit code: configuration errors
// first, then the worst check.
func doctorOutcome(results []checkResult, cfgErr error) error {
	worst := statusOK
	for _, r := range results {
		worst = max(worst, r.status)
	}
	switch {
	case cfgErr != nil:
		return &codedError{code: exitConfig}
	case worst == statusFail:
		return &codedError{code: exitError}
	case worst == statusWarn:
		return &codedError{code: exitWarnings}
	}
	return nil
}

func formatBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit && exp < 3; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

// platformEnv is the real doctorEnv.
type platformEnv struct{ platformDisk }

func (platformEnv) IsElevated() (bool, error) { return windows.IsElevated() }

func (platformEnv) FindRestic(configured string) (string, error) {
	return restic.FindExecutable(configured)
}

func (platformEnv) FindResticForSystem() (string, error) {
	sp, err := windows.SystemPath()
	if err != nil {
		return "", err
	}
	return restic.FindOnPath(sp)
}

func (platformEnv) ResticVersion(ctx context.Context, exe string) (restic.Version, error) {
	return restic.ExecutableVersion(ctx, exe)
}

func (platformEnv) CheckWritable(dir string) error { return windows.CheckDirWritable(dir) }

func (platformEnv) CheckService(name string) error { return windows.CheckService(name) }

// platformDisk is the real diskUsager.
type platformDisk struct{}

func (platformDisk) DiskUsage(path string) (total, free uint64, err error) {
	return windows.DiskUsage(path)
}

// Schema of "doctor --json"; documented in docs/JSON.md.
type doctorOut struct {
	Checks []doctorCheckOut `json:"checks"`
}

type doctorCheckOut struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func doctorJSON(results []checkResult) doctorOut {
	out := doctorOut{Checks: []doctorCheckOut{}}
	for _, r := range results {
		st := map[checkStatus]string{statusOK: "ok", statusWarn: "warn", statusFail: "fail"}[r.status]
		out.Checks = append(out.Checks, doctorCheckOut{Name: r.name, Status: st, Detail: r.detail})
	}
	return out
}
