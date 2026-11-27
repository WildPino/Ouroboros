//go:build windows

package webview2

import (
	"errors"
	"sync"
	"unsafe"

	"github.com/WildPino/ouroboros/third_party/go-webview2/internal/w32"
	"github.com/WildPino/ouroboros/third_party/go-webview2/pkg/edge"

	"golang.org/x/sys/windows"
)

var (
	windowContext     = map[uintptr]interface{}{}
	windowContextSync sync.RWMutex

	getDpiForSystem = windows.NewLazySystemDLL("user32.dll").NewProc("GetDpiForSystem")
)

// sizeMinimized is SIZE_MINIMIZED, the wParam of WM_SIZE.
const sizeMinimized = 1

func getWindowContext(wnd uintptr) interface{} {
	windowContextSync.RLock()
	defer windowContextSync.RUnlock()
	return windowContext[wnd]
}

func setWindowContext(wnd uintptr, data interface{}) {
	windowContextSync.Lock()
	defer windowContextSync.Unlock()
	windowContext[wnd] = data
}

type webview struct {
	hwnd       uintptr
	mainthread uintptr
	browser    *edge.Chromium
	autofocus  bool
	maxsz      w32.Point
	minsz      w32.Point
	m          sync.Mutex
	dispatchq  []func()
}

type WindowOptions struct {
	Title string
	// Width and Height are in pixels at 96 DPI and are scaled to the
	// system DPI.
	Width  uint
	Height uint
	// IconName is the name of an icon resource of the executable.
	IconName string
	Center   bool
	// ClassName is the window class, which other programs can use to find
	// the window. The default is "webview".
	ClassName string
}

type WebViewOptions struct {
	// DataPath specifies the datapath for the WebView2 runtime to use for the
	// browser instance. It must be set.
	DataPath string

	// AutoFocus will try to keep the WebView2 widget focused when the window
	// is focused.
	AutoFocus bool

	// WindowOptions customizes the window that is created to embed the
	// WebView2 widget.
	WindowOptions WindowOptions
}

// NewWithOptions creates a new webview in a new window. The window is shown
// only once the browser is ready. Developer tools and the other features
// listed in edge.Chromium are always off, and every navigation, message and
// request is refused until the caller sets the browser's callbacks and
// filters, which it must do before Navigate.
func NewWithOptions(options WebViewOptions) (WebView, error) {
	w := &webview{}
	w.autofocus = options.AutoFocus

	chromium := edge.NewChromium()
	chromium.DataPath = options.DataPath

	w.browser = chromium
	w.mainthread, _, _ = w32.Kernel32GetCurrentThreadID.Call()
	if err := w.create(options.WindowOptions); err != nil {
		return nil, err
	}
	return w, nil
}

