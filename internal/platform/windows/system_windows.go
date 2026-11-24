//go:build windows

package windows

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DiskUsage returns the total size and the bytes available to the caller on
// the volume holding path, which may be a drive root, a folder on a mounted
// volume, or a path that does not exist yet.
func DiskUsage(path string) (total, free uint64, err error) {
	dir, err := NearestExisting(path)
	if err != nil {
		return 0, 0, err
	}
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, 0, err
	}
	var avail, size, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &size, &totalFree); err != nil {
		return 0, 0, fmt.Errorf("GetDiskFreeSpaceEx %s: %w", dir, err)
	}
	return size, avail, nil
}

// Directory access rights from winnt.h, not exported by x/sys/windows.
const (
	fileAddFile         = 0x0002
	fileAddSubdirectory = 0x0004
)

// CheckDirWritable verifies that files can be created in dir, or in its
// closest existing ancestor when dir does not exist yet. It opens the
// directory requesting add-file access instead of creating a probe file, so
// nothing is ever written inside an existing repository.
func CheckDirWritable(dir string) error {
	target, err := NearestExisting(dir)
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(p,
		fileAddFile|fileAddSubdirectory,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return fmt.Errorf("%s: %w", target, err)
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return fmt.Errorf("%s: %w", target, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return fmt.Errorf("%s is not a directory", target)
	}
	return nil
}

// IsElevated reports whether the process runs with Administrator rights.
func IsElevated() (bool, error) {
	return windows.GetCurrentProcessToken().IsElevated(), nil
}

// ErrServiceDisabled is returned by CheckService for a disabled service.
var ErrServiceDisabled = errors.New("service is disabled")

// CheckService returns nil if the named service is installed and not
// disabled. It needs only query rights, so it works without elevation.
func CheckService(name string) error {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return fmt.Errorf("open service manager: %w", err)
	}
	defer windows.CloseServiceHandle(scm)

	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	svc, err := windows.OpenService(scm, n, windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return fmt.Errorf("open service %s: %w", name, err)
	}
	defer windows.CloseServiceHandle(svc)

	var needed uint32
	err = windows.QueryServiceConfig(svc, nil, 0, &needed)
	if err != nil && !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
		return fmt.Errorf("query service %s: %w", name, err)
	}
	if needed == 0 {
		return fmt.Errorf("query service %s: empty configuration", name)
	}
	buf := make([]byte, needed)
	cfg := (*windows.QUERY_SERVICE_CONFIG)(unsafe.Pointer(&buf[0]))
	if err := windows.QueryServiceConfig(svc, cfg, needed, &needed); err != nil {
		return fmt.Errorf("query service %s: %w", name, err)
	}
	if cfg.StartType == windows.SERVICE_DISABLED {
		return fmt.Errorf("%s: %w", name, ErrServiceDisabled)
	}
	return nil
}

var procGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

// memoryStatusEx is MEMORYSTATUSEX.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

// FreeMemory returns the physical memory available to processes.
func FreeMemory() (uint64, error) {
	m := memoryStatusEx{}
	m.length = uint32(unsafe.Sizeof(m))
	if r, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); r == 0 {
		return 0, fmt.Errorf("GlobalMemoryStatusEx: %w", err)
	}
	return m.availPhys, nil
}
