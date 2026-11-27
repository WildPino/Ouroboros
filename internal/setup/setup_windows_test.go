package setup

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/lock"
	"github.com/WildPino/ouroboros/internal/payload"
	platform "github.com/WildPino/ouroboros/internal/platform/windows"
)

// sleepEnv makes a copy of the test binary wait until it is killed, so
// that tests can replace or delete an executable that is running.
const sleepEnv = "OUROBOROS_SETUP_TEST_SLEEP"

func TestMain(m *testing.M) {
	if os.Getenv(sleepEnv) == "1" {
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeSystem records what setup did to the rest of the machine.
type fakeSystem struct {
	task       platform.TaskState
	deleteErr  error
	deleted    int
	cliCalls   [][]string
	cliOut     string
	cliCode    int
	atReboot   []string
	envChanges int
}

func (f *fakeSystem) system(lockName string) System {
	return System{
		AcquireLock: func(ctx context.Context, name string) (*lock.Lock, error) {
			if name != lockName {
				return nil, fmt.Errorf("lock %q, want %q", name, lockName)
			}
			return WaitLock(ctx, name, nil)
		},
		RunCLI: func(ctx context.Context, exe string, args ...string) ([]byte, int, error) {
			f.cliCalls = append(f.cliCalls, append([]string{exe}, args...))
			return []byte(f.cliOut), f.cliCode, nil
		},
		TaskState: func(ctx context.Context) (platform.TaskState, error) { return f.task, nil },
		DeleteTask: func(ctx context.Context) error {
			if f.deleteErr != nil {
				return f.deleteErr
			}
			f.deleted++
			f.task = platform.TaskMissing
			return nil
		},
		UnregisterSource: func() error { return nil },
		DeleteAtReboot: func(path string) error {
			f.atReboot = append(f.atReboot, path)
			return nil
		},
		EnvironmentChanged: func() { f.envChanges++ },
	}
}

type env struct {
	t     *testing.T
	root  string
	sys   *fakeSystem
	tgt   Target
	self  string
	files map[string]string
}

// newEnv prepares temporary folders, a registry key below HKCU that is
// removed at the end, and a Downloads folder with an Ouroboros.exe.
func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf(`Software\OuroborosSetupTest-%d-%d`, os.Getpid(), rand.Uint32())
	t.Cleanup(func() { deleteKeyTree(t, registry.CURRENT_USER, key) })
	e := &env{
		t:    t,
		root: root,
		sys:  &fakeSystem{task: platform.TaskMissing},
		tgt: Target{
			ProgramDir:     filepath.Join(root, "Program Files", "Ouroboros"),
			DataDir:        filepath.Join(root, "ProgramData", "Ouroboros"),
			StartMenuDir:   filepath.Join(root, "Start Menu", "Programs"),
			Registry:       registry.CURRENT_USER,
			UninstallKey:   key + `\Uninstall\Ouroboros`,
			EnvironmentKey: key + `\Environment`,
			// The current user too, so that the test can clean up, and
			// as owner, which needs no administrator rights.
			DataDirSDDL: testDataDirSDDL(u.User.Sid),
			LockName:    fmt.Sprintf(`Local\OuroborosSetupTest-%d-%d`, os.Getpid(), rand.Uint32()),
		},
		self: filepath.Join(root, "Downloads", "Ouroboros.exe"),
	}
	e.setFiles("v1")
	k, _, err := registry.CreateKey(registry.CURRENT_USER, e.tgt.EnvironmentKey, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	if err := k.SetExpandStringValue("Path", `%SystemRoot%\system32;C:\Tools`); err != nil {
		t.Fatal(err)
	}
	return e
}

// setFiles sets the content of every file to install, marked with tag.
func (e *env) setFiles(tag string) {
	e.files = map[string]string{
		appPath:          "window " + tag,
		"LICENSE":        "MIT " + tag,
		cliPath:          "command line " + tag,
		resticPath:       "restic " + tag,
		"restic/LICENSE": "BSD " + tag,
	}
	if err := os.MkdirAll(filepath.Dir(e.self), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(e.self, []byte(e.files[appPath]), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) payload(corrupt string) *payload.Payload {
	e.t.Helper()
	fsys := fstest.MapFS{}
	var entries []payload.File
	for _, p := range []string{cliPath, "LICENSE", resticPath, "restic/LICENSE"} {
		data := []byte(e.files[p])
		var b bytes.Buffer
		w := gzip.NewWriter(&b)
		w.Write(data)
		w.Close()
		fsys[p+".gz"] = &fstest.MapFile{Data: b.Bytes()}
		s := sha256.Sum256(data)
		if p == corrupt {
			s = sha256.Sum256([]byte("something else"))
		}
		entries = append(entries, payload.File{Path: p, Size: int64(len(data)), SHA256: hex.EncodeToString(s[:])})
	}
	m, err := json.Marshal(map[string]any{"files": entries})
	if err != nil {
		e.t.Fatal(err)
	}
	fsys[payload.ManifestName] = &fstest.MapFile{Data: m}
	p, err := payload.Load(fsys)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *env) installer(version string) *Installer {
	return &Installer{
		Target:  e.tgt,
		System:  e.sys.system(e.tgt.LockName),
		Version: version,
		Self:    e.self,
		Payload: e.payload(""),
	}
}

func (e *env) install(version string) InstallResult {
	e.t.Helper()
	res, err := e.installer(version).Install(context.Background())
	if err != nil {
		e.t.Fatalf("Install %s: %v", version, err)
	}
	return res
}

// checkFiles fails unless every installed file has the content of tag and
// no leftovers remain.
func (e *env) checkFiles() {
	e.t.Helper()
	for p, want := range e.files {
		got, err := os.ReadFile(e.tgt.path(p))
		if err != nil || string(got) != want {
			e.t.Errorf("%s = %q, %v; want %q", p, got, err, want)
		}
	}
	for _, d := range []string{e.tgt.ProgramDir, e.tgt.binDir(), filepath.Join(e.tgt.ProgramDir, "restic")} {
		entries, err := os.ReadDir(d)
		if err != nil {
			e.t.Fatal(err)
		}
		for _, en := range entries {
			if strings.HasSuffix(en.Name(), ".old") || strings.HasSuffix(en.Name(), ".new") {
				e.t.Errorf("leftover %s in %s", en.Name(), d)
			}
		}
	}
}

func (e *env) path() string {
	e.t.Helper()
	k, err := registry.OpenKey(registry.CURRENT_USER, e.tgt.EnvironmentKey, registry.QUERY_VALUE)
	if err != nil {
		e.t.Fatal(err)
	}
	defer k.Close()
	v, typ, err := k.GetStringValue("Path")
	if err != nil {
		e.t.Fatal(err)
	}
	if typ != registry.EXPAND_SZ {
		e.t.Errorf("Path has type %d, want REG_EXPAND_SZ", typ)
	}
	return v
}

func (e *env) writeConfig(edit func(*config.Config)) string {
	e.t.Helper()
	c := config.Default()
	c.Repository.Path = filepath.Join(e.root, "repo")
	c.Jobs = []config.Job{{Name: "docs", Paths: []string{filepath.Join(e.root, "docs")}, Interval: config.Duration(6 * time.Hour)}}
	if edit != nil {
		edit(&c)
	}
	if err := os.MkdirAll(e.tgt.DataDir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	p := e.tgt.configPath()
	if err := config.Save(p, &c); err != nil {
		e.t.Fatal(err)
	}
	return p
}

// runCopy starts a copy of the test binary at path that runs until the
// test ends.
func runCopy(t *testing.T, path string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path)
	cmd.Env = append(os.Environ(), sleepEnv+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Registered after the folders, so it runs before t.TempDir removes them.
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
}

func deleteKeyTree(t *testing.T, root registry.Key, path string) {
	k, err := registry.OpenKey(root, path, registry.ENUMERATE_SUB_KEYS)
	if errors.Is(err, registry.ErrNotExist) {
		return
	}
	if err != nil {
		t.Errorf("open %s: %v", path, err)
		return
	}
	subs, err := k.ReadSubKeyNames(-1)
	k.Close()
	if err != nil {
		t.Errorf("list %s: %v", path, err)
	}
	for _, s := range subs {
		deleteKeyTree(t, root, path+`\`+s)
	}
	if err := registry.DeleteKey(root, path); err != nil {
		t.Errorf("delete %s: %v", path, err)
	}
}

func TestInstallFresh(t *testing.T) {
	e := newEnv(t)
	res := e.install("v0.2.0")
	if res.Previous != "" || res.RestartNeeded || res.TaskUpdated || len(res.Notes) != 0 {
		t.Errorf("result = %+v", res)
	}
	e.checkFiles()

	got, err := shortcutTarget(e.tgt.shortcut())
	if err != nil || !strings.EqualFold(got, e.tgt.path(appPath)) {
		t.Errorf("shortcut target = %q, %v; want %q", got, err, e.tgt.path(appPath))
	}
	if want := `%SystemRoot%\system32;C:\Tools;` + e.tgt.binDir(); e.path() != want {
		t.Errorf("Path = %q, want %q", e.path(), want)
	}
	if e.sys.envChanges != 1 {
		t.Errorf("environment change broadcasts = %d, want 1", e.sys.envChanges)
	}
	if v, err := e.installer("v0.2.0").InstalledVersion(); err != nil || v != "0.2.0" {
		t.Errorf("InstalledVersion = %q, %v", v, err)
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, e.tgt.UninstallKey, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	if v, _, _ := k.GetStringValue("UninstallString"); v != `"`+e.tgt.path(appPath)+`" uninstall` {
		t.Errorf("UninstallString = %q", v)
	}
	if v, _, _ := k.GetIntegerValue("EstimatedSize"); v != 1 {
		t.Errorf("EstimatedSize = %d KB", v)
	}
	if fi, err := os.Stat(e.tgt.DataDir); err != nil || !fi.IsDir() {
		t.Errorf("data folder: %v", err)
	}
	if len(e.sys.cliCalls) != 0 {
		t.Errorf("command line run without a task: %v", e.sys.cliCalls)
	}
}

func TestInstallUpdate(t *testing.T) {
	e := newEnv(t)
	e.install("v0.2.0")
	e.setFiles("v2")
	res := e.install("v0.2.1")
	if res.Previous != "0.2.0" || res.RestartNeeded {
		t.Errorf("result = %+v", res)
	}
	e.checkFiles()
	if strings.Count(strings.ToLower(e.path()), strings.ToLower(e.tgt.binDir())) != 1 {
		t.Errorf("Path = %q", e.path())
	}
	if e.sys.envChanges != 1 {
		t.Errorf("environment change broadcasts = %d, want 1", e.sys.envChanges)
	}
	// The same version again repairs the installation.
	if err := os.Remove(e.tgt.path(resticPath)); err != nil {
		t.Fatal(err)
	}
	e.install("v0.2.1")
	e.checkFiles()
}

func TestInstallRefusesOlder(t *testing.T) {
	e := newEnv(t)
	e.install("v0.3.0")
	e.setFiles("v2")
	_, err := e.installer("v0.2.0").Install(context.Background())
	if !errors.Is(err, ErrNewerInstalled) {
		t.Fatalf("Install = %v, want ErrNewerInstalled", err)
	}
	e.setFiles("v1")
	e.checkFiles()
	// A development build has no order and may replace anything.
	e.install("dev")
}

func TestInstallBadPayloadChangesNothing(t *testing.T) {
	e := newEnv(t)
	e.install("v0.2.0")
	e.setFiles("v2")
	in := e.installer("v0.2.1")
	in.Payload = e.payload(resticPath)
	if _, err := in.Install(context.Background()); !errors.Is(err, payload.ErrChecksum) {
		t.Fatalf("Install = %v, want ErrChecksum", err)
	}
	e.setFiles("v1")
	e.checkFiles()
	if v, _ := in.InstalledVersion(); v != "0.2.0" {
		t.Errorf("installed version = %q", v)
	}
}

func TestInstallReplacesRunningFile(t *testing.T) {
	e := newEnv(t)
	runCopy(t, e.tgt.path(cliPath))
	res := e.install("v0.2.0")
	if !res.RestartNeeded {
		t.Error("RestartNeeded = false with the command line running")
	}
	old := e.tgt.path(cliPath) + ".old"
	if !slices.Equal(e.sys.atReboot, []string{old}) {
		t.Errorf("marked for deletion at restart: %v, want %v", e.sys.atReboot, []string{old})
	}
	if got, _ := os.ReadFile(e.tgt.path(cliPath)); string(got) != e.files[cliPath] {
		t.Errorf("command line not replaced: %.40q", got)
	}

	// A second update while the first old copy still runs.
	e.setFiles("v2")
	e.sys.atReboot = nil
	res = e.install("v0.2.1")
	if res.RestartNeeded || len(e.sys.atReboot) != 0 {
		t.Errorf("result = %+v, at reboot %v", res, e.sys.atReboot)
	}
	if got, _ := os.ReadFile(e.tgt.path(cliPath)); string(got) != e.files[cliPath] {
		t.Errorf("command line not replaced: %.40q", got)
	}
}

func TestInstallOverOldCommandLineFolder(t *testing.T) {
	e := newEnv(t)
	// Version 0.1.0 was installed by hand as ouroboros.exe in the same
	// folder, which is the same file name as Ouroboros.exe.
	legacy := filepath.Join(e.tgt.ProgramDir, "ouroboros.exe")
	if err := os.MkdirAll(e.tgt.ProgramDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("command line 0.1.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := e.writeConfig(nil)
	e.sys.task = platform.TaskEnabled
	e.sys.cliOut = `{"type":"result","command":"install","data":{"task":"\\Ouroboros\\Tick","paused":false,"warnings":[]}}` + "\n"
	res := e.install("v0.2.0")
	e.checkFiles()
	entries, err := os.ReadDir(e.tgt.ProgramDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, en := range entries {
		names = append(names, en.Name())
	}
	if !slices.Equal(names, []string{"LICENSE", "Ouroboros.exe", "bin", "restic"}) {
		t.Errorf("program folder = %v", names)
	}
	want := []string{e.tgt.path(cliPath), "install", "--config", cfg, "--json"}
	if !res.TaskUpdated || len(e.sys.cliCalls) != 1 || !slices.Equal(e.sys.cliCalls[0], want) {
		t.Errorf("TaskUpdated = %v, calls %v; want %v", res.TaskUpdated, e.sys.cliCalls, want)
	}
}

func TestInstallTaskUpdate(t *testing.T) {
	e := newEnv(t)
	e.writeConfig(nil)
	e.sys.task = platform.TaskDisabled
	e.sys.cliOut = `{"type":"result","command":"install","data":{"warnings":["folder can be modified"]}}`
	e.sys.cliCode = 3
	res := e.install("v0.2.0")
	if !res.TaskUpdated || !slices.Contains(res.Notes, "folder can be modified") {
		t.Errorf("result = %+v", res)
	}

	e.sys.cliOut = `{"type":"error","command":"install","error":{"code":"not_elevated","message":"install must be run as Administrator"}}`
	e.sys.cliCode = 1
	res, err := e.installer("v0.2.0").Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "install must be run as Administrator") || res.TaskUpdated {
		t.Errorf("Install = %+v, %v", res, err)
	}
	e.checkFiles()

	// No configuration where setup expects it: the task may use another
	// one, so the user must reinstall it.
	if err := os.Remove(e.tgt.configPath()); err != nil {
		t.Fatal(err)
	}
	e.sys.cliCalls = nil
	res, err = e.installer("v0.2.0").Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ouroboros install --config PATH") || res.TaskUpdated || len(e.sys.cliCalls) != 0 {
		t.Errorf("Install = %+v, %v; calls %v", res, err, e.sys.cliCalls)
	}
	e.checkFiles()
}

func TestInstallSetsResticPath(t *testing.T) {
	e := newEnv(t)
	cfg := e.writeConfig(nil)
	res := e.install("v0.2.0")
	c, err := config.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.Repository.ResticPath != e.tgt.path(resticPath) || len(res.Notes) != 0 {
		t.Errorf("restic_path = %q, notes %v", c.Repository.ResticPath, res.Notes)
	}

	// A path already set is the user's choice.
	e.writeConfig(func(c *config.Config) { c.Repository.ResticPath = `D:\Tools\restic.exe` })
	e.install("v0.2.0")
	if c, _ := config.Load(cfg); c.Repository.ResticPath != `D:\Tools\restic.exe` {
		t.Errorf("restic_path = %q", c.Repository.ResticPath)
	}

	// A file written by hand keeps its comments; the user is told instead.
	e.writeConfig(nil)
	data, _ := os.ReadFile(cfg)
	if err := os.WriteFile(cfg, append(data, "# mine\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	res = e.install("v0.2.0")
	if after, _ := os.ReadFile(cfg); !bytes.HasSuffix(after, []byte("# mine\n")) {
		t.Error("hand-written configuration was rewritten")
	}
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "restic_path = '"+e.tgt.path(resticPath)+"'") {
		t.Errorf("notes = %v", res.Notes)
	}
}

func TestInstallWaitsForRun(t *testing.T) {
	e := newEnv(t)
	l, err := lock.Acquire(e.tgt.LockName)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if _, err := e.installer("v0.2.0").Install(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Install while locked = %v", err)
	}
	if _, err := os.Stat(e.tgt.ProgramDir); !os.IsNotExist(err) {
		t.Errorf("program folder created while locked: %v", err)
	}
	l.Release()
	e.install("v0.2.0")
}

func TestWaitLock(t *testing.T) {
	name := fmt.Sprintf(`Local\OuroborosSetupTest-%d-wait`, os.Getpid())
	held, err := lock.Acquire(name)
	if err != nil {
		t.Fatal(err)
	}
	waits := 0
	go func() {
		time.Sleep(1500 * time.Millisecond)
		held.Release()
	}()
	l, err := WaitLock(context.Background(), name, func() { waits++ })
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
	if waits != 1 {
		t.Errorf("waiting called %d times, want 1", waits)
	}
}

func TestUninstall(t *testing.T) {
	e := newEnv(t)
	e.writeConfig(nil)
	e.install("v0.2.0")
	e.sys.task = platform.TaskEnabled
	res, err := e.installer("v0.2.0").Uninstall(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.RestartNeeded || res.DataRemoved || len(res.Notes) != 0 || e.sys.deleted != 1 {
		t.Errorf("result = %+v, task deletions %d", res, e.sys.deleted)
	}
	for _, p := range []string{e.tgt.ProgramDir, e.tgt.shortcut()} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists: %v", p, err)
		}
	}
	if v, err := e.installer("v0.2.0").InstalledVersion(); err != nil || v != "" {
		t.Errorf("InstalledVersion = %q, %v", v, err)
	}
	if want := `%SystemRoot%\system32;C:\Tools`; e.path() != want {
		t.Errorf("Path = %q, want %q", e.path(), want)
	}
	if _, err := os.Stat(e.tgt.configPath()); err != nil {
		t.Errorf("configuration removed without asking: %v", err)
	}

	// Again, with the data, and nothing left to uninstall.
	res, err = e.installer("v0.2.0").Uninstall(context.Background(), true)
	if err != nil || !res.DataRemoved || len(res.Notes) != 0 {
		t.Fatalf("Uninstall = %+v, %v", res, err)
	}
	if _, err := os.Stat(e.tgt.DataDir); !os.IsNotExist(err) {
		t.Errorf("data folder still exists: %v", err)
	}
}

func TestUninstallKeepsRepositoryInDataDir(t *testing.T) {
	e := newEnv(t)
	e.writeConfig(func(c *config.Config) { c.Repository.Path = filepath.Join(e.tgt.DataDir, "repo") })
	e.install("v0.2.0")
	res, err := e.installer("v0.2.0").Uninstall(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if res.DataRemoved || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "the repository is inside it") {
		t.Errorf("result = %+v", res)
	}
	if _, err := os.Stat(e.tgt.configPath()); err != nil {
		t.Errorf("configuration removed: %v", err)
	}
}

func TestUninstallRunningWindow(t *testing.T) {
	e := newEnv(t)
	e.install("v0.2.0")
	app := e.tgt.path(appPath)
	if err := os.Remove(app); err != nil {
		t.Fatal(err)
	}
	runCopy(t, app)
	in := e.installer("v0.2.0")
	in.Self = app
	res, err := in.Uninstall(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{app, e.tgt.ProgramDir}
	if !res.RestartNeeded || !slices.Equal(e.sys.atReboot, want) || len(res.Notes) != 0 {
		t.Errorf("result = %+v, at reboot %v; want %v", res, e.sys.atReboot, want)
	}
	if _, err := os.Stat(e.tgt.binDir()); !os.IsNotExist(err) {
		t.Errorf("bin folder still exists: %v", err)
	}
}

func TestUninstallStopsWhenTaskStays(t *testing.T) {
	e := newEnv(t)
	e.install("v0.2.0")
	e.sys.task = platform.TaskEnabled
	e.sys.deleteErr = errors.New("access denied")
	if _, err := e.installer("v0.2.0").Uninstall(context.Background(), true); err == nil {
		t.Fatal("Uninstall succeeded with the task still installed")
	}
	e.checkFiles()
	if v, _ := e.installer("v0.2.0").InstalledVersion(); v != "0.2.0" {
		t.Errorf("installed version = %q", v)
	}
}

func TestShortcut(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "Program Files", "Ouroboros.exe")
	lnk := filepath.Join(dir, "Ouroboros.lnk")
	if err := createShortcut(lnk, target, filepath.Dir(target), "test"); err != nil {
		t.Fatal(err)
	}
	got, err := shortcutTarget(lnk)
	if err != nil || got != target {
		t.Fatalf("target = %q, %v; want %q", got, err, target)
	}
	if _, err := shortcutTarget(filepath.Join(dir, "missing.lnk")); err == nil {
		t.Error("reading a missing shortcut succeeded")
	}
}

func TestCleanOldOnlyInInstalledFolders(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "ouroboros.exe.old")
	if err := os.WriteFile(old, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	CleanOld(filepath.Join(dir, "ouroboros.exe"))
	if _, err := os.Stat(old); err != nil {
		t.Errorf("CleanOld touched a folder that is not the installation: %v", err)
	}
}

// testDataDirSDDL is DataDirSDDL with the current user as owner and with
// access, which a test without administrator rights can set.
func testDataDirSDDL(user *windows.SID) string {
	sddl := strings.Replace(DataDirSDDL, "O:BA", "O:"+user.String(), 1)
	return sddl + "(A;OICI;FA;;;" + user.String() + ")"
}

func TestDataDirOwner(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(e.tgt.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	e.install("v0.2.0")
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := ownedBy(e.tgt.DataDir, u.User.Sid); err != nil || !ok {
		t.Errorf("owner of the data folder: %v, %v", ok, err)
	}
	if ok, err := ownedBy(e.tgt.DataDir, mustSID(t, windows.WinLocalSystemSid)); err != nil || ok {
		t.Errorf("owned by SYSTEM: %v, %v", ok, err)
	}
}

func TestPrepareWebViewDir(t *testing.T) {
	e := newEnv(t)
	dir, err := PrepareWebViewDir(e.tgt)
	if err != nil || dir != filepath.Join(e.tgt.DataDir, "WebView2") {
		t.Fatalf("PrepareWebViewDir = %q, %v", dir, err)
	}
	if _, err := os.Stat(e.tgt.DataDir); err != nil {
		t.Fatalf("data folder not created: %v", err)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := PrepareWebViewDir(e.tgt); err != nil || got != dir {
		t.Errorf("own folder: %q, %v", got, err)
	}

	// A folder owned by anyone else is refused. Without administrator
	// rights a test cannot give a folder away, so the expected owner
	// changes instead.
	err = checkOwner(dir, mustSID(t, windows.WinLocalSystemSid))
	if err == nil || !strings.Contains(err.Error(), "belongs to another account") {
		t.Errorf("folder of another owner: %v", err)
	}
	if err := checkOwner(filepath.Join(e.tgt.DataDir, "missing"), mustSID(t, windows.WinLocalSystemSid)); err != nil {
		t.Errorf("missing folder: %v", err)
	}
}

func mustSID(t *testing.T, kind windows.WELL_KNOWN_SID_TYPE) *windows.SID {
	t.Helper()
	sid, err := windows.CreateWellKnownSid(kind)
	if err != nil {
		t.Fatal(err)
	}
	return sid
}