func wndproc(hwnd, msg, wp, lp uintptr) uintptr {
	if w, ok := getWindowContext(hwnd).(*webview); ok {
		switch msg {
		case w32.WMMove, w32.WMMoving:
			_ = w.browser.NotifyParentWindowPositionChanged()
		case w32.WMNCLButtonDown:
			_, _, _ = w32.User32SetFocus.Call(w.hwnd)
			r, _, _ := w32.User32DefWindowProcW.Call(hwnd, msg, wp, lp)
			return r
		case w32.WMSize:
			// A minimized browser stops rendering; it must be made visible
			// again on restore.
			if wp == sizeMinimized {
				_ = w.browser.Hide()
				break
			}
			_ = w.browser.Show()
			w.browser.Resize()
		case w32.WMActivate:
			if wp == w32.WAInactive {
				break
			}
			if w.autofocus {
				w.browser.Focus()
			}
		case w32.WMClose:
			_, _, _ = w32.User32DestroyWindow.Call(hwnd)
		case w32.WMDestroy:
			w.Terminate()
		case w32.WMGetMinMaxInfo:
			// lp points to a MINMAXINFO owned by the system.
			lpmmi := *(**w32.MinMaxInfo)(unsafe.Pointer(&lp))
			if w.maxsz.X > 0 && w.maxsz.Y > 0 {
				lpmmi.PtMaxSize = w.maxsz
				lpmmi.PtMaxTrackSize = w.maxsz
			}
			if w.minsz.X > 0 && w.minsz.Y > 0 {
				lpmmi.PtMinTrackSize = w.minsz
			}
		default:
			r, _, _ := w32.User32DefWindowProcW.Call(hwnd, msg, wp, lp)
			return r
		}
		return 0
	}
	r, _, _ := w32.User32DefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

// scale converts a size at 96 DPI to the system DPI.
func scale(v uint) uint {
	dpi, _, _ := getDpiForSystem.Call()
	if dpi == 0 {
		return v
	}
	return v * uint(dpi) / 96
}

func (w *webview) create(opts WindowOptions) error {
	var hinstance windows.Handle
	_ = windows.GetModuleHandleEx(0, nil, &hinstance)

	var icon, smallIcon uintptr
	if opts.IconName != "" {
		name, err := windows.UTF16PtrFromString(opts.IconName)
		if err != nil {
			return err
		}
		const imageIcon = 1
		icon, _, _ = w32.User32LoadImageW.Call(uintptr(hinstance), uintptr(unsafe.Pointer(name)), imageIcon, 0, 0, w32.LR_DEFAULTSIZE|w32.LR_SHARED)
		cx, _, _ := w32.User32GetSystemMetrics.Call(w32.SystemMetricsCxSmIcon)
		cy, _, _ := w32.User32GetSystemMetrics.Call(w32.SystemMetricsCySmIcon)
		smallIcon, _, _ = w32.User32LoadImageW.Call(uintptr(hinstance), uintptr(unsafe.Pointer(name)), imageIcon, cx, cy, w32.LR_SHARED)
	}

	class := opts.ClassName
	if class == "" {
		class = "webview"
	}
	className, err := windows.UTF16PtrFromString(class)
	if err != nil {
		return err
	}
	wc := w32.WndClassExW{
		CbSize:        uint32(unsafe.Sizeof(w32.WndClassExW{})),
		HInstance:     hinstance,
		LpszClassName: className,
		HIcon:         windows.Handle(icon),
		HIconSm:       windows.Handle(smallIcon),
		LpfnWndProc:   windows.NewCallback(wndproc),
	}
	_, _, _ = w32.User32RegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	windowName, err := windows.UTF16PtrFromString(opts.Title)
	if err != nil {
		return err
	}

	windowWidth := opts.Width
	if windowWidth == 0 {
		windowWidth = 640
	}
	windowHeight := opts.Height
	if windowHeight == 0 {
		windowHeight = 480
	}
	windowWidth, windowHeight = scale(windowWidth), scale(windowHeight)

	var posX, posY uint
	if opts.Center {
		screenWidth, _, _ := w32.User32GetSystemMetrics.Call(w32.SM_CXSCREEN)
		screenHeight, _, _ := w32.User32GetSystemMetrics.Call(w32.SM_CYSCREEN)
		if uint(screenWidth) > windowWidth && uint(screenHeight) > windowHeight {
			posX = (uint(screenWidth) - windowWidth) / 2
			posY = (uint(screenHeight) - windowHeight) / 2
		}
	} else {
		posX = w32.CW_USEDEFAULT
		posY = w32.CW_USEDEFAULT
	}

	w.hwnd, _, _ = w32.User32CreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowName)),
		0xCF0000, // WS_OVERLAPPEDWINDOW
		uintptr(posX),
		uintptr(posY),
		uintptr(windowWidth),
		uintptr(windowHeight),
		0,
		0,
		uintptr(hinstance),
		0,
	)
	if w.hwnd == 0 {
		return errors.New("the window could not be created")
	}
	setWindowContext(w.hwnd, w)

	if err := w.browser.Embed(w.hwnd); err != nil {
		_, _, _ = w32.User32DestroyWindow.Call(w.hwnd)
		return err
	}
	_, _, _ = w32.User32ShowWindow.Call(w.hwnd, w32.SWShow)
	// The browser was created in a hidden window, so it starts hidden too.
	_ = w.browser.Show()
	w.browser.Resize()
	_, _, _ = w32.User32UpdateWindow.Call(w.hwnd)
	_, _, _ = w32.User32SetFocus.Call(w.hwnd)
	return nil
}

