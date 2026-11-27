package main

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"

	"github.com/WildPino/ouroboros/internal/gui"
	"github.com/WildPino/ouroboros/internal/setup"
)

// runWindow opens the main window, or brings an open one to the front.
func runWindow() int {
	tgt, err := setup.SystemTarget()
	if err != nil {
		message("Ouroboros cannot start: "+err.Error(), windows.MB_ICONERROR)
		return exitError
	}
	dataPath, err := gui.DataPath()
	if err != nil {
		message("Ouroboros cannot start: "+err.Error(), windows.MB_ICONERROR)
		return exitError
	}
	err = gui.Run(gui.Options{
		DataPath: dataPath,
		CLI:      gui.ExecRunner{Path: filepath.Join(tgt.ProgramDir, "bin", "ouroboros.exe")},
		Info:     func() (gui.AppInfo, error) { return appInfo(tgt) },
	})
	switch {
	case err == nil, errors.Is(err, gui.ErrAlreadyOpen):
		return exitOK
	case errors.Is(err, gui.ErrRuntimeMissing):
		gui.ShowRuntimeMissing(err)
		return exitError
	}
	message("The Ouroboros window could not open: "+err.Error(), windows.MB_ICONERROR)
	return exitError
}

// appInfo reports this program's version, the installed one, and whether
// the first setup is still to be done: no configuration in the data folder.
func appInfo(tgt setup.Target) (gui.AppInfo, error) {
	installed, err := (&setup.Installer{Target: tgt}).InstalledVersion()
	if err != nil {
		return gui.AppInfo{}, err
	}
	_, statErr := os.Stat(filepath.Join(tgt.DataDir, setup.ConfigName))
	return gui.AppInfo{
		Version:          version,
		InstalledVersion: installed,
		SetupNeeded:      errors.Is(statErr, os.ErrNotExist),
	}, nil
}
