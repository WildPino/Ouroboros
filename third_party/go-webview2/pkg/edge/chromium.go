//go:build windows

package edge

import (
	"errors"
	"fmt"
	"sync/atomic"
	"unsafe"

	"github.com/WildPino/ouroboros/third_party/go-webview2/internal/w32"
	"golang.org/x/sys/windows"
)

// Chromium hosts one WebView2 controller in a window. Everything the page
// can reach is denied unless a callback allows it: navigations, web
// messages and resource requests need their callbacks, popups and downloads
// are always refused, and every permission request is denied.
type Chromium struct {
	hwnd                  uintptr
	focusOnInit           bool
	controller            *ICoreWebView2Controller
	webview               *ICoreWebView2
	inited                uintptr
	initErr               error
	envCompleted          *iCoreWebView2CreateCoreWebView2EnvironmentCompletedHandler
	controllerCompleted   *iCoreWebView2CreateCoreWebView2ControllerCompletedHandler
	webMessageReceived    *iCoreWebView2WebMessageReceivedEventHandler
	permissionRequested   *iCoreWebView2PermissionRequestedEventHandler
	webResourceRequested  *iCoreWebView2WebResourceRequestedEventHandler
	acceleratorKeyPressed *ICoreWebView2AcceleratorKeyPressedEventHandler
	navigationCompleted   *ICoreWebView2NavigationCompletedEventHandler
	navigationStarting    *navigationStartingHandler
	newWindowRequested    *newWindowRequestedHandler
	downloadStarting      *downloadStartingHandler

	environment *ICoreWebView2Environment

	// DataPath is the WebView2 user data folder. It must be set.
	DataPath string

	// NavigationAllowed decides whether the top-level document or a frame
	// may navigate to uri. Without it every navigation is cancelled.
	NavigationAllowed func(uri string) bool
	// MessageCallback receives the web messages whose source document
	// MessageSourceAllowed accepts. Without MessageSourceAllowed every
	// message is dropped.
	MessageCallback      func(message string)
	MessageSourceAllowed func(uri string) bool
	// WebResourceRequestedCallback answers the requests that match the
	// filters. A request it leaves without a response gets 403.
	WebResourceRequestedCallback func(request *ICoreWebView2WebResourceRequest, args *ICoreWebView2WebResourceRequestedEventArgs)
	// Refused reports a navigation, popup, download, message or permission
	// that was refused, for logging. It may be nil.
	Refused func(what, uri string)

	NavigationCompletedCallback func(sender *ICoreWebView2, args *ICoreWebView2NavigationCompletedEventArgs)
	AcceleratorKeyCallback      func(uint) bool
}

func NewChromium() *Chromium {
	e := &Chromium{}
	/*
	 All these handlers are passed to native code through syscalls with 'uintptr(unsafe.Pointer(handler))' and we know
	 that a pointer to those will be kept in the native code. Furthermore these handlers als contain pointer to other Go
	 structs like the vtable.
	 This violates the unsafe.Pointer rule '(4) Conversion of a Pointer to a uintptr when calling syscall.Syscall.' because
	 theres no guarantee that Go doesn't move these objects.
	 AFAIK currently the Go runtime doesn't move HEAP objects, so we should be safe with these handlers. But they don't
	 guarantee it, because in the future Go might use a compacting GC.
	 There's a proposal to add a runtime.Pin function, to prevent moving pinned objects, which would allow to easily fix
	 this issue by just pinning the handlers. The https://go-review.googlesource.com/c/go/+/367296/ should land in Go 1.19.
	*/
	e.envCompleted = newICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler(e)
	e.controllerCompleted = newICoreWebView2CreateCoreWebView2ControllerCompletedHandler(e)
	e.webMessageReceived = newICoreWebView2WebMessageReceivedEventHandler(e)
	e.permissionRequested = newICoreWebView2PermissionRequestedEventHandler(e)
	e.webResourceRequested = newICoreWebView2WebResourceRequestedEventHandler(e)
	e.acceleratorKeyPressed = newICoreWebView2AcceleratorKeyPressedEventHandler(e)
	e.navigationCompleted = newICoreWebView2NavigationCompletedEventHandler(e)
	e.navigationStarting = &navigationStartingHandler{vtbl: &navigationStartingFn, e: e}
	e.newWindowRequested = &newWindowRequestedHandler{vtbl: &newWindowRequestedFn, e: e}
	e.downloadStarting = &downloadStartingHandler{vtbl: &downloadStartingFn, e: e}
	return e
}

