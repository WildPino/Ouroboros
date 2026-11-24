//go:build !windows

package restic

import (
	"os"
	"syscall"
)

func sysProcAttr(string) *syscall.SysProcAttr { return nil }

func afterStart(*os.Process) error { return nil }
