//go:build !windows

package gui

import "syscall"

func sysProcAttr() *syscall.SysProcAttr { return nil }

func detachedProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
