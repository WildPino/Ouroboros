package main

import (
	"bytes"
	"os"

	"golang.org/x/sys/windows"

	"github.com/WildPino/ouroboros/internal/payload"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == embeddedFlag {
		os.Exit(embeddedVersions())
	}
	message("Ouroboros "+version+"\n\nThe window is not part of this build yet. "+
		"Use the ouroboros command line.", windows.MB_ICONINFORMATION)
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
		return 1
	}
	return 0
}

func message(text string, icon uint32) {
	t, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	c, _ := windows.UTF16PtrFromString("Ouroboros")
	windows.MessageBox(0, t, c, windows.MB_OK|icon)
}
