package main

import (
	"bytes"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"

	"github.com/WildPino/ouroboros/internal/payload"
	"github.com/WildPino/ouroboros/internal/setup"
)

// Exit codes. Arguments this build does not know end it at once and
// without a dialog: a scheduled task left over from a manual installation
// in the same folder may start Ouroboros.exe with "run".
const (
	exitOK        = 0
	exitError     = 1
	exitUsage     = 2
	exitCancelled = 3
)

func main() {
	self, err := os.Executable()
	if err == nil {
		if resolved, rerr := filepath.EvalSymlinks(self); rerr == nil {
			self = resolved
		}
		setup.CleanOld(self)
	}
	switch {
	case len(os.Args) == 1:
		message("Ouroboros "+version+"\n\nThe window is not part of this build yet. "+
			"Install with \"Ouroboros.exe install\" and use the ouroboros command line.", windows.MB_ICONINFORMATION)
		os.Exit(exitOK)
	case len(os.Args) == 2 && os.Args[1] == embeddedFlag:
		os.Exit(embeddedVersions())
	case len(os.Args) == 2 && os.Args[1] == "install":
		os.Exit(runInstall(self, err))
	case len(os.Args) == 2 && os.Args[1] == "uninstall":
		os.Exit(runUninstall(self, err))
	}
	os.Exit(exitUsage)
}

// embeddedVersions prints the embedded files to standard output. A
// windowed program has no console: the output reaches the caller only when
// redirected, as with "Ouroboros.exe --embedded-versions | more", so it is
// shown in a message box otherwise.
func embeddedVersions() int {
	p, err := payload.Embedded()
	var b bytes.Buffer
	ok := writeEmbedded(&b, p, err)
	if _, err := os.Stdout.Write(b.Bytes()); err != nil {
		icon := uint32(windows.MB_ICONINFORMATION)
		if !ok {
			icon = windows.MB_ICONERROR
		}
		message(b.String(), icon)
	}
	if !ok {
		return exitError
	}
	return exitOK
}

// Values returned by MessageBox.
const (
	idOK     = 1
	idCancel = 2
	idRetry  = 4
	idYes    = 6
	idNo     = 7
)

// message shows a native message box and returns the button chosen.
func message(text string, flags uint32) int {
	t, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return idCancel
	}
	c, _ := windows.UTF16PtrFromString("Ouroboros")
	ret, _ := windows.MessageBox(0, t, c, flags|windows.MB_SETFOREGROUND)
	return int(ret)
}
