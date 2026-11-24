//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/WildPino/ouroboros/internal/backend"
	"github.com/WildPino/ouroboros/internal/backend/restic"
	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/secret"
	"github.com/WildPino/ouroboros/internal/space"
)

// testStore returns a Store whose ACL also admits the current user, because
// tests run without elevation.
func testStore(t *testing.T) func(string) *secret.Store {
	t.Helper()
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sddl := secret.DefaultSDDL + "(A;;FA;;;" + u.User.Sid.String() + ")"
	return func(p string) *secret.Store { return secret.NewStoreWithSDDL(p, sddl) }
}

type fakeIniter struct {
	err    error
	called bool
}

func (f *fakeIniter) Init(context.Context) error { f.called = true; return f.err }

func passwords(pw ...string) func(string) ([]byte, error) {
	return func(string) ([]byte, error) {
		if len(pw) == 0 {
			return nil, errors.New("no more input")
		}
		p := pw[0]
		pw = pw[1:]
		return []byte(p), nil
	}
}

type initCase struct {
	dir     string
	cfgPath string
	repo    string
	opts    *options
	out     *bytes.Buffer
	errOut  *bytes.Buffer
	backend *fakeIniter
	deps    initDeps
}

func newInitCase(t *testing.T, extra string) *initCase {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	cfg := "[repository]\npath = '" + repo + "'\n[space]\nmin_free = \"1MB\"\nreserve_file_size = \"1MB\"\n" + extra +
		"[[job]]\nname = \"docs\"\npaths = ['" + filepath.Join(dir, "src") + "']\ninterval = \"6h\"\n"
	cfgPath := filepath.Join(dir, "ouroboros.toml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &initCase{dir: dir, cfgPath: cfgPath, repo: repo, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}, backend: &fakeIniter{}}
	c.opts = &options{configPath: cfgPath, stdout: c.out, stderr: c.errOut}
	c.deps = initDeps{
		isElevated:   func() (bool, error) { return true, nil },
		readPassword: passwords("correct horse", "correct horse"),
		newStore:     testStore(t),
		newBackend:   func(*config.Config, string) (repoIniter, error) { return c.backend, nil },
		disk:         fakeDisk{total: 100 << 30, free: 50 << 30},
	}
	return c
}

func (c *initCase) secretExists(t *testing.T) bool {
	t.Helper()
	_, err := os.Stat(secretPath(c.cfgPath))
	return err == nil
}

func TestInitSuccessWithFakes(t *testing.T) {
	c := newInitCase(t, "")
	if err := runInit(context.Background(), c.opts, c.deps); err != nil {
		t.Fatalf("init: %v\n%s", err, c.errOut)
	}
	if !c.backend.called || !c.secretExists(t) {
		t.Fatal("repository or secret not created")
	}
	pw, err := c.deps.newStore(secretPath(c.cfgPath)).Load()
	if err != nil || string(pw) != "correct horse" {
		t.Fatalf("stored password %q, %v", pw, err)
	}
	fi, err := os.Stat(space.BallastPath(c.repo))
	if err != nil || fi.Size() != 1<<20 {
		t.Fatalf("ballast: %v", err)
	}
	if !strings.Contains(c.out.String(), "write down the repository password") {
		t.Fatalf("missing password reminder:\n%s", c.out)
	}
}

func TestInitRefusals(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, c *initCase)
		want   string
	}{
		{"not elevated", func(t *testing.T, c *initCase) {
			c.deps.isElevated = func() (bool, error) { return false, nil }
		}, "Administrator"},
		{"repository not empty", func(t *testing.T, c *initCase) {
			writeTestFile(t, filepath.Join(c.repo, "config"))
		}, "not empty"},
		{"secret already stored", func(t *testing.T, c *initCase) {
			if err := c.deps.newStore(secretPath(c.cfgPath)).Save([]byte("old password")); err != nil {
				t.Fatal(err)
			}
		}, "already stored"},
		{"passwords differ", func(t *testing.T, c *initCase) {
			c.deps.readPassword = passwords("correct horse", "correct house")
		}, "do not match"},
		{"password too short", func(t *testing.T, c *initCase) {
			c.deps.readPassword = passwords("short", "short")
		}, "at least"},
		{"password with surrounding spaces", func(t *testing.T, c *initCase) {
			c.deps.readPassword = passwords(" correct horse", " correct horse")
		}, "spaces"},
		{"no console", func(t *testing.T, c *initCase) {
			c.deps.readPassword = passwords()
		}, "no more input"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newInitCase(t, "")
			tt.mutate(t, c)
			preexisting := c.secretExists(t)
			err := runInit(context.Background(), c.opts, c.deps)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			if c.backend.called {
				t.Fatal("repository was created")
			}
			if !preexisting && c.secretExists(t) {
				t.Fatal("password was stored")
			}
		})
	}
}

