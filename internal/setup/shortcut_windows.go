package setup

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Start menu shortcut is written through the shell's own ShellLink
// object, so that its format is whatever Windows expects. The COM calls are
// made by hand to avoid a dependency for two interfaces.

var procCoCreateInstance = windows.NewLazySystemDLL("ole32.dll").NewProc("CoCreateInstance")

var (
	clsidShellLink  = windows.GUID{Data1: 0x00021401, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIShellLinkW  = windows.GUID{Data1: 0x000214F9, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIPersistFile = windows.GUID{Data1: 0x0000010B, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
)

// Method indexes in the interfaces' virtual tables.
const (
	vtQueryInterface = 0
	vtRelease        = 2

	slGetPath             = 3
	slSetDescription      = 7
	slSetWorkingDirectory = 9
	slSetIconLocation     = 17
	slSetPath             = 20

	pfLoad = 5
	pfSave = 6
)

const (
	clsctxInprocServer = 1
	sFalse             = windows.Errno(1)
	rpcEChangedMode    = windows.Errno(0x80010106)
)

type comObject struct{ p unsafe.Pointer }

func (o comObject) call(method int, args ...uintptr) error {
	vtbl := *(**[32]uintptr)(o.p)
	hr, _, _ := syscall.SyscallN(vtbl[method], append([]uintptr{uintptr(o.p)}, args...)...)
	if int32(hr) < 0 {
		return windows.Errno(hr)
	}
	return nil
}

func (o comObject) release() { o.call(vtRelease) }

// withShellLink runs fn with a new ShellLink object and its IPersistFile
// interface, on a thread initialized for COM.
func withShellLink(fn func(link, file comObject) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	switch err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err {
	case nil, sFalse:
		defer windows.CoUninitialize()
	case rpcEChangedMode:
		// Already initialized differently on this thread; ShellLink works in
		// either apartment.
	default:
		return fmt.Errorf("initialize COM: %w", err)
	}

	var link unsafe.Pointer
	hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidShellLink)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIShellLinkW)), uintptr(unsafe.Pointer(&link)))
	if int32(hr) < 0 {
		return fmt.Errorf("create ShellLink: %w", windows.Errno(hr))
	}
	l := comObject{link}
	defer l.release()
	var file unsafe.Pointer
	if err := l.call(vtQueryInterface, uintptr(unsafe.Pointer(&iidIPersistFile)), uintptr(unsafe.Pointer(&file))); err != nil {
		return fmt.Errorf("query IPersistFile: %w", err)
	}
	f := comObject{file}
	defer f.release()
	return fn(l, f)
}

// createShortcut writes a shortcut at lnk that starts target in its own
// folder, with target's icon.
func createShortcut(lnk, target, workDir, description string) error {
	strs := map[string]*uint16{}
	for _, s := range []string{lnk, target, workDir, description} {
		p, err := windows.UTF16PtrFromString(s)
		if err != nil {
			return err
		}
		strs[s] = p
	}
	ptr := func(s string) uintptr { return uintptr(unsafe.Pointer(strs[s])) }
	err := withShellLink(func(link, file comObject) error {
		steps := []struct {
			what string
			err  error
		}{
			{"set path", link.call(slSetPath, ptr(target))},
			{"set working directory", link.call(slSetWorkingDirectory, ptr(workDir))},
			{"set description", link.call(slSetDescription, ptr(description))},
			{"set icon", link.call(slSetIconLocation, ptr(target), 0)},
			{"save", file.call(pfSave, ptr(lnk), 1)},
		}
		for _, s := range steps {
			if s.err != nil {
				return fmt.Errorf("shortcut %s: %s: %w", lnk, s.what, s.err)
			}
		}
		return nil
	})
	runtime.KeepAlive(strs)
	return err
}

// shortcutTarget returns the path that the shortcut at lnk starts.
func shortcutTarget(lnk string) (string, error) {
	name, err := windows.UTF16PtrFromString(lnk)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	const slgpRawPath = 4
	err = withShellLink(func(link, file comObject) error {
		if err := file.call(pfLoad, uintptr(unsafe.Pointer(name)), 0); err != nil {
			return fmt.Errorf("load shortcut %s: %w", lnk, err)
		}
		return link.call(slGetPath, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, slgpRawPath)
	})
	runtime.KeepAlive(name)
	if err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf), nil
}
