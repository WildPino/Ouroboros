package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WildPino/ouroboros"
	"github.com/WildPino/ouroboros/internal/backend/restic"
	"github.com/WildPino/ouroboros/internal/platform/windows"
)

const testConfig = `
[repository]
path = 'B:\ouroboros'

[space]
min_free = "10GB"

[[job]]
name = "docs"
paths = ['C:\Users\me\Documents']
interval = "6h"
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ouroboros.toml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func runCLI(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = run(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestVersionCommand(t *testing.T) {
	code, out, _ := runCLI("version")
	if code != exitOK || out != "ouroboros dev\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestConfigInitPrintsExample(t *testing.T) {
	code, out, _ := runCLI("config", "init")
	if code != exitOK || out != string(ouroboros.ExampleConfig) {
		t.Fatalf("code=%d, output differs from the embedded example", code)
	}
}

func TestConfigValidateMissingFile(t *testing.T) {
	code, _, errOut := runCLI("config", "validate", "--config", filepath.Join(t.TempDir(), "none.toml"))
	if code != exitConfig || !strings.Contains(errOut, "error:") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestConfigValidateInvalid(t *testing.T) {
	p := writeConfig(t, testConfig+"bogus = 1\n")
	code, _, errOut := runCLI("config", "validate", "--config", p)
	if code != exitConfig || !strings.Contains(errOut, "job.bogus") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestUnknownCommand(t *testing.T) {
	if code, _, _ := runCLI("frobnicate"); code != exitError {
		t.Fatalf("code=%d, want %d", code, exitError)
	}
}

type fakeDisk struct {
	total, free uint64
	err         error
}

func (d fakeDisk) DiskUsage(string) (uint64, uint64, error) { return d.total, d.free, d.err }

func TestValidateConfig(t *testing.T) {
	p := writeConfig(t, testConfig)
	tests := []struct {
		name string
		disk fakeDisk
		code int // 0 = nil error
	}{
		{"ok", fakeDisk{total: 100 << 30}, exitOK},
		{"partition too small", fakeDisk{total: 5 << 30}, exitConfig},
		{"partition unreachable", fakeDisk{err: errors.New("no such drive")}, exitWarnings},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			err := validateConfig(&options{configPath: p, stdout: &out, stderr: &errb}, tt.disk)
			if got := codeOf(err); got != tt.code {
				t.Fatalf("code=%d want %d (err=%v)", got, tt.code, err)
			}
		})
	}
}

func codeOf(err error) int {
	if err == nil {
		return exitOK
	}
	return exitCode(err)
}

type fakeEnv struct {
	fakeDisk
	elevated    bool
	elevatedErr error
	resticPath  string
	findErr     error
	sysRestic   string
	sysErr      error
	version     restic.Version
	versionErr  error
	writableErr error
	serviceErr  error

	gotConfiguredRestic string
	checkedServices     []string
}

func (e *fakeEnv) IsElevated() (bool, error) { return e.elevated, e.elevatedErr }
func (e *fakeEnv) FindRestic(configured string) (string, error) {
	e.gotConfiguredRestic = configured
	return e.resticPath, e.findErr
}
func (e *fakeEnv) FindResticForSystem() (string, error) { return e.sysRestic, e.sysErr }
func (e *fakeEnv) ResticVersion(context.Context, string) (restic.Version, error) {
	return e.version, e.versionErr
}
func (e *fakeEnv) CheckWritable(string) error { return e.writableErr }
func (e *fakeEnv) CheckService(name string) error {
	e.checkedServices = append(e.checkedServices, name)
	return e.serviceErr
}

func healthyEnv() *fakeEnv {
	return &fakeEnv{
		fakeDisk:   fakeDisk{total: 1 << 40, free: 500 << 30},
		elevated:   true,
		resticPath: `C:\restic\restic.exe`,
		sysRestic:  `C:\restic\restic.exe`,
		version:    restic.Version{Major: 0, Minor: 18, Patch: 0},
	}
}

func statusOf(results []checkResult, name string) (checkStatus, bool) {
	for _, r := range results {
		if r.name == name {
			return r.status, true
		}
	}
	return 0, false
}

func TestDoctor(t *testing.T) {
	good := writeConfig(t, testConfig)
	noVSS := writeConfig(t, testConfig+"vss = false\n")
	withRestic := writeConfig(t, strings.Replace(testConfig, "[space]", "restic_path = 'C:\\x\\restic.exe'\n\n[space]", 1))

	tests := []struct {
		name     string
		cfg      string
		mutate   func(e *fakeEnv)
		check    string
		want     checkStatus
		wantCode int
	}{
		{"all healthy", good, func(e *fakeEnv) {}, "vss", statusOK, exitOK},
		{"not elevated", good, func(e *fakeEnv) { e.elevated = false }, "admin", statusWarn, exitWarnings},
		{"restic missing", good, func(e *fakeEnv) { e.findErr = errors.New("not found") }, "restic", statusFail, exitError},
		{"restic broken", good, func(e *fakeEnv) { e.versionErr = errors.New("bad exe") }, "restic", statusFail, exitError},
		{"restic too old", good, func(e *fakeEnv) { e.version = restic.Version{Major: 0, Minor: 16, Patch: 4} }, "restic", statusFail, exitError},
		{"restic exactly minimum", good, func(e *fakeEnv) { e.version = restic.MinVersion }, "restic", statusOK, exitOK},
		{"repo not writable", good, func(e *fakeEnv) { e.writableErr = errors.New("access denied") }, "repository", statusFail, exitError},
		{"disk unreadable", good, func(e *fakeEnv) { e.fakeDisk.err = errors.New("no drive") }, "free space", statusFail, exitError},
		{"low free space", good, func(e *fakeEnv) { e.fakeDisk.free = 1 << 30 }, "free space", statusWarn, exitWarnings},
		{"partition smaller than min_free", good, func(e *fakeEnv) { e.fakeDisk.total, e.fakeDisk.free = 5<<30, 5<<30 }, "free space", statusFail, exitConfig},
		{"vss disabled", good, func(e *fakeEnv) { e.serviceErr = windows.ErrServiceDisabled }, "vss", statusFail, exitError},
		{"vss missing", good, func(e *fakeEnv) { e.serviceErr = errors.New("not installed") }, "vss", statusFail, exitError},
		{"configured restic path used", withRestic, func(e *fakeEnv) {}, "restic", statusOK, exitOK},
		{"restic on the system PATH", good, func(e *fakeEnv) {}, "task restic", statusOK, exitOK},
		{"restic only on the user PATH", good, func(e *fakeEnv) { e.sysErr = restic.ErrNotOnPath }, "task restic", statusFail, exitError},
		{"system PATH unreadable", good, func(e *fakeEnv) { e.sysErr = errors.New("registry") }, "task restic", statusWarn, exitWarnings},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := healthyEnv()
			tt.mutate(env)
			results, cfgErr := runDoctor(context.Background(), tt.cfg, env)
			got, ok := statusOf(results, tt.check)
			if !ok || got != tt.want {
				t.Fatalf("check %q: got %v (present=%v), want %v; results=%+v", tt.check, got, ok, tt.want, results)
			}
			var out bytes.Buffer
			if code := codeOf(reportDoctor(&out, results, cfgErr)); code != tt.wantCode {
				t.Fatalf("exit code %d, want %d\n%s", code, tt.wantCode, out.String())
			}
		})
	}

	t.Run("configured restic path passed through", func(t *testing.T) {
		env := healthyEnv()
		runDoctor(context.Background(), withRestic, env)
		if env.gotConfiguredRestic != `C:\x\restic.exe` {
			t.Fatalf("FindRestic got %q", env.gotConfiguredRestic)
		}
		// The task runs the configured path: the system PATH does not matter.
		env.sysErr = restic.ErrNotOnPath
		results, _ := runDoctor(context.Background(), withRestic, env)
		if _, ok := statusOf(results, "task restic"); ok {
			t.Fatalf("system PATH checked with restic_path set: %+v", results)
		}
	})

	t.Run("vss not checked when no job uses it", func(t *testing.T) {
		env := healthyEnv()
		results, _ := runDoctor(context.Background(), noVSS, env)
		if _, ok := statusOf(results, "vss"); ok || len(env.checkedServices) != 0 {
			t.Fatalf("vss checked: %+v", results)
		}
	})

	t.Run("invalid config", func(t *testing.T) {
		env := healthyEnv()
		results, cfgErr := runDoctor(context.Background(), filepath.Join(t.TempDir(), "none.toml"), env)
		if st, _ := statusOf(results, "config"); st != statusFail {
			t.Fatalf("config status %v", st)
		}
		// Checks that do not depend on the configuration still run.
		if _, ok := statusOf(results, "restic"); !ok {
			t.Fatal("restic check skipped")
		}
		if _, ok := statusOf(results, "repository"); ok {
			t.Fatal("repository check ran without a configuration")
		}
		var out bytes.Buffer
		if code := codeOf(reportDoctor(&out, results, cfgErr)); code != exitConfig {
			t.Fatalf("exit code %d, want %d", code, exitConfig)
		}
	})
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		n    uint64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{10 << 30, "10.0 GB"},
		{3 << 40, "3.0 TB"},
		{5000 << 40, "5000.0 TB"},
	}
	for _, tt := range tests {
		if got := formatBytes(tt.n); got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// TestConfigErrorExitCode checks that every command that reads the
// configuration exits with 2 when it is missing or invalid, before doing
// anything else.
func TestConfigErrorExitCode(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.toml")
	invalid := writeConfig(t, testConfig+"bogus = 1\n")
	commands := [][]string{
		{"run"}, {"run", "--job", "docs", "--force"}, {"status"}, {"snapshots"}, {"check"},
		{"pin", "abcd1234"}, {"unpin", "abcd1234"}, {"restore", "abcd1234", "--target", t.TempDir()},
		{"reclaim", "--dry-run"}, {"install"}, {"init"}, {"config", "validate"},
	}
	for _, cfg := range []string{missing, invalid} {
		for _, args := range commands {
			code, _, errOut := runCLI(append(args, "--config", cfg)...)
			if code != exitConfig {
				t.Errorf("%v with %s: code %d, want %d (stderr %q)", args, filepath.Base(cfg), code, exitConfig, errOut)
			}
		}
	}
}
