package gui

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// sysProcAttr runs the command line without a console window.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

// detachedProcAttr also puts the command in its own process group, so it
// is not stopped with the window.
func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP}
}
