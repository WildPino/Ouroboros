package gui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	webview2 "github.com/WildPino/ouroboros/third_party/go-webview2"
	"github.com/WildPino/ouroboros/third_party/go-webview2/pkg/edge"
	"github.com/WildPino/ouroboros/third_party/go-webview2/webviewloader"
)

// ErrRuntimeMissing reports that the WebView2 Runtime is not installed or
// too old, so the window cannot open.
var ErrRuntimeMissing = webviewloader.ErrRuntimeMissing

// ErrAlreadyOpen reports that another window of Ouroboros was brought to
// the front instead of opening a second one.
var ErrAlreadyOpen = errors.New("the window is already open")

// ClassName is the window class of the Ouroboros window, by which a second
// start and setup find it.
const ClassName = "OuroborosWindow"

// Options configure the window.
type Options struct {
	// DataPath is the WebView2 user data folder.
	DataPath string
	// CLI and Info are passed to the bridge.
	CLI  Runner
	Info func() (AppInfo, error)
	// Files are the page files; nil means the embedded ones.
	Files fs.FS
	// StartPage is the first page; empty means StartPage.
	StartPage string
	// Refused, if set, is told about every navigation, request, message,
	// popup, download or permission the window refused.
	Refused func(what, uri string)
	// Ready, if set, is called on the window's thread once the page is
	// loaded, with a function that closes the window and whether the browser
	// is visible and fills the window.
	Ready func(close func(), shown bool)
}

// Run opens the window and returns when it is closed. If another window of
// Ouroboros is open in this session, it brings that one to the front and
// returns ErrAlreadyOpen.
func Run(opts Options) error {
	// The window, WebView2 and the native dialogs need one thread with a
	// single-threaded COM apartment for their whole life.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	switch err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); {
	case err == nil, errors.Is(err, windows.Errno(1)): // S_OK, S_FALSE
		defer windows.CoUninitialize()
	default:
		return fmt.Errorf("initialize COM: %w", err)
	}

	release, err := singleInstance()
	if err != nil {
		return err
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, err := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  opts.DataPath,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:     "Ouroboros",
			Width:     960,
			Height:    680,
			IconName:  "APP",
			Center:    true,
			ClassName: ClassName,
		},
	})
	if err != nil {
		return err
	}
	c := w.Chromium()
	files := opts.Files
	if files == nil {
		files = WebFS()
	}
	refused := func(what, uri string) {
		if opts.Refused != nil {
			opts.Refused(what, uri)
		}
	}
	c.Refused = refused
	c.NavigationAllowed = SameOrigin
	c.MessageSourceAllowed = SameOrigin
	c.WebResourceRequestedCallback = func(req *edge.ICoreWebView2WebResourceRequest, args *edge.ICoreWebView2WebResourceRequestedEventArgs) {
		uri, err := edge.RequestURI(req)
		if err != nil {
			return
		}
		method, err := edge.RequestMethod(req)
		if err != nil {
			return
		}
		r := Serve(files, method, uri)
		if r.Status != 200 {
			refused(fmt.Sprintf("request %s (%d)", method, r.Status), uri)
		}
		_ = c.Respond(args, r.Body, r.Status, r.Reason, r.Headers)
	}
	if err := c.AddWebResourceRequestedFilter("*", edge.COREWEBVIEW2_WEB_RESOURCE_CONTEXT_ALL); err != nil {
		w.Destroy()
		return err
	}

	post := func(v []byte) {
		w.Dispatch(func() { _ = c.PostWebMessageAsJSON(string(v)) })
	}
	bridge := NewBridge(Deps{
		CLI:  opts.CLI,
		Info: opts.Info,
		PickFolder: func(ctx context.Context, start string) (string, error) {
			return onWindowThread(ctx, w, func() (string, error) { return pickFolder(w.HWND(), start) })
		},
		Emit: func(e Event) {
			if b, err := json.Marshal(e); err == nil {
				post(b)
			}
		},
	})
	c.MessageCallback = func(message string) {
		go func() {
			if out, ok := bridge.Handle(ctx, message); ok {
				post(out)
			} else {
				refused("malformed message", "")
			}
		}()
	}
	if opts.Ready != nil {
		var once sync.Once
		c.NavigationCompletedCallback = func(*edge.ICoreWebView2, *edge.ICoreWebView2NavigationCompletedEventArgs) {
			once.Do(func() { opts.Ready(w.Destroy, c.Shown()) })
		}
	}

	start := opts.StartPage
	if start == "" {
		start = StartPage
	}
	w.Navigate(start)
	w.Run()
	return nil
}

// onWindowThread runs f from the window's message loop, as native dialogs
// need, and waits for it.
func onWindowThread(ctx context.Context, w webview2.WebView, f func() (string, error)) (string, error) {
	type result struct {
		s   string
		err error
	}
	done := make(chan result, 1)
	w.Dispatch(func() {
		s, err := f()
		done <- result{s, err}
	})
	select {
	case r := <-done:
		return r.s, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

var (
	user32              = windows.NewLazySystemDLL("user32.dll")
	findWindow          = user32.NewProc("FindWindowW")
	setForegroundWindow = user32.NewProc("SetForegroundWindow")
	showWindow          = user32.NewProc("ShowWindow")
	isIconic            = user32.NewProc("IsIconic")
)

// FindWindow returns the Ouroboros window open in this session, or 0.
func FindWindow() uintptr {
	class, _ := windows.UTF16PtrFromString(ClassName)
	h, _, _ := findWindow.Call(uintptr(unsafe.Pointer(class)), 0)
	return h
}

// singleInstance makes sure only one window runs per session. A named
// mutex decides between two windows started at once; the second brings
// the first to the front. If the mutex exists but no window appears within
// a few seconds, as when another program holds the name, the window opens
// anyway.
func singleInstance() (func(), error) {
	name, _ := windows.UTF16PtrFromString(`Local\Ouroboros.Window`)
	h, err := windows.CreateMutex(nil, false, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return func() {}, nil
	}
	release := func() { _ = windows.CloseHandle(h) }
	if err == nil {
		return release, nil
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if hwnd := FindWindow(); hwnd != 0 {
			const swRestore = 9
			if r, _, _ := isIconic.Call(hwnd); r != 0 {
				_, _, _ = showWindow.Call(hwnd, swRestore)
			}
			_, _, _ = setForegroundWindow.Call(hwnd)
			release()
			return nil, ErrAlreadyOpen
		}
	}
	return release, nil
}