// Embed creates the browser in hwnd and waits, running the message loop of
// the calling thread, until it is ready or has failed.
func (e *Chromium) Embed(hwnd uintptr) error {
	e.hwnd = hwnd
	if e.DataPath == "" {
		return errors.New("no WebView2 user data folder")
	}
	dataPath, err := windows.UTF16PtrFromString(e.DataPath)
	if err != nil {
		return err
	}
	res, err := createCoreWebView2EnvironmentWithOptions(nil, dataPath, 0, e.envCompleted)
	if err != nil {
		return err
	}
	if res != 0 {
		return fmt.Errorf("creating the WebView2 environment failed with %08x", uint32(res))
	}
	var msg w32.Msg
	for atomic.LoadUintptr(&e.inited) == 0 {
		r, _, _ := w32.User32GetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if r == 0 || int32(r) == -1 {
			return errors.New("the window closed before WebView2 was ready")
		}
		_, _, _ = w32.User32TranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		_, _, _ = w32.User32DispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	return e.initErr
}

func (e *Chromium) fail(err error) uintptr {
	e.initErr = err
	atomic.StoreUintptr(&e.inited, 1)
	return 0
}

func (e *Chromium) Navigate(url string) {
	_, _, _ = e.webview.vtbl.Navigate.Call(
		uintptr(unsafe.Pointer(e.webview)),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(url))),
	)
}

func (e *Chromium) NavigateToString(htmlContent string) {
	_, _, _ = e.webview.vtbl.NavigateToString.Call(
		uintptr(unsafe.Pointer(e.webview)),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(htmlContent))),
	)
}

func (e *Chromium) Init(script string) {
	_, _, _ = e.webview.vtbl.AddScriptToExecuteOnDocumentCreated.Call(
		uintptr(unsafe.Pointer(e.webview)),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(script))),
		0,
	)
}

func (e *Chromium) Eval(script string) {
	_script, err := windows.UTF16PtrFromString(script)
	if err != nil {
		return
	}
	_, _, _ = e.webview.vtbl.ExecuteScript.Call(
		uintptr(unsafe.Pointer(e.webview)),
		uintptr(unsafe.Pointer(_script)),
		0,
	)
}

// PostWebMessageAsJSON sends json to the page, which receives it parsed as
// the data of a message event. It must be called on the window's thread.
func (e *Chromium) PostWebMessageAsJSON(json string) error {
	s, err := windows.UTF16PtrFromString(json)
	if err != nil {
		return err
	}
	hr, _, _ := e.webview.vtbl.PostWebMessageAsJSON.Call(
		uintptr(unsafe.Pointer(e.webview)),
		uintptr(unsafe.Pointer(s)),
	)
	return hresult("PostWebMessageAsJSON", hr)
}

func (e *Chromium) Show() error {
	return e.controller.PutIsVisible(true)
}

func (e *Chromium) Hide() error {
	return e.controller.PutIsVisible(false)
}

