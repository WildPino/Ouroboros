//go:build windows

package edge

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Additions for a host that loads only its own content: handlers that
// cancel navigations, popups and downloads, settings that turn off the
// browser features a local page does not need, and calls that check the
// HRESULT instead of the thread's last error.

var (
	iidNavigationStartingHandler = NewGUID("{9ADBE429-F36D-432B-9DDC-F8881FBD76E3}")
	iidNewWindowRequestedHandler = NewGUID("{D4C185FE-C81C-4989-97AF-2D3FA7AB5651}")
	iidDownloadStartingHandler   = NewGUID("{EFEDC989-C396-41CA-83F7-07F845A55724}")
	iidICoreWebView2_4           = NewGUID("{20D02D59-6DF2-42DC-BD06-F98A694B1302}")
)

// hresult turns a failed HRESULT into an error.
func hresult(call string, hr uintptr) error {
	if int32(hr) < 0 {
		return fmt.Errorf("%s failed with %08x", call, uint32(hr))
	}
	return nil
}

// takeString returns a string allocated by WebView2 and frees it.
func takeString(p *uint16) string {
	if p == nil {
		return ""
	}
	s := windows.UTF16PtrToString(p)
	windows.CoTaskMemFree(unsafe.Pointer(p))
	return s
}

// RequestURI returns the URI of a web resource request.
func RequestURI(req *ICoreWebView2WebResourceRequest) (string, error) {
	var p *uint16
	hr, _, _ := req.vtbl.GetUri.Call(uintptr(unsafe.Pointer(req)), uintptr(unsafe.Pointer(&p)))
	if err := hresult("get_Uri", hr); err != nil {
		return "", err
	}
	return takeString(p), nil
}

// RequestMethod returns the HTTP method of a web resource request.
func RequestMethod(req *ICoreWebView2WebResourceRequest) (string, error) {
	var p *uint16
	hr, _, _ := req.vtbl.GetMethod.Call(uintptr(unsafe.Pointer(req)), uintptr(unsafe.Pointer(&p)))
	if err := hresult("get_Method", hr); err != nil {
		return "", err
	}
	return takeString(p), nil
}

// releaseStream releases an IStream from SHCreateMemStream.
func releaseStream(stream uintptr) {
	// stream is a COM object allocated by the system, outside the Go heap.
	obj := *(*unsafe.Pointer)(unsafe.Pointer(&stream))
	vtbl := *(**[3]uintptr)(obj)
	_, _, _ = syscall.SyscallN(vtbl[2], stream)
}

// messageSource returns the URI of the document that posted a web message.
func messageSource(args *iCoreWebView2WebMessageReceivedEventArgs) string {
	var p *uint16
	hr, _, _ := args.vtbl.GetSource.Call(uintptr(unsafe.Pointer(args)), uintptr(unsafe.Pointer(&p)))
	if int32(hr) < 0 {
		return ""
	}
	return takeString(p)
}

// Navigation starting, for the top-level document and for frames.

type navigationStartingArgsVtbl struct {
	_IUnknownVtbl
	GetUri             ComProc
	GetIsUserInitiated ComProc
	GetIsRedirected    ComProc
	GetRequestHeaders  ComProc
	GetCancel          ComProc
	PutCancel          ComProc
	GetNavigationId    ComProc
}

type navigationStartingArgs struct{ vtbl *navigationStartingArgsVtbl }

type handlerVtbl struct {
	_IUnknownVtbl
	Invoke ComProc
}

type navigationStartingHandler struct {
	vtbl *handlerVtbl
	e    *Chromium
}

var navigationStartingFn = handlerVtbl{
	_IUnknownVtbl{
		NewComProc(func(this *navigationStartingHandler, refiid *GUID, object *unsafe.Pointer) uintptr {
			return queryHandler(unsafe.Pointer(this), iidNavigationStartingHandler, refiid, object)
		}),
		NewComProc(func(this *navigationStartingHandler) uintptr { return 1 }),
		NewComProc(func(this *navigationStartingHandler) uintptr { return 1 }),
	},
	NewComProc(func(this *navigationStartingHandler, _ *ICoreWebView2, args *navigationStartingArgs) uintptr {
		var p *uint16
		hr, _, _ := args.vtbl.GetUri.Call(uintptr(unsafe.Pointer(args)), uintptr(unsafe.Pointer(&p)))
		uri := ""
		if int32(hr) >= 0 {
			uri = takeString(p)
		}
		e := this.e
		if int32(hr) < 0 || e.NavigationAllowed == nil || !e.NavigationAllowed(uri) {
			_, _, _ = args.vtbl.PutCancel.Call(uintptr(unsafe.Pointer(args)), 1)
			e.refused("navigation", uri)
		}
		return 0
	}),
}

// New windows: window.open, target="_blank" and the like.

type newWindowRequestedArgsVtbl struct {
	_IUnknownVtbl
	GetUri       ComProc
	PutNewWindow ComProc
	GetNewWindow ComProc
	PutHandled   ComProc
}

type newWindowRequestedArgs struct{ vtbl *newWindowRequestedArgsVtbl }

type newWindowRequestedHandler struct {
	vtbl *handlerVtbl
	e    *Chromium
}