func TestInitRemovesSecretWhenRepositoryFails(t *testing.T) {
	c := newInitCase(t, "")
	c.backend.err = errors.New("disk on fire")
	err := runInit(context.Background(), c.opts, c.deps)
	if err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("err = %v", err)
	}
	if c.secretExists(t) {
		t.Fatal("secret left behind after a failed init")
	}
	if _, err := os.Stat(space.BallastPath(c.repo)); err == nil {
		t.Fatal("ballast created after a failed init")
	}
}

func TestInitBallast(t *testing.T) {
	t.Run("not enough space", func(t *testing.T) {
		c := newInitCase(t, "")
		c.deps.disk = fakeDisk{total: 100 << 30, free: 3 << 19} // 1.5 MB: ballast fits, min_free does not
		err := runInit(context.Background(), c.opts, c.deps)
		if codeOf(err) != exitWarnings || !strings.Contains(c.errOut.String(), "ballast not created") {
			t.Fatalf("err = %v, stderr %q", err, c.errOut)
		}
		if !c.backend.called || !c.secretExists(t) {
			t.Fatal("repository must still be created")
		}
	})
	t.Run("disabled", func(t *testing.T) {
		c := newInitCase(t, "")
		os.WriteFile(c.cfgPath, bytes.Replace(mustRead(t, c.cfgPath), []byte(`reserve_file_size = "1MB"`), []byte(`reserve_file_size = 0`), 1), 0o600)
		if err := runInit(context.Background(), c.opts, c.deps); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(space.BallastPath(c.repo)); err == nil {
			t.Fatal("ballast created although disabled")
		}
	})
}

// TestInitEndToEnd creates a real repository. restic gets the password by
// running this test binary as "ouroboros internal password", which reads
// the DPAPI-protected file written by init.
func TestInitEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	resticExe, err := exec.LookPath("restic")
	if err != nil {
		t.Skip("restic not installed")
	}
	t.Setenv(mainHelperEnv, "1")
	c := newInitCase(t, "")
	cache := filepath.Join(c.dir, "cache")
	newBackend := func(cfg *config.Config, cfgPath string) (*restic.Restic, error) {
		self, err := os.Executable()
		if err != nil {
			return nil, err
		}
		return restic.New(restic.Options{
			Executable:      resticExe,
			Repository:      cfg.Repository.Path,
			PasswordCommand: restic.PasswordCommand(self, "--config", cfgPath),
			Hostname:        "testhost",
			CacheDir:        cache,
		})
	}
	c.deps.newBackend = func(cfg *config.Config, p string) (repoIniter, error) { return newBackend(cfg, p) }
	c.deps.disk = platformDisk{}

	if err := runInit(context.Background(), c.opts, c.deps); err != nil {
		t.Fatalf("init: %v\nstdout:\n%s\nstderr:\n%s", err, c.out, c.errOut)
	}

	// The hidden command prints exactly the stored password.
	code, out, errOut := runCLI("internal", "password", "--config", c.cfgPath)
	if code != exitOK || out != "correct horse" {
		t.Fatalf("internal password: code %d out %q err %q", code, out, errOut)
	}

	// The repository opens with that password.
	cfg, err := config.Load(c.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	b, err := newBackend(cfg, c.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if snaps, err := b.Snapshots(context.Background(), backend.SnapshotFilter{}); err != nil || len(snaps) != 0 {
		t.Fatalf("snapshots: %v, %v", snaps, err)
	}
}

func TestInternalPasswordWithoutSecret(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "ouroboros.toml")
	code, out, errOut := runCLI("internal", "password", "--config", cfgPath)
	if code == exitOK || out != "" || !strings.Contains(errOut, "not stored") {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
}

func TestAskNewPasswordClearsOnMismatch(t *testing.T) {
	first := []byte("correct horse")
	read := func(prompt string) ([]byte, error) {
		if strings.HasPrefix(prompt, "Repository") {
			return first, nil
		}
		return []byte("other value"), nil
	}
	if _, err := askNewPassword(read); err == nil {
		t.Fatal("want error")
	}
	if !bytes.Equal(first, make([]byte, len(first))) {
		t.Fatal("rejected password was not cleared")
	}
}

func writeTestFile(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
