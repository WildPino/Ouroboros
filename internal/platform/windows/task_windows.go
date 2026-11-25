//go:build windows

package windows

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// schtasks is called by full path so that PATH cannot substitute it.
func schtasks() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "schtasks.exe")
}

func runSchtasks(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(ctx, schtasks(), args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

// CreateTask registers (or replaces) a scheduled task from its XML
// definition. It requires elevation.
func CreateTask(ctx context.Context, name string, def []byte) error {
	f, err := os.CreateTemp("", "ouroboros-task-*.xml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(def); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return runSchtasks(ctx, "/Create", "/TN", name, "/XML", f.Name(), "/F")
}

// DeleteTask removes a scheduled task.
func DeleteTask(ctx context.Context, name string) error {
	return runSchtasks(ctx, "/Delete", "/TN", name, "/F")
}

// QueryTask reports whether a scheduled task is registered and enabled.
func QueryTask(ctx context.Context, name string) (TaskState, error) {
	out, err := exec.CommandContext(ctx, schtasks(), "/Query", "/TN", name, "/XML").Output()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return taskStateFromXML(out)
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return TaskMissing, nil
	default:
		return TaskMissing, fmt.Errorf("schtasks /Query: %w", err)
	}
}

// SetTaskEnabled enables or disables a registered task. A disabled task
// does not start, but an instance already running goes on.
func SetTaskEnabled(ctx context.Context, name string, enabled bool) error {
	flag := "/DISABLE"
	if enabled {
		flag = "/ENABLE"
	}
	return runSchtasks(ctx, "/Change", "/TN", name, flag)
}

// Accounts that may write to the programs a SYSTEM task runs.
var trustedSIDs = map[string]bool{
	"S-1-5-18":     true, // LocalSystem
	"S-1-5-32-544": true, // Administrators
	"S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464": true, // TrustedInstaller
	"S-1-3-0": true, // CREATOR OWNER: a placeholder, resolved to the real owner
}

// Rights that let an account replace a file, add files to a directory or
// change who may do so.
const writeRights = 0x2 | 0x4 | // FILE_WRITE_DATA / FILE_ADD_FILE, FILE_APPEND_DATA / FILE_ADD_SUBDIRECTORY
	windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_WRITE | windows.GENERIC_ALL

// UntrustedWriters lists the accounts other than SYSTEM, Administrators and
// TrustedInstaller that can modify path. Anything such an account writes
// there would later run as SYSTEM. An untrusted owner is listed as
// "owner <name>", since an owner can always change the permissions.
func UntrustedWriters(path string) ([]string, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, fmt.Errorf("read permissions of %s: %w", path, err)
	}
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if owner, _, err := sd.Owner(); err == nil && owner != nil && !trustedSIDs[owner.String()] {
		add("owner " + accountName(owner))
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return nil, fmt.Errorf("read permissions of %s: %w", path, err)
	}
	if dacl == nil {
		add("Everyone (no access control list)")
		return out, nil
	}
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return nil, fmt.Errorf("read permissions of %s: %w", path, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
			ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 ||
			ace.Mask&writeRights == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !trustedSIDs[sid.String()] {
			add(accountName(sid))
		}
	}
	return out, nil
}

func accountName(sid *windows.SID) string {
	account, domain, _, err := sid.LookupAccount("")
	switch {
	case err != nil:
		return sid.String()
	case domain == "":
		return account
	default:
		return domain + `\` + account
	}
}