// Shown reports whether the browser is visible and covers the whole client
// area of its window. It must be called on the window's thread.
func (e *Chromium) Shown() bool {
	if e.controller == nil {
		return false
	}
	var visible int32
	hr, _, _ := e.controller.vtbl.GetIsVisible.Call(
		uintptr(unsafe.Pointer(e.controller)),
		uintptr(unsafe.Pointer(&visible)),
	)
	if int32(hr) < 0 || visible == 0 {
		return false
	}
	var bounds, client w32.Rect
	hr, _, _ = e.controller.vtbl.GetBounds.Call(
		uintptr(unsafe.Pointer(e.controller)),
		uintptr(unsafe.Pointer(&bounds)),
	)
	if int32(hr) < 0 {
		return false
	}
	r, _, _ := w32.User32GetClientRect.Call(e.hwnd, uintptr(unsafe.Pointer(&client)))
	return r != 0 && client.Right > 0 && client.Bottom > 0 && bounds == client
}

// QueryInterface, AddRef and Release are not used for the handlers, which
// answer QueryInterface themselves (queryHandler); the handlers live as
// long as the Chromium.
func (e *Chromium) QueryInterface(_, _ uintptr) uintptr {
	return eNoInterface
}

func (e *Chromium) AddRef() uintptr {
	return 1
}

func (e *Chromium) Release() uintptr {
	return 1
}

func (e *Chromium) EnvironmentCompleted(res uintptr, env *ICoreWebView2Environment) uintptr {
	if int32(res) < 0 || env == nil {
		return e.fail(fmt.Errorf("creating the WebView2 environment failed with %08x", uint32(res)))
	}
	_, _, _ = env.vtbl.AddRef.Call(uintptr(unsafe.Pointer(env)))
	e.environment = env

	hr, _, _ := env.vtbl.CreateCoreWebView2Controller.Call(
		uintptr(unsafe.Pointer(env)),
		e.hwnd,
		uintptr(unsafe.Pointer(e.controllerCompleted)),
	)
	if int32(hr) < 0 {
		return e.fail(fmt.Errorf("creating the WebView2 controller failed with %08x", uint32(hr)))
	}
	return 0
}

func (e *Chromium) CreateCoreWebView2ControllerCompleted(res uintptr, controller *ICoreWebView2Controller) uintptr {
	if int32(res) < 0 || controller == nil {
		return e.fail(fmt.Errorf("creating the WebView2 controller failed with %08x", uint32(res)))
	}
	_, _, _ = controller.vtbl.AddRef.Call(uintptr(unsafe.Pointer(controller)))
	e.controller = controller

	hr, _, _ := controller.vtbl.GetCoreWebView2.Call(
		uintptr(unsafe.Pointer(controller)),
		uintptr(unsafe.Pointer(&e.webview)),
	)
	if int32(hr) < 0 || e.webview == nil {
		return e.fail(fmt.Errorf("getting the WebView2 failed with %08x", uint32(hr)))
	}
	// The getter already added a reference for the caller.
	if err := e.register(); err != nil {
		return e.fail(err)
	}
	_ = e.controller.AddAcceleratorKeyPressed(e.acceleratorKeyPressed, new(_EventRegistrationToken))

	atomic.StoreUintptr(&e.inited, 1)
	if e.focusOnInit {
		e.Focus()
	}
	return 0
}

// register adds every event handler and hardens the settings. The page is
// not loaded until this has succeeded, so a failure stops the window.
func (e *Chromium) register() error {
	var token _EventRegistrationToken
	v := e.webview.vtbl
	steps := []struct {
		name    string
		proc    ComProc
		handler unsafe.Pointer
	}{
		{"add_NavigationStarting", v.AddNavigationStarting, unsafe.Pointer(e.navigationStarting)},
		{"add_FrameNavigationStarting", v.AddFrameNavigationStarting, unsafe.Pointer(e.navigationStarting)},
		{"add_NewWindowRequested", v.AddNewWindowRequested, unsafe.Pointer(e.newWindowRequested)},
		{"add_PermissionRequested", v.AddPermissionRequested, unsafe.Pointer(e.permissionRequested)},
		{"add_WebMessageReceived", v.AddWebMessageReceived, unsafe.Pointer(e.webMessageReceived)},
		{"add_WebResourceRequested", v.AddWebResourceRequested, unsafe.Pointer(e.webResourceRequested)},
		{"add_NavigationCompleted", v.AddNavigationCompleted, unsafe.Pointer(e.navigationCompleted)},
	}
	for _, s := range steps {
		hr, _, _ := s.proc.Call(
			uintptr(unsafe.Pointer(e.webview)),
			uintptr(s.handler),
			uintptr(unsafe.Pointer(&token)),
		)
		if err := hresult(s.name, hr); err != nil {
			return err
		}
	}
	if err := e.addDownloadStarting(); err != nil {
		return err
	}
	return e.hardenSettings()
}

