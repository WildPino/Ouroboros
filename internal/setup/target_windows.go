package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/WildPino/ouroboros/internal/lock"
	"github.com/WildPino/ouroboros/internal/logging"
	platform "github.com/WildPino/ouroboros/internal/platform/windows"
)

// Target says where Ouroboros is installed and registered. Tests point it
// at temporary folders and a registry key of their own.
type Target struct {
	// ProgramDir holds Ouroboros.exe, bin\ and restic\ (DESIGN.md §12.4).
	ProgramDir string
	// DataDir holds the configuration, the password, the state and the logs.
	DataDir string
	// StartMenuDir is the folder of the Start menu shortcut.
	StartMenuDir string
	// Registry is the root of UninstallKey and EnvironmentKey.
	Registry registry.Key
	// UninstallKey is the "Installed apps" entry.
	UninstallKey string
	// EnvironmentKey holds the Path value that gets bin\.
	EnvironmentKey string
	// DataDirSDDL is the security descriptor applied to DataDir.
	DataDirSDDL string
	// LockName is the run lock taken while files change.
	LockName string
}

// DataDirSDDL restricts the data folder and everything inherited from it
// to SYSTEM and Administrators (DESIGN.md §6).
const DataDirSDDL = "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

// ConfigName is the configuration file in DataDir, as the command line
// expects it.
const ConfigName = "ouroboros.toml"

// SystemTarget is the real installation for all users of this machine.
func SystemTarget() (Target, error) {
	pf, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return Target{}, fmt.Errorf("locate Program Files: %w", err)
	}
	pd, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return Target{}, fmt.Errorf("locate ProgramData: %w", err)
	}
	sm, err := windows.KnownFolderPath(windows.FOLDERID_CommonPrograms, 0)
	if err != nil {
		return Target{}, fmt.Errorf("locate the Start menu: %w", err)
	}
	return Target{
		ProgramDir:     filepath.Join(pf, "Ouroboros"),
		DataDir:        filepath.Join(pd, "Ouroboros"),
		StartMenuDir:   sm,
		Registry:       registry.LOCAL_MACHINE,
		UninstallKey:   `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\Ouroboros`,
		EnvironmentKey: `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`,
		DataDirSDDL:    DataDirSDDL,
		LockName:       lock.Name,
	}, nil
}

func (t Target) path(slashPath string) string {
	return filepath.Join(t.ProgramDir, filepath.FromSlash(slashPath))
}

func (t Target) shortcut() string { return filepath.Join(t.StartMenuDir, "Ouroboros.lnk") }
func (t Target) binDir() string   { return filepath.Join(t.ProgramDir, "bin") }
func (t Target) configPath() string {
	return filepath.Join(t.DataDir, ConfigName)
}

// System holds the effects on the rest of the machine, replaced in tests.
type System struct {
	// AcquireLock takes the run lock, waiting while a run holds it.
	AcquireLock func(ctx context.Context, name string) (*lock.Lock, error)
	// RunCLI runs the installed command line and returns its standard
	// output and exit code.
	RunCLI           func(ctx context.Context, exe string, args ...string) (stdout []byte, code int, err error)
	TaskState        func(ctx context.Context) (platform.TaskState, error)
	DeleteTask       func(ctx context.Context) error
	UnregisterSource func() error
	// DeleteAtReboot marks a file or an empty folder for deletion at the
	// next restart.
	DeleteAtReboot func(path string) error
	// EnvironmentChanged tells running programs that PATH changed.
	EnvironmentChanged func()
}

// LiveSystem acts on the real machine. While a run holds the lock,
// AcquireLock calls waiting once and retries every second.
func LiveSystem(waiting func()) System {
	return System{
		AcquireLock: func(ctx context.Context, name string) (*lock.Lock, error) {
			return WaitLock(ctx, name, waiting)
		},
		RunCLI: runCLI,
		TaskState: func(ctx context.Context) (platform.TaskState, error) {
			return platform.QueryTask(ctx, platform.TaskName)
		},
		DeleteTask: func(ctx context.Context) error {
			return platform.DeleteTask(ctx, platform.TaskName)
		},
		UnregisterSource:   logging.UnregisterSource,
		DeleteAtReboot:     deleteAtReboot,
		EnvironmentChanged: environmentChanged,
	}
}

// WaitLock takes the named run lock, waiting until the run that holds it
// ends or ctx is done. waiting, if not nil, is called once before the
// first wait.
func WaitLock(ctx context.Context, name string, waiting func()) (*lock.Lock, error) {
	notified := false
	for {
		l, err := lock.Acquire(name)
		if !errors.Is(err, lock.ErrLocked) {
			return l, err
		}
		if !notified && waiting != nil {
			waiting()
			notified = true
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func runCLI(ctx context.Context, exe string, args ...string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out, exit.ExitCode(), nil
	}
	if err != nil {
		return out, -1, err
	}
	return out, 0, nil
}

func deleteAtReboot(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(p, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT); err != nil {
		return fmt.Errorf("mark %s for deletion at restart: %w", path, err)
	}
	return nil
}

var procSendMessageTimeoutW = windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")

// environmentChanged broadcasts WM_SETTINGCHANGE for "Environment", so that
// Explorer gives the new PATH to the terminals it opens from now on.
func environmentChanged() {
	const (
		hwndBroadcast    = 0xffff
		wmSettingChange  = 0x001A
		smtoAbortIfHung  = 0x0002
		timeoutMillisecs = 5000
	)
	env, _ := windows.UTF16PtrFromString("Environment")
	var result uintptr
	procSendMessageTimeoutW.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)),
		smtoAbortIfHung, timeoutMillisecs, uintptr(unsafe.Pointer(&result)))
}

// restrictDir creates dir if needed and gives it the protected security
// descriptor sddl, which its contents inherit.
func restrictDir(dir, sddl string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("security descriptor: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("security descriptor: %w", err)
	}
	err = windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	if err != nil {
		return fmt.Errorf("restrict %s: %w", dir, err)
	}
	return nil
}

// CleanOld deletes the NAME.old files that an update leaves when it
// replaces a running program, if exe runs from the installed program or
// bin folder; elsewhere a file of the same name is not ours. Files still in
// use stay until a later start or the restart that deletes them. It skips
// NAME.new, which a running setup may be writing.
func CleanOld(exe string) {
	t, err := SystemTarget()
	if err != nil {
		return
	}
	dir := filepath.Dir(exe)
	if samePathEntry(dir, t.ProgramDir) || samePathEntry(dir, t.binDir()) {
		cleanLeftovers(dir, false)
	}
}
