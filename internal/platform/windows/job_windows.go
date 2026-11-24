//go:build windows

package windows

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	killJobOnce sync.Once
	killJob     windows.Handle
	killJobErr  error
)

// AssignToKillOnExitJob places the process in a job object that Windows
// terminates when this process exits, so that restic never outlives
// Ouroboros. The job handle is intentionally never closed.
func AssignToKillOnExitJob(pid int) error {
	killJobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			killJobErr = fmt.Errorf("create job object: %w", err)
			return
		}
		var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			windows.CloseHandle(h)
			killJobErr = fmt.Errorf("configure job object: %w", err)
			return
		}
		killJob = h
	})
	if killJobErr != nil {
		return killJobErr
	}
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(p)
	if err := windows.AssignProcessToJobObject(killJob, p); err != nil {
		return fmt.Errorf("assign process %d to job object: %w", pid, err)
	}
	return nil
}
