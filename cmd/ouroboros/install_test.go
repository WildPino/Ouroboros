package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/WildPino/ouroboros/internal/platform/windows"
	"github.com/WildPino/ouroboros/internal/secret"
)

type fakeTasks struct {
	created map[string][]byte
	deleted []string
	err     error
}

func (f *fakeTasks) Create(_ context.Context, name string, def []byte) error {
	if f.err != nil {
		return f.err
	}
	if f.created == nil {
		f.created = map[string][]byte{}
	}
	f.created[name] = def
	return nil
}

func (f *fakeTasks) Delete(_ context.Context, name string) error {
	f.deleted = append(f.deleted, name)
	delete(f.created, name)
	return nil
}

func (f *fakeTasks) Exists(_ context.Context, name string) (bool, error) {
	_, ok := f.created[name]
	return ok, nil
}

func decodeUTF16(t *testing.T, b []byte) string {
	t.Helper()
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xFE {
		t.Fatal("not UTF-16LE")
	}
	u := make([]uint16, (len(b)-2)/2)
	for i := range u {
		u[i] = uint16(b[2+2*i]) | uint16(b[3+2*i])<<8
	}
	return string(utf16.Decode(u))
}

type installCase struct {
	opts    *options
	out     *bytes.Buffer
	errOut  *bytes.Buffer
	cfgPath string
	exe     string
	progDir string
	tasks   *fakeTasks
	deps    installDeps

	registered, unregistered int
}

func newInstallCase(t *testing.T) *installCase {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "data")
	progDir := filepath.Join(root, "programs")
	for _, d := range []string{cfgDir, progDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfgPath := filepath.Join(cfgDir, "ouroboros.toml")
	if err := os.WriteFile(cfgPath, []byte(testConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, secret.FileName), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// restic installed for all users: on the machine PATH.
	if err := os.WriteFile(filepath.Join(progDir, "restic.exe"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	c := &installCase{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}, cfgPath: cfgPath,
		exe: filepath.Join(progDir, "ouroboros.exe"), progDir: progDir, tasks: &fakeTasks{}}
	c.opts = &options{configPath: cfgPath, stdout: c.out, stderr: c.errOut}
	c.deps = installDeps{
		isElevated:       func() (bool, error) { return true, nil },
		executable:       func() (string, error) { return c.exe, nil },
		findRestic:       func(string) (string, error) { return filepath.Join(progDir, "restic.exe"), nil },
		systemPath:       func() (string, error) { return filepath.Join(root, "missing") + ";" + progDir, nil },
		untrustedWriters: func(string) ([]string, error) { return nil, nil },
		tasks:            c.tasks,
		now:              func() time.Time { return time.Date(2026, 5, 1, 10, 7, 33, 0, time.Local) },
		tempDir:          func() string { return filepath.Join(root, "tmp") },
		registerSource:   func() error { c.registered++; return nil },
		unregisterSource: func() error { c.unregistered++; return nil },
	}
	return c
}

func TestInstall(t *testing.T) {
	c := newInstallCase(t)
	if err := runInstall(context.Background(), c.opts, c.deps); err != nil {
		t.Fatalf("%v\n%s", err, c.errOut)
	}
	def, ok := c.tasks.created[windows.TaskName]
	if !ok {
		t.Fatal("task not created")
	}
	doc := decodeUTF16(t, def)
	for _, want := range []string{
		"<Command>" + c.exe + "</Command>",
		"<Arguments>run --config &#34;" + c.cfgPath + "&#34;</Arguments>",
		"<Interval>PT15M</Interval>",
		"<StartBoundary>2026-05-01T10:07:00</StartBoundary>",
		"<UserId>S-1-5-18</UserId>",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("task lacks %q:\n%s", want, doc)
		}
	}
	if !strings.Contains(c.out.String(), "Installed scheduled task") || c.errOut.Len() != 0 {
		t.Fatalf("stdout %q stderr %q", c.out, c.errOut)
	}

	// Installing again replaces the task; uninstall removes it.
	if err := runInstall(context.Background(), c.opts, c.deps); err != nil {
		t.Fatal(err)
	}
	c.out.Reset()
	if err := runUninstall(context.Background(), c.opts, c.deps); err != nil {
		t.Fatal(err)
	}
	if len(c.tasks.deleted) != 1 || !strings.Contains(c.out.String(), "Removed") {
		t.Fatalf("deleted %v, stdout %q", c.tasks.deleted, c.out)
	}
	if c.registered != 2 || c.unregistered != 1 {
		t.Fatalf("event source registered %d, unregistered %d", c.registered, c.unregistered)
	}
	c.out.Reset()
	if err := runUninstall(context.Background(), c.opts, c.deps); err != nil {
		t.Fatal(err)
	}
	if len(c.tasks.deleted) != 1 || !strings.Contains(c.out.String(), "not installed") {
		t.Fatalf("deleted %v, stdout %q", c.tasks.deleted, c.out)
	}
}

func TestInstallWarnsAboutWritablePrograms(t *testing.T) {
	c := newInstallCase(t)
	progDir := filepath.Dir(c.exe)
	c.deps.untrustedWriters = func(p string) ([]string, error) {
		if p == progDir {
			return []string{`BUILTIN\Users`}, nil
		}
		return nil, nil
	}
	err := runInstall(context.Background(), c.opts, c.deps)
	if exitCode(err) != exitWarnings {
		t.Fatalf("got %v", err)
	}
	if _, ok := c.tasks.created[windows.TaskName]; !ok {
		t.Fatal("the task is installed despite the warning")
	}
	// The directory holds both executables but is reported once.
	if n := strings.Count(c.errOut.String(), "warning:"); n != 1 || !strings.Contains(c.errOut.String(), `BUILTIN\Users`) {
		t.Fatalf("stderr %q", c.errOut)
	}
}

func TestInstallRefusals(t *testing.T) {
	tests := []struct {
		name  string
		setup func(c *installCase)
		want  string
	}{
		{"not elevated", func(c *installCase) {
			c.deps.isElevated = func() (bool, error) { return false, nil }
		}, "Administrator"},
		{"no password", func(c *installCase) {
			os.Remove(secretPath(c.cfgPath))
		}, "ouroboros init"},
		{"temporary executable", func(c *installCase) {
			c.deps.tempDir = func() string { return filepath.Dir(c.exe) }
		}, "temporary directory"},
		{"restic missing", func(c *installCase) {
			c.deps.findRestic = func(string) (string, error) { return "", errors.New("restic not found") }
		}, "restic not found"},
		{"schtasks fails", func(c *installCase) {
			c.tasks.err = errors.New("access denied")
		}, "access denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newInstallCase(t)
			tt.setup(c)
			err := runInstall(context.Background(), c.opts, c.deps)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
			if tt.name != "schtasks fails" && len(c.tasks.created) != 0 {
				t.Fatal("task created")
			}
		})
	}
}