func (e *Chromium) MessageReceived(sender *ICoreWebView2, args *iCoreWebView2WebMessageReceivedEventArgs) uintptr {
	source := messageSource(args)
	if e.MessageSourceAllowed == nil || !e.MessageSourceAllowed(source) {
		e.refused("message", source)
		return 0
	}
	var message *uint16
	hr, _, _ := args.vtbl.TryGetWebMessageAsString.Call(
		uintptr(unsafe.Pointer(args)),
		uintptr(unsafe.Pointer(&message)),
	)
	if int32(hr) < 0 || message == nil {
		e.refused("message that is not a string", source)
		return 0
	}
	text := windows.UTF16PtrToString(message)
	windows.CoTaskMemFree(unsafe.Pointer(message))
	if e.MessageCallback != nil {
		e.MessageCallback(text)
	}
	return 0
}

// PermissionRequested denies every permission: clipboard, camera,
// microphone, location, notifications and any kind added later.
func (e *Chromium) PermissionRequested(_ *ICoreWebView2, args *iCoreWebView2PermissionRequestedEventArgs) uintptr {
	var kind CoreWebView2PermissionKind
	_, _, _ = args.vtbl.GetPermissionKind.Call(
		uintptr(unsafe.Pointer(args)),
		uintptr(unsafe.Pointer(&kind)),
	)
	_, _, _ = args.vtbl.PutState.Call(
		uintptr(unsafe.Pointer(args)),
		uintptr(CoreWebView2PermissionStateDeny),
	)
	e.refused(fmt.Sprintf("permission %d", kind), "")
	return 0
}

func (e *Chromium) WebResourceRequested(sender *ICoreWebView2, args *ICoreWebView2WebResourceRequestedEventArgs) uintptr {
	var req *ICoreWebView2WebResourceRequest
	hr, _, _ := args.vtbl.GetRequest.Call(uintptr(unsafe.Pointer(args)), uintptr(unsafe.Pointer(&req)))
	if int32(hr) >= 0 && req != nil {
		defer req.vtbl.Release.Call(uintptr(unsafe.Pointer(req)))
		if e.WebResourceRequestedCallback != nil {
			e.WebResourceRequestedCallback(req, args)
		}
	}
	var resp *ICoreWebView2WebResourceResponse
	hr, _, _ = args.vtbl.GetResponse.Call(uintptr(unsafe.Pointer(args)), uintptr(unsafe.Pointer(&resp)))
	if int32(hr) >= 0 && resp != nil {
		_, _, _ = resp.vtbl.Release.Call(uintptr(unsafe.Pointer(resp)))
		return 0
	}
	// Nothing answered: refuse rather than let the request go out.
	uri := ""
	if req != nil {
		uri, _ = RequestURI(req)
	}
	e.refused("request", uri)
	_ = e.Respond(args, nil, 403, "Forbidden", "Content-Type: text/plain")
	return 0
}

