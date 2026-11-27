package gui

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ole32            = windows.NewLazySystemDLL("ole32.dll")
	coCreateInstance = ole32.NewProc("CoCreateInstance")
	shell32          = windows.NewLazySystemDLL("shell32.dll")
	shCreateItem     = shell32.NewProc("SHCreateItemFromParsingName")

	clsidFileOpenDialog = windows.GUID{Data1: 0xDC1C5A9C, Data2: 0xE88A, Data3: 0x4DDE, Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidFileOpenDialog   = windows.GUID{Data1: 0xD57C7288, Data2: 0xD4AD, Data3: 0x4768, Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
	iidShellItem        = windows.GUID{Data1: 0x43826D1E, Data2: 0xE718, Data3: 0x42EE, Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
)

// comObject is a COM interface pointer whose methods are called by their
// index in the vtable.
type comObject struct{ vtbl *[32]uintptr }

func (o *comObject) call(method int, a ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(o.vtbl[method], append([]uintptr{uintptr(unsafe.Pointer(o))}, a...)...)
	return r
}

func (o *comObject) release() { o.call(2) }

// Indexes in the IFileOpenDialog and IShellItem vtables.
const (
	dialogShow          = 3
	dialogSetFolder     = 12
	dialogSetOptions    = 9
	dialogGetOptions    = 10
	dialogGetResult     = 20
	itemGetDisplayName  = 5
	fosPickFolders      = 0x20
	fosForceFileSystem  = 0x40
	sigdnFileSysPath    = 0x80058000
	hresultCancelled    = 0x800704C7
	clsctxInprocServer  = 1
	errFolderPickerText = "the folder picker could not be shown"
)

// pickFolder shows the Windows folder picker owned by hwnd, starting in
// start when it is set, and returns the chosen folder or "" when cancelled.
// It must run on the window's thread.
func pickFolder(hwnd uintptr, start string) (string, error) {
	var d *comObject
	r, _, _ := coCreateInstance.Call(uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidFileOpenDialog)), uintptr(unsafe.Pointer(&d)))
	if int32(r) < 0 || d == nil {
		return "", errors.New(errFolderPickerText)
	}
	defer d.release()
	var opts uint32
	d.call(dialogGetOptions, uintptr(unsafe.Pointer(&opts)))
	d.call(dialogSetOptions, uintptr(opts|fosPickFolders|fosForceFileSystem))
	if start != "" {
		if p, err := windows.UTF16PtrFromString(start); err == nil {
			var item *comObject
			hr, _, _ := shCreateItem.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&item)))
			if int32(hr) >= 0 && item != nil {
				d.call(dialogSetFolder, uintptr(unsafe.Pointer(item)))
				item.release()
			}
		}
	}
	if hr := d.call(dialogShow, hwnd); int32(hr) < 0 {
		if uint32(hr) == hresultCancelled {
			return "", nil
		}
		return "", errors.New(errFolderPickerText)
	}
	var item *comObject
	if hr := d.call(dialogGetResult, uintptr(unsafe.Pointer(&item))); int32(hr) < 0 || item == nil {
		return "", errors.New(errFolderPickerText)
	}
	defer item.release()
	var p *uint16
	if hr := item.call(itemGetDisplayName, sigdnFileSysPath, uintptr(unsafe.Pointer(&p))); int32(hr) < 0 || p == nil {
		return "", errors.New("the chosen folder is not on a drive")
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(p))
	return windows.UTF16PtrToString(p), nil
}

// Confirm asks a yes or no question in a native dialog owned by the
// window, if one is open, and reports whether the answer was yes.
func Confirm(text string) bool {
	t, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return false
	}
	c, _ := windows.UTF16PtrFromString("Ouroboros")
	ret, _ := windows.MessageBox(windows.HWND(FindWindow()), t, c,
		windows.MB_YESNO|windows.MB_ICONWARNING|windows.MB_DEFBUTTON2|windows.MB_SETFOREGROUND)
	return ret == 6 // IDYES
}

// runtimePage is Microsoft's download page for the WebView2 Runtime.
const runtimePage = "https://developer.microsoft.com/microsoft-edge/webview2/"

// ShowRuntimeMissing tells the user that the WebView2 Runtime is needed and
// offers to open its download page. The page opens through Explorer, so the
// browser does not run with the administrator rights of this program.
func ShowRuntimeMissing(detail error) {
	text := "Ouroboros needs the Microsoft Edge WebView2 Runtime, which is part of Windows 11 " +
		"and of most Windows 10 installations, but is missing or too old on this computer.\n\n" +
		"Backups and the ouroboros command line keep working.\n\n"
	if detail != nil {
		text += "Details: " + detail.Error() + "\n\n"
	}
	text += "Open Microsoft's download page now?"
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString("Ouroboros")
	ret, _ := windows.MessageBox(0, t, c, windows.MB_YESNO|windows.MB_ICONWARNING|windows.MB_SETFOREGROUND)
	if ret != 6 {
		return
	}
	openInExplorer(runtimePage)
}

// openInExplorer hands url to the running Explorer, which opens it with
// the rights of the signed-in user.
func openInExplorer(url string) {
	dir, err := windows.GetWindowsDirectory()
	if err != nil {
		return
	}
	var si windows.StartupInfo
	var pi windows.ProcessInformation
	si.Cb = uint32(unsafe.Sizeof(si))
	app, _ := windows.UTF16PtrFromString(dir + `\explorer.exe`)
	cmdLine, _ := windows.UTF16PtrFromString(`explorer.exe "` + url + `"`)
	if err := windows.CreateProcess(app, cmdLine, nil, nil, false, 0, nil, nil, &si, &pi); err == nil {
		_ = windows.CloseHandle(pi.Thread)
		_ = windows.CloseHandle(pi.Process)
	}
}
