package gui

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestDataPath runs as the same account as the desktop shell, so the shell's
// folder and the test's own must be the same.
func TestDataPath(t *testing.T) {
	if windows.GetShellWindow() == 0 {
		t.Skip("no desktop shell")
	}
	if windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("the shell may belong to another account than an elevated test")
	}
	shell, err := shellLocalAppData()
	if err != nil {
		t.Fatal(err)
	}
	own, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(shell, own) {
		t.Errorf("shell folder %q, own %q", shell, own)
	}
	got, err := DataPath()
	if err != nil || !strings.EqualFold(got, filepath.Join(own, "Ouroboros", "WebView2")) {
		t.Errorf("DataPath = %q, %v", got, err)
	}
}
