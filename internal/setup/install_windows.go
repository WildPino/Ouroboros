package setup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"

	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/payload"
	platform "github.com/WildPino/ouroboros/internal/platform/windows"
)

// Paths below ProgramDir, slash-separated as in the payload manifest.
const (
	appPath    = "Ouroboros.exe"
	cliPath    = "bin/ouroboros.exe"
	resticPath = "restic/restic.exe"
)

// installedFiles lists every file setup may write below ProgramDir, so
// that uninstall removes them whichever version wrote them.
var installedFiles = []string{appPath, "LICENSE", cliPath, resticPath, "restic/LICENSE"}

// ErrNewerInstalled means the installed version is newer than the one
// running setup, which then changes nothing.
var ErrNewerInstalled = errors.New("a newer version of Ouroboros is installed")

// Installer installs, updates and uninstalls Ouroboros (DESIGN.md §12.4 and
// §12.8, question 4).
type Installer struct {
	Target
	System
	// Version is the version of the running Ouroboros.exe.
	Version string
	// Self is the running Ouroboros.exe, copied to ProgramDir.
	Self string
	// Payload holds the command line, restic and the licenses.
	Payload *payload.Payload
}

// InstallResult describes a completed installation.
type InstallResult struct {
	// Previous is the version that was installed before, if any.
	Previous string
	// RestartNeeded means a replaced file was in use and is deleted at the
	// next restart.
	RestartNeeded bool
	// TaskUpdated means the existing scheduled task now runs the installed
	// command line.
	TaskUpdated bool
	// Notes are things the user should know; none of them is an error.
	Notes []string
}

// InstalledVersion returns the version recorded in the "Installed apps"
// entry, or "" if Ouroboros is not installed.
func (in *Installer) InstalledVersion() (string, error) {
	k, err := registry.OpenKey(in.Registry, in.UninstallKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read the installed version: %w", err)
	}
	defer k.Close()
	v, _, err := k.GetStringValue("DisplayVersion")
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read the installed version: %w", err)
	}
	return v, nil
}

// source is one file to install.
type source struct {
	path   string // slash-separated, below ProgramDir
	dst    string
	size   int64
	sha256 string
	open   func() (io.ReadCloser, error)
}

func (in *Installer) sources() ([]source, error) {
	var out []source
	known := func(p string) bool {
		for _, k := range installedFiles {
			if strings.EqualFold(k, p) {
				return true
			}
		}
		return false
	}
	for _, f := range in.Payload.Files {
		if strings.EqualFold(f.Path, appPath) || !known(f.Path) {
			return nil, fmt.Errorf("the embedded file %s is not one setup installs", f.Path)
		}
		out = append(out, source{path: f.Path, dst: in.path(f.Path), size: f.Size, sha256: f.SHA256,
			open: func() (io.ReadCloser, error) { return in.Payload.Open(f) }})
	}
	if _, ok := in.Payload.Find(cliPath); !ok {
		return nil, fmt.Errorf("this build does not embed %s", cliPath)
	}

	// The installed Ouroboros.exe updating itself has nothing to copy.
	dst := in.path(appPath)
	if same, err := sameFile(in.Self, dst); err != nil {
		return nil, err
	} else if !same {
		sum, size, err := hashFile(in.Self)
		if err != nil {
			return nil, err
		}
		self := in.Self
		out = append([]source{{path: appPath, dst: dst, size: size, sha256: sum,
			open: func() (io.ReadCloser, error) { return os.Open(self) }}}, out...)
	}
	return out, nil
}

func sameFile(a, b string) (bool, error) {
	sa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	sb, err := os.Stat(b)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return os.SameFile(sa, sb), nil
}