func TestInstallNeedsResticForSystem(t *testing.T) {
	c := newInstallCase(t)
	userDir := filepath.Join(filepath.Dir(c.progDir), "user", "WinGet", "Links")
	userRestic := filepath.Join(userDir, "restic.exe")
	c.deps.findRestic = func(string) (string, error) { return userRestic, nil }
	c.deps.systemPath = func() (string, error) { return filepath.Join(filepath.Dir(c.progDir), "system32"), nil }
	err := runInstall(context.Background(), c.opts, c.deps)
	if err == nil || !strings.Contains(err.Error(), "restic_path = '"+userRestic+"'") || !strings.Contains(err.Error(), "SYSTEM") {
		t.Fatalf("got %v", err)
	}
	if len(c.tasks.created) != 0 {
		t.Fatal("task created although SYSTEM cannot find restic")
	}

	// An explicit restic_path is what the task runs; the PATH is not used.
	cfg := strings.Replace(testConfig, "[repository]\n", "[repository]\nrestic_path = '"+userRestic+"'\n", 1)
	if err := os.WriteFile(c.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	c.deps.systemPath = func() (string, error) { t.Fatal("system PATH read with restic_path set"); return "", nil }
	if err := runInstall(context.Background(), c.opts, c.deps); err != nil {
		t.Fatalf("with restic_path: %v", err)
	}
}

func TestUninstallRequiresElevation(t *testing.T) {
	c := newInstallCase(t)
	c.deps.isElevated = func() (bool, error) { return false, nil }
	if err := runUninstall(context.Background(), c.opts, c.deps); err == nil {
		t.Fatal("want error")
	}
}

func TestInstallEventSourceFailureIsWarning(t *testing.T) {
	c := newInstallCase(t)
	c.deps.registerSource = func() error { return errors.New("access denied") }
	err := runInstall(context.Background(), c.opts, c.deps)
	if exitCode(err) != exitWarnings || !strings.Contains(c.errOut.String(), "Event Log source") {
		t.Fatalf("err %v stderr %q", err, c.errOut)
	}
	if _, ok := c.tasks.created[windows.TaskName]; !ok {
		t.Fatal("task not installed")
	}
}