func (w *webview) Destroy() {
	_, _, _ = w32.User32PostMessageW.Call(w.hwnd, w32.WMClose, 0, 0)
}

func (w *webview) Run() {
	var msg w32.Msg
	for {
		_, _, _ = w32.User32GetMessageW.Call(
			uintptr(unsafe.Pointer(&msg)),
			0,
			0,
			0,
		)
		if msg.Message == w32.WMApp {
			w.m.Lock()
			q := append([]func(){}, w.dispatchq...)
			w.dispatchq = []func(){}
			w.m.Unlock()
			for _, v := range q {
				v()
			}
		} else if msg.Message == w32.WMQuit {
			return
		}
		r, _, _ := w32.User32GetAncestor.Call(uintptr(msg.Hwnd), w32.GARoot)
		r, _, _ = w32.User32IsDialogMessage.Call(r, uintptr(unsafe.Pointer(&msg)))
		if r != 0 {
			continue
		}
		_, _, _ = w32.User32TranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		_, _, _ = w32.User32DispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (w *webview) Terminate() {
	_, _, _ = w32.User32PostQuitMessage.Call(0)
}

func (w *webview) Window() unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&w.hwnd))
}

func (w *webview) HWND() uintptr {
	return w.hwnd
}

func (w *webview) Chromium() *edge.Chromium {
	return w.browser
}

func (w *webview) Navigate(url string) {
	w.browser.Navigate(url)
}

func (w *webview) SetHtml(html string) {
	w.browser.NavigateToString(html)
}

func (w *webview) SetTitle(title string) {
	_title, err := windows.UTF16FromString(title)
	if err != nil {
		_title, _ = windows.UTF16FromString("")
	}
	_, _, _ = w32.User32SetWindowTextW.Call(w.hwnd, uintptr(unsafe.Pointer(&_title[0])))
}

func (w *webview) SetSize(width int, height int, hints Hint) {
	index := w32.GWLStyle
	style := w32.GetWindowLong(w.hwnd, index)
	if hints == HintFixed {
		style &^= (w32.WSThickFrame | w32.WSMaximizeBox)
	} else {
		style |= (w32.WSThickFrame | w32.WSMaximizeBox)
	}
	w32.SetWindowLong(w.hwnd, index, style)

	if hints == HintMax {
		w.maxsz.X = int32(width)
		w.maxsz.Y = int32(height)
	} else if hints == HintMin {
		w.minsz.X = int32(width)
		w.minsz.Y = int32(height)
	} else {
		r := w32.Rect{}
		r.Left = 0
		r.Top = 0
		r.Right = int32(width)
		r.Bottom = int32(height)
		_, _, _ = w32.User32AdjustWindowRect.Call(uintptr(unsafe.Pointer(&r)), w32.WSOverlappedWindow, 0)
		_, _, _ = w32.User32SetWindowPos.Call(
			w.hwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top),
			w32.SWPNoZOrder|w32.SWPNoActivate|w32.SWPNoMove|w32.SWPFrameChanged)
		w.browser.Resize()
	}
}

func (w *webview) Init(js string) {
	w.browser.Init(js)
}

func (w *webview) Eval(js string) {
	w.browser.Eval(js)
}

func (w *webview) Dispatch(f func()) {
	w.m.Lock()
	w.dispatchq = append(w.dispatchq, f)
	w.m.Unlock()
	_, _, _ = w32.User32PostThreadMessageW.Call(w.mainthread, w32.WMApp, 0, 0)
}
