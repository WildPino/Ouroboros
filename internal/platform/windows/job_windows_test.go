//go:build windows

package windows

import (
	"os/exec"
	"testing"
)

func TestAssignToKillOnExitJob(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "ping -n 30 127.0.0.1 >nul")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()
	if err := AssignToKillOnExitJob(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	// A second assignment reuses the same job.
	if err := AssignToKillOnExitJob(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
}