// Every popup is marked handled without a window being created, so no
// second browser, which no filter would cover, ever opens.
var newWindowRequestedFn = handlerVtbl{
	_IUnknownVtbl{
		NewComProc(func(this *newWindowRequestedHandler, refiid *GUID, object *unsafe.Pointer) uintptr {
			return queryHandler(unsafe.Pointer(this), iidNewWindowRequestedHandler, refiid, object)
		}),
		NewComProc(func(this *newWindowRequestedHandler) uintptr { return 1 }),
		NewComProc(func(this *newWindowRequestedHandler) uintptr { return 1 }),
	},
	NewComProc(func(this *newWindowRequestedHandler, _ *ICoreWebView2, args *newWindowRequestedArgs) uintptr {
		var p *uint16
		uri := ""
		if hr, _, _ := args.vtbl.GetUri.Call(uintptr(unsafe.Pointer(args)), uintptr(unsafe.Pointer(&p))); int32(hr) >= 0 {
			uri = takeString(p)
		}
		_, _, _ = args.vtbl.PutHandled.Call(uintptr(unsafe.Pointer(args)), 1)
		this.e.refused("new window", uri)
		return 0
	}),
}

// Downloads.

type iCoreWebView2_4Vtbl struct {
	iCoreWebView2_3Vtbl
	AddFrameCreated        ComProc
	RemoveFrameCreated     ComProc
	AddDownloadStarting    ComProc
	RemoveDownloadStarting ComProc
}

type iCoreWebView2_4 struct{ vtbl *iCoreWebView2_4Vtbl }

type downloadStartingArgsVtbl struct {
	_IUnknownVtbl
	GetDownloadOperation ComProc
	GetCancel            ComProc
	PutCancel            ComProc
	GetResultFilePath    ComProc
	PutResultFilePath    ComProc
	GetHandled           ComProc
	PutHandled           ComProc
}

type downloadStartingArgs struct{ vtbl *downloadStartingArgsVtbl }

type downloadStartingHandler struct {
	vtbl *handlerVtbl
	e    *Chromium
}

var downloadStartingFn = handlerVtbl{
	_IUnknownVtbl{
		NewComProc(func(this *downloadStartingHandler, refiid *GUID, object *unsafe.Pointer) uintptr {
			return queryHandler(unsafe.Pointer(this), iidDownloadStartingHandler, refiid, object)
		}),
		NewComProc(func(this *downloadStartingHandler) uintptr { return 1 }),
		NewComProc(func(this *downloadStartingHandler) uintptr { return 1 }),
	},
	NewComProc(func(this *downloadStartingHandler, _ *ICoreWebView2, args *downloadStartingArgs) uintptr {
		_, _, _ = args.vtbl.PutCancel.Call(uintptr(unsafe.Pointer(args)), 1)
		_, _, _ = args.vtbl.PutHandled.Call(uintptr(unsafe.Pointer(args)), 1)
		this.e.refused("download", "")
		return 0
	}),
}

func (e *Chromium) addDownloadStarting() error {
	var w4 *iCoreWebView2_4
	hr, _, _ := e.webview.vtbl.QueryInterface.Call(
		uintptr(unsafe.Pointer(e.webview)),
		uintptr(unsafe.Pointer(iidICoreWebView2_4)),
		uintptr(unsafe.Pointer(&w4)),
	)
	if err := hresult("QueryInterface(ICoreWebView2_4)", hr); err != nil {
		return err
	}
	defer w4.vtbl.Release.Call(uintptr(unsafe.Pointer(w4)))
	var token _EventRegistrationToken
	hr, _, _ = w4.vtbl.AddDownloadStarting.Call(
		uintptr(unsafe.Pointer(w4)),
		uintptr(unsafe.Pointer(e.downloadStarting)),
		uintptr(unsafe.Pointer(&token)),
	)
	return hresult("add_DownloadStarting", hr)
}

// hardenSettings turns off what a page made only of local files does not
// need: developer tools, the context menu, the status bar, host objects,
// the browser's keyboard shortcuts (print, find, save, reload), password
// saving and form autofill, and swipe navigation. Script dialogs and web
// messages stay on.
func (e *Chromium) hardenSettings() error {
	var s *ICoreWebViewSettings
	hr, _, _ := e.webview.vtbl.GetSettings.Call(uintptr(unsafe.Pointer(e.webview)), uintptr(unsafe.Pointer(&s)))
	if err := hresult("get_Settings", hr); err != nil {
		return err
	}
	defer s.vtbl.Release.Call(uintptr(unsafe.Pointer(s)))
	v := s.vtbl
	puts := []struct {
		name  string
		proc  ComProc
		value bool
	}{
		{"put_AreDevToolsEnabled", v.PutAreDevToolsEnabled, false},
		{"put_AreDefaultContextMenusEnabled", v.PutAreDefaultContextMenusEnabled, false},
		{"put_IsStatusBarEnabled", v.PutIsStatusBarEnabled, false},
		{"put_AreHostObjectsAllowed", v.PutAreHostObjectsAllowed, false},
		{"put_IsWebMessageEnabled", v.PutIsWebMessageEnabled, true},
		{"put_AreBrowserAcceleratorKeysEnabled", v.PutAreBrowserAcceleratorKeysEnabled, false},
		{"put_IsPasswordAutosaveEnabled", v.PutIsPasswordAutosaveEnabled, false},
		{"put_IsGeneralAutofillEnabled", v.PutIsGeneralAutofillEnabled, false},
		{"put_IsSwipeNavigationEnabled", v.PutIsSwipeNavigationEnabled, false},
	}
	for _, p := range puts {
		hr, _, _ := p.proc.Call(uintptr(unsafe.Pointer(s)), uintptr(boolToInt(p.value)))
		if err := hresult(p.name, hr); err != nil {
			return err
		}
	}
	return nil
}
