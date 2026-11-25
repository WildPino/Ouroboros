//go:build windows

package restic

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/WildPino/ouroboros/internal/config"
	platform "github.com/WildPino/ouroboros/internal/platform/windows"
)

// sysProcAttr always sets an explicit priority class: without one, a child
// of a below-normal process, such as the scheduled task, inherits that class.
func sysProcAttr(priority string) *syscall.SysProcAttr {
	class := uint32(windows.NORMAL_PRIORITY_CLASS)
	switch priority {
	case config.PriorityIdle:
		class = windows.IDLE_PRIORITY_CLASS
	case config.PriorityBelowNormal:
		class = windows.BELOW_NORMAL_PRIORITY_CLASS
	}
	return &syscall.SysProcAttr{CreationFlags: class}
}

func afterStart(p *os.Process) error { return platform.AssignToKillOnExitJob(p.Pid) }
