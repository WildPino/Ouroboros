//go:build windows

// Package webviewloader finds the installed WebView2 Runtime and creates its
// environment, doing in Go what Microsoft's WebView2Loader.dll does, so no
// loader DLL is searched for on disk or loaded from memory.
//
// Only the machine-wide Evergreen Runtime of the stable channel is used. Its
// registry key under HKEY_LOCAL_MACHINE and its folder in Program Files can
// be changed only by administrators, which matters when the caller runs
// elevated. Per-user installations, preview channels, fixed-version folders
// and the WEBVIEW2_* environment overrides are not honoured.
package webviewloader

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// stableClientState is the EdgeUpdate key of the stable WebView2 Runtime. Its
// EBWebView value is the versioned folder of the installed runtime.
const stableClientState = `SOFTWARE\Microsoft\EdgeUpdate\ClientState\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`

// MinimumVersion is the oldest runtime accepted. The hardening of the host
// calls interfaces that older runtimes lack.
const MinimumVersion = "110.0.0.0"

// ErrRuntimeMissing reports that no usable WebView2 Runtime is installed.
var ErrRuntimeMissing = errors.New("the WebView2 Runtime is not installed")

// runtimeTypeInstalled tells the runtime DLL that it was found through the
// registry, as opposed to a fixed-version folder shipped with the program.
const runtimeTypeInstalled = 0

var (
	loadOnce sync.Once
	create   *windows.Proc
	loadErr  error
)

// Runtime describes the installed WebView2 Runtime.
type Runtime struct {
	Version string // such as 131.0.2903.70
	DLL     string // full path of EmbeddedBrowserWebView.dll
}

// Find returns the installed runtime, or an error wrapping
// ErrRuntimeMissing when there is none or it is older than MinimumVersion.
func Find() (Runtime, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, stableClientState, registry.QUERY_VALUE|registry.WOW64_32KEY)
	if err != nil {
		return Runtime{}, fmt.Errorf("%w: %v", ErrRuntimeMissing, err)
	}
	defer k.Close()
	folder, _, err := k.GetStringValue("EBWebView")
	if err != nil || folder == "" {
		return Runtime{}, fmt.Errorf("%w: no runtime folder in the registry", ErrRuntimeMissing)
	}
	return runtimeIn(folder)
}

// runtimeIn checks the runtime folder named by the registry.
func runtimeIn(folder string) (Runtime, error) {
	if !filepath.IsAbs(folder) || filepath.VolumeName(folder) == "" || strings.HasPrefix(folder, `\\`) {
		return Runtime{}, fmt.Errorf("%w: the runtime folder %q is not a local absolute path", ErrRuntimeMissing, folder)
	}
	folder = filepath.Clean(folder)
	version := filepath.Base(folder)
	if _, ok := parseVersion(version); !ok {
		return Runtime{}, fmt.Errorf("%w: the runtime folder %q does not name a version", ErrRuntimeMissing, folder)
	}
	if CompareVersions(version, MinimumVersion) < 0 {
		return Runtime{}, fmt.Errorf("%w: version %s is older than %s", ErrRuntimeMissing, version, MinimumVersion)
	}
	arch, err := archFolder()
	if err != nil {
		return Runtime{}, err
	}
	dll := filepath.Join(folder, "EBWebView", arch, "EmbeddedBrowserWebView.dll")
	if fi, err := os.Stat(dll); err != nil || !fi.Mode().IsRegular() {
		return Runtime{}, fmt.Errorf("%w: %s is missing", ErrRuntimeMissing, dll)
	}
	return Runtime{Version: version, DLL: dll}, nil
}

func archFolder() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "x64", nil
	case "arm64":
		return "arm64", nil
	case "386":
		return "x86", nil
	}
	return "", fmt.Errorf("WebView2 does not support %s", runtime.GOARCH)
}

// parseVersion parses the four numbers of a runtime version.
func parseVersion(v string) ([4]int, bool) {
	var out [4]int
	parts := strings.Split(v, ".")
	if len(parts) != 4 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || (len(p) > 1 && p[0] == '0') {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// CompareVersions compares two runtime versions as -1, 0 or 1. A version
// that does not parse sorts before every version that does.
func CompareVersions(a, b string) int {
	va, oka := parseVersion(a)
	vb, okb := parseVersion(b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	}
	for i := range va {
		if va[i] != vb[i] {
			if va[i] < vb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// GetInstalledVersion returns the installed runtime version, or "" when no
// usable runtime is installed.
func GetInstalledVersion() (string, error) {
	rt, err := Find()
	if errors.Is(err, ErrRuntimeMissing) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return rt.Version, nil
}

// clearOverrides removes the WEBVIEW2_* variables from the environment of
// this process. They can add browser arguments, such as a remote debugging
// port, or move the user data folder, and a program that is not elevated
// can set them for the user whose elevated process inherits them.
func clearOverrides() {
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(strings.ToUpper(name), "WEBVIEW2_") {
			_ = os.Unsetenv(name)
		}
	}
}

func load() error {
	loadOnce.Do(func() {
		clearOverrides()
		var rt Runtime
		rt, loadErr = Find()
		if loadErr != nil {
			return
		}
		// The DLL's own dependencies are searched in its folder and in
		// System32 only, never in the folder of the executable, which may
		// be a downloads folder.
		h, err := windows.LoadLibraryEx(rt.DLL, 0,
			windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
		if err != nil {
			loadErr = fmt.Errorf("loading %s: %w", rt.DLL, err)
			return
		}
		dll := &windows.DLL{Name: rt.DLL, Handle: h}
		create, loadErr = dll.FindProc("CreateWebViewEnvironmentWithOptionsInternal")
		if loadErr != nil {
			loadErr = fmt.Errorf("%s: %w", rt.DLL, loadErr)
		}
	})
	return loadErr
}

// CreateCoreWebView2EnvironmentWithOptions creates the environment as the
// function of the same name in WebView2Loader.dll does. browserExecutableFolder
// must be nil: only the installed runtime is used. The result is the HRESULT
// of the call; the handler is invoked later, on the calling thread's message
// loop.
func CreateCoreWebView2EnvironmentWithOptions(browserExecutableFolder, userDataFolder *uint16, environmentOptions uintptr, environmentCompletedHandle uintptr) (uintptr, error) {
	if browserExecutableFolder != nil {
		return 0, errors.New("a fixed-version runtime folder is not supported")
	}
	if err := load(); err != nil {
		return 0, err
	}
	if environmentOptions == 0 {
		environmentOptions = uintptr(unsafe.Pointer(fixedEnvOptions))
	}
	// The first argument is always 1 in WebView2Loader.dll.
	hr, _, _ := create.Call(
		1,
		runtimeTypeInstalled,
		uintptr(unsafe.Pointer(userDataFolder)),
		environmentOptions,
		environmentCompletedHandle,
	)
	return hr, nil
}