func hashFile(path string) (sum string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("read %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// Install copies the files, then registers the shortcut, PATH entry and
// "Installed apps" entry, sets restic_path in an existing configuration and
// points an existing scheduled task at the installed command line. It
// refuses to replace a newer version and holds the run lock throughout,
// so no scheduled run starts while files change.
func (in *Installer) Install(ctx context.Context) (res InstallResult, err error) {
	prev, err := in.InstalledVersion()
	if err != nil {
		return res, err
	}
	res.Previous = prev
	if prev != "" {
		if c, ok := CompareVersions(in.Version, prev); ok && c < 0 {
			return res, fmt.Errorf("%w: version %s is installed and this is %s", ErrNewerInstalled, prev, in.Version)
		}
	}
	files, err := in.sources()
	if err != nil {
		return res, err
	}

	l, err := in.AcquireLock(ctx, in.LockName)
	if err != nil {
		return res, err
	}
	defer l.Release()

	if err := restrictDir(in.DataDir, in.DataDirSDDL); err != nil {
		return res, err
	}
	var total int64
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f.dst), 0o755); err != nil {
			return res, err
		}
		cleanLeftovers(filepath.Dir(f.dst), true)
		total += f.size
	}

	// Every new file is written and checked before any installed one is
	// replaced, so a damaged download changes nothing.
	staged := 0
	defer func() {
		if err != nil {
			for _, f := range files[:staged] {
				os.Remove(f.dst + ".new")
			}
		}
	}()
	for _, f := range files {
		if err := stage(f); err != nil {
			return res, err
		}
		staged++
	}
	for _, f := range files {
		pending, err := in.replace(f.dst)
		if err != nil {
			return res, err
		}
		res.RestartNeeded = res.RestartNeeded || pending
	}
	staged = 0

	app := in.path(appPath)
	if err := os.MkdirAll(in.StartMenuDir, 0o755); err != nil {
		return res, err
	}
	if err := createShortcut(in.shortcut(), app, in.ProgramDir, "Back up this computer with Ouroboros"); err != nil {
		return res, err
	}
	if changed, err := in.editPath(addPathEntry, true); err != nil {
		return res, err
	} else if changed {
		in.EnvironmentChanged()
	}
	if err := in.register(total); err != nil {
		return res, err
	}
	_, hasRestic := in.Payload.Find(resticPath)
	res.Notes = append(res.Notes, in.setResticPath(hasRestic)...)
	updated, notes, err := in.updateTask(ctx)
	res.TaskUpdated = updated
	res.Notes = append(res.Notes, notes...)
	return res, err
}

// stage writes f to NAME.new and checks what reached the disk.
func stage(f source) error {
	r, err := f.open()
	if err != nil {
		return err
	}
	defer r.Close()
	tmp := f.dst + ".new"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, r)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	sum, size, err := hashFile(tmp)
	if err != nil {
		return err
	}
	if size != f.size || sum != f.sha256 {
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", tmp, payload.ErrChecksum)
	}
	return nil
}

