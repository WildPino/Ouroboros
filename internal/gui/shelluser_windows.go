package gui

import (
	"errors"
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// DataPath returns the WebView2 user data folder of the window:
// Ouroboros\WebView2 in the local application data of the account that runs
// the desktop shell. WebView2 never runs elevated: for an elevated window
// the runtime starts the browser through Explorer, as that account, so the
// folder must be one that account can write, also when the window was
// elevated with the credentials of another account (DESIGN.md §12.6).
// Without a shell it falls back to the window's own account.
func DataPath() (string, error) {
	local, err := shellLocalAppData()
	if err != nil {
		local, err = windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
		if err != nil {
			return "", fmt.Errorf("locate the local application data: %w", err)
		}
	}
	return filepath.Join(local, "Ouroboros", "WebView2"), nil
}

// shellLocalAppData returns the local application data folder of the
// account that runs the desktop shell.
func shellLocalAppData() (string, error) {
	shell := windows.GetShellWindow()
	if shell == 0 {
		return "", errors.New("no desktop shell")
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(shell, &pid); err != nil {
		return "", err
	}
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(p)
	var token windows.Token
	if err := windows.OpenProcessToken(p, windows.TOKEN_QUERY|windows.TOKEN_IMPERSONATE|windows.TOKEN_DUPLICATE, &token); err != nil {
		return "", err
	}
	defer token.Close()
	return token.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
}
