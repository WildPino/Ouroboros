package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/WildPino/ouroboros/internal/lock"
	"github.com/WildPino/ouroboros/internal/payload"
	platform "github.com/WildPino/ouroboros/internal/platform/windows"
	"github.com/WildPino/ouroboros/internal/setup"
)

// Until the window exists, "Ouroboros.exe install" and "Ouroboros.exe
// uninstall" ask and report through native message boxes.

var errCancelled = errors.New("cancelled")

func newInstaller(self string, withPayload bool) (*setup.Installer, error) {
	elevated, err := platform.IsElevated()
	if err != nil {
		return nil, fmt.Errorf("check privileges: %w", err)
	}
	if !elevated {
		return nil, errors.New("it must be run as Administrator")
	}
	tgt, err := setup.SystemTarget()
	if err != nil {
		return nil, err
	}
	var p *payload.Payload
	if withPayload {
		if p, err = payload.Embedded(); err != nil {
			return nil, err
		}
	}
	sys := setup.LiveSystem(nil)
	sys.AcquireLock = acquireAsking
	return &setup.Installer{Target: tgt, System: sys, Version: version, Self: self, Payload: p}, nil
}

// acquireAsking takes the run lock, asking the user to retry while a
// backup holds it.
func acquireAsking(ctx context.Context, name string) (*lock.Lock, error) {
	for {
		l, err := lock.Acquire(name)
		if !errors.Is(err, lock.ErrLocked) {
			return l, err
		}
		if message("A backup is running. Ouroboros can change its files only after it has finished.\n\n"+
			"Choose Retry when it is done, or Cancel to stop.", windows.MB_RETRYCANCEL|windows.MB_ICONWARNING) != idRetry {
			return nil, errCancelled
		}
	}
}

func withNotes(text string, notes []string) string {
	if len(notes) == 0 {
		return text
	}
	return text + "\n\nPlease note:\n- " + strings.Join(notes, "\n- ")
}

func runInstall(self string, selfErr error) int {
	in, err := newInstaller(self, true)
	if err == nil {
		err = selfErr
	}
	if err != nil {
		message("Ouroboros cannot be installed: "+err.Error(), windows.MB_ICONERROR)
		return exitError
	}
	prev, err := in.InstalledVersion()
	if err != nil {
		message("Ouroboros cannot be installed: "+err.Error(), windows.MB_ICONERROR)
		return exitError
	}
	q := fmt.Sprintf("Install Ouroboros %s for all users of this computer?\n\nProgram: %s\nConfiguration and logs: %s",
		version, in.ProgramDir, in.DataDir)
	if prev != "" {
		q = fmt.Sprintf("Update Ouroboros from %s to %s?\n\nThe configuration and the backups are kept.", prev, version)
	}
	if message(q, windows.MB_OKCANCEL|windows.MB_ICONQUESTION) != idOK {
		return exitCancelled
	}

	res, err := in.Install(context.Background())
	if errors.Is(err, errCancelled) {
		return exitCancelled
	}
	if err != nil {
		message(withNotes("Ouroboros could not be installed: "+err.Error(), res.Notes), windows.MB_ICONERROR)
		return exitError
	}
	text := fmt.Sprintf("Ouroboros %s is installed in %s.\n\nThe command line is \"ouroboros\" in a new terminal.",
		version, in.ProgramDir)
	if res.TaskUpdated {
		text += "\nThe scheduled task runs the installed command line."
	}
	if res.RestartNeeded {
		text += "\nSome replaced files are deleted when the computer restarts."
	}
	message(withNotes(text, res.Notes), windows.MB_ICONINFORMATION)
	return exitOK
}

func runUninstall(self string, selfErr error) int {
	in, err := newInstaller(self, false)
	if err == nil {
		err = selfErr
	}
	if err != nil {
		message("Ouroboros cannot be removed: "+err.Error(), windows.MB_ICONERROR)
		return exitError
	}
	if message("Remove Ouroboros from this computer?\n\nAutomatic backups stop. The repository and its backups are kept.",
		windows.MB_YESNO|windows.MB_ICONQUESTION|windows.MB_DEFBUTTON2) != idYes {
		return exitCancelled
	}
	var removeData bool
	switch message(fmt.Sprintf("Also remove the configuration, the stored repository password, the history and the logs in %s?\n\n"+
		"Keep them to reinstall later with the same settings. Without them, the backups can be restored "+
		"only with the password you wrote down.", in.DataDir),
		windows.MB_YESNOCANCEL|windows.MB_ICONQUESTION|windows.MB_DEFBUTTON2) {
	case idYes:
		removeData = true
	case idNo:
	default:
		return exitCancelled
	}

	res, err := in.Uninstall(context.Background(), removeData)
	if errors.Is(err, errCancelled) {
		return exitCancelled
	}
	if err != nil {
		message(withNotes("Ouroboros could not be removed: "+err.Error(), res.Notes), windows.MB_ICONERROR)
		return exitError
	}
	text := "Ouroboros has been removed. The repository and its backups are kept."
	if res.RestartNeeded {
		text += "\n\nSome files, including this program, are deleted when the computer restarts."
	}
	message(withNotes(text, res.Notes), windows.MB_ICONINFORMATION)
	return exitOK
}
