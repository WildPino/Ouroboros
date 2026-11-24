//go:build windows

package restic

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/WildPino/ouroboros/internal/config"
	platform "github.com/WildPino/ouroboros/internal/platform/windows"
)

func sysProcAttr(priority string) *syscall.SysProcAttr {
	var class uint32
	switch priority {
	case config.PriorityIdle:
		class = windows.IDLE_PRIORITY_CLASS
	case config.PriorityBelowNormal:
		class = windows.BELOW_NORMAL_PRIORITY_CLASS
	}
	return &syscall.SysProcAttr{CreationFlags: class}
}

func afterStart(p *os.Process) error { return platform.AssignToKillOnExitJob(p.Pid) }