// Respond answers a request seen by WebResourceRequestedCallback.
func (e *Chromium) Respond(args *ICoreWebView2WebResourceRequestedEventArgs, content []byte, statusCode int, reasonPhrase, headers string) error {
	var stream uintptr
	if len(content) > 0 {
		var err error
		stream, err = w32.SHCreateMemStream(content)
		if err != nil {
			return err
		}
		defer releaseStream(stream)
	}
	reason, err := windows.UTF16PtrFromString(reasonPhrase)
	if err != nil {
		return err
	}
	h, err := windows.UTF16PtrFromString(headers)
	if err != nil {
		return err
	}
	var response *ICoreWebView2WebResourceResponse
	hr, _, _ := e.environment.vtbl.CreateWebResourceResponse.Call(
		uintptr(unsafe.Pointer(e.environment)),
		stream,
		uintptr(statusCode),
		uintptr(unsafe.Pointer(reason)),
		uintptr(unsafe.Pointer(h)),
		uintptr(unsafe.Pointer(&response)),
	)
	if err := hresult("CreateWebResourceResponse", hr); err != nil {
		return err
	}
	defer response.vtbl.Release.Call(uintptr(unsafe.Pointer(response)))
	hr, _, _ = args.vtbl.PutResponse.Call(uintptr(unsafe.Pointer(args)), uintptr(unsafe.Pointer(response)))
	return hresult("put_Response", hr)
}

func (e *Chromium) AddWebResourceRequestedFilter(filter string, ctx COREWEBVIEW2_WEB_RESOURCE_CONTEXT) error {
	f, err := windows.UTF16PtrFromString(filter)
	if err != nil {
		return err
	}
	hr, _, _ := e.webview.vtbl.AddWebResourceRequestedFilter.Call(
		uintptr(unsafe.Pointer(e.webview)),
		uintptr(unsafe.Pointer(f)),
		uintptr(ctx),
	)
	return hresult("AddWebResourceRequestedFilter", hr)
}

func (e *Chromium) Environment() *ICoreWebView2Environment {
	return e.environment
}

// AcceleratorKeyPressed is called when an accelerator key is pressed.
// If the AcceleratorKeyCallback method has been set, it will defer handling of the keypress
// to the callback. That callback returns a bool indicating if the event was handled.
func (e *Chromium) AcceleratorKeyPressed(sender *ICoreWebView2Controller, args *ICoreWebView2AcceleratorKeyPressedEventArgs) uintptr {
	if e.AcceleratorKeyCallback == nil {
		return 0
	}
	eventKind, _ := args.GetKeyEventKind()
	if eventKind == COREWEBVIEW2_KEY_EVENT_KIND_KEY_DOWN ||
		eventKind == COREWEBVIEW2_KEY_EVENT_KIND_SYSTEM_KEY_DOWN {
		virtualKey, _ := args.GetVirtualKey()
		status, _ := args.GetPhysicalKeyStatus()
		if !status.WasKeyDown {
			_ = args.PutHandled(e.AcceleratorKeyCallback(virtualKey))
			return 0
		}
	}
	_ = args.PutHandled(false)
	return 0
}

func (e *Chromium) GetController() *ICoreWebView2Controller {
	return e.controller
}

func boolToInt(input bool) int {
	if input {
		return 1
	}
	return 0
}

func (e *Chromium) NavigationCompleted(sender *ICoreWebView2, args *ICoreWebView2NavigationCompletedEventArgs) uintptr {
	if e.NavigationCompletedCallback != nil {
		e.NavigationCompletedCallback(sender, args)
	}
	return 0
}

func (e *Chromium) NotifyParentWindowPositionChanged() error {
	//It looks like the wndproc function is called before the controller initialization is complete.
	//Because of this the controller is nil
	if e.controller == nil {
		return nil
	}
	return e.controller.NotifyParentWindowPositionChanged()
}

func (e *Chromium) Focus() {
	if e.controller == nil {
		e.focusOnInit = true
		return
	}
	_ = e.controller.MoveFocus(COREWEBVIEW2_MOVE_FOCUS_REASON_PROGRAMMATIC)
}

func (e *Chromium) refused(what, uri string) {
	if e.Refused != nil {
		e.Refused(what, uri)
	}
}