// replace puts dst.new in place of dst in the order that leaves either the
// old or the new file under dst at every step. A running executable cannot
// be deleted but can be renamed: the previous file then stays as NAME.old
// until the next restart. It reports whether that happened.
func (in *Installer) replace(dst string) (pending bool, err error) {
	old := ""
	if _, err := os.Lstat(dst); err == nil {
		old = dst + ".old"
		if err := os.Remove(old); err != nil && !os.IsNotExist(err) {
			old = fmt.Sprintf("%s.%08x.old", dst, rand.Uint32())
		}
		if err := os.Rename(dst, old); err != nil {
			return false, fmt.Errorf("move %s aside: %w", dst, err)
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.Rename(dst+".new", dst); err != nil {
		if old != "" {
			os.Rename(old, dst)
		}
		return false, fmt.Errorf("install %s: %w", dst, err)
	}
	if old == "" {
		return false, nil
	}
	if err := os.Remove(old); err == nil || os.IsNotExist(err) {
		return false, nil
	}
	// If even that fails, the next start of either program deletes it.
	return in.DeleteAtReboot(old) == nil, nil
}

// editPath applies edit to the Path value of EnvironmentKey with the bin
// folder. It keeps the value's type, which is normally REG_EXPAND_SZ.
func (in *Installer) editPath(edit func(list, dir string) (string, bool), create bool) (bool, error) {
	var k registry.Key
	var err error
	if create {
		k, _, err = registry.CreateKey(in.Registry, in.EnvironmentKey, registry.QUERY_VALUE|registry.SET_VALUE)
	} else {
		k, err = registry.OpenKey(in.Registry, in.EnvironmentKey, registry.QUERY_VALUE|registry.SET_VALUE)
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
	}
	if err != nil {
		return false, fmt.Errorf("open the PATH setting: %w", err)
	}
	defer k.Close()
	list, typ, err := k.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		list, typ = "", registry.EXPAND_SZ
	} else if err != nil {
		return false, fmt.Errorf("read PATH: %w", err)
	}
	list, changed := edit(list, in.binDir())
	if !changed {
		return false, nil
	}
	if typ == registry.EXPAND_SZ {
		err = k.SetExpandStringValue("Path", list)
	} else {
		err = k.SetStringValue("Path", list)
	}
	if err != nil {
		return false, fmt.Errorf("write PATH: %w", err)
	}
	return true, nil
}

// register writes the "Installed apps" entry.
func (in *Installer) register(size int64) error {
	k, _, err := registry.CreateKey(in.Registry, in.UninstallKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("register in Installed apps: %w", err)
	}
	defer k.Close()
	app := in.path(appPath)
	for _, v := range []struct{ name, value string }{
		{"DisplayName", "Ouroboros"},
		{"DisplayVersion", strings.TrimPrefix(in.Version, "v")},
		{"Publisher", "SirProjects"},
		{"InstallLocation", in.ProgramDir},
		{"DisplayIcon", app + ",0"},
		{"UninstallString", `"` + app + `" uninstall`},
		{"URLInfoAbout", "https://github.com/WildPino/ouroboros"},
	} {
		if err := k.SetStringValue(v.name, v.value); err != nil {
			return fmt.Errorf("register in Installed apps: %w", err)
		}
	}
	for _, v := range []struct {
		name  string
		value uint32
	}{
		{"NoModify", 1},
		{"NoRepair", 1},
		{"EstimatedSize", uint32((size + 1023) / 1024)},
	} {
		if err := k.SetDWordValue(v.name, v.value); err != nil {
			return fmt.Errorf("register in Installed apps: %w", err)
		}
	}
	return nil
}

// setResticPath points an existing configuration with an empty
// restic_path at the installed restic, so the scheduled task does not
// depend on PATH. A file written by hand is not rewritten, since saving
// would drop its comments; the user is told what to set instead.
func (in *Installer) setResticPath(hasRestic bool) []string {
	cfg := in.configPath()
	if _, err := os.Stat(cfg); err != nil || !hasRestic {
		return nil
	}
	c, err := config.Load(cfg)
	if err != nil {
		return []string{fmt.Sprintf("%s was not changed: %v", cfg, err)}
	}
	if c.Repository.ResticPath != "" {
		return nil
	}
	restic := in.path(resticPath)
	hand, err := config.HandEdited(cfg)
	if err != nil {
		return []string{fmt.Sprintf("%s was not changed: %v", cfg, err)}
	}
	if hand {
		return []string{fmt.Sprintf("%s was written by hand and is left as it is: "+
			"set restic_path = '%s' in it to use the installed restic", cfg, restic)}
	}
	err = config.Edit(cfg, func(c *config.Config) error {
		c.Repository.ResticPath = restic
		return nil
	})
	if err != nil {
		return []string{fmt.Sprintf("%s was not changed: %v", cfg, err)}
	}
	return nil
}

// updateTask reinstalls an existing scheduled task through the installed
// command line, so that it runs bin\ouroboros.exe; a task installed by
// hand from another folder would otherwise keep running the old copy. A
// paused task stays paused. Setup creates no task that does not exist.
func (in *Installer) updateTask(ctx context.Context) (updated bool, notes []string, err error) {
	st, err := in.TaskState(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("the files are installed, but the scheduled task could not be checked: %w", err)
	}
	if st == platform.TaskMissing {
		return false, nil, nil
	}
	cfg := in.configPath()
	if _, err := os.Stat(cfg); err != nil {
		// The task may use a configuration elsewhere, which only the user
		// knows; left as it is, it could run a file this update replaced.
		return false, nil, fmt.Errorf("the files are installed, but the scheduled task was not updated because there is no configuration at %s: "+
			"run \"ouroboros install --config PATH\" as Administrator with the configuration the task uses", cfg)
	}
	out, code, err := in.RunCLI(ctx, in.path(cliPath), "install", "--config", cfg, "--json")
	if err != nil {
		return false, nil, fmt.Errorf("the files are installed, but the scheduled task could not be updated: %w", err)
	}
	r := lastJSONLine(out)
	if (code != 0 && code != 3) || r.Type != "result" {
		msg := r.Error.Message
		if msg == "" {
			msg = fmt.Sprintf("ouroboros install exited with code %d", code)
		}
		return false, nil, fmt.Errorf("the files are installed, but the scheduled task could not be updated: %s", msg)
	}
	return true, r.Data.Warnings, nil
}

// jsonLine is the part of the command line's --json output that setup
// reads (docs/JSON.md).
type jsonLine struct {
	Type string `json:"type"`
	Data struct {
		Warnings []string `json:"warnings"`
	} `json:"data"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func lastJSONLine(out []byte) jsonLine {
	var last []byte
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		if line := bytes.TrimSpace(sc.Bytes()); len(line) > 0 {
			last = append(last[:0], line...)
		}
	}
	var l jsonLine
	json.Unmarshal(last, &l)
	return l
}
