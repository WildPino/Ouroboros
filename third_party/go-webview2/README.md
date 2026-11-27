# go-webview2 (modified copy)

A copy of [github.com/jchv/go-webview2](https://github.com/jchv/go-webview2)
at commit `56598839c808` (2026-02-05), MIT License (see `LICENSE`). It is
part of the Ouroboros module so the window can be hardened as described in
`docs/DESIGN.md` §12.3 and §12.6. Only the files the window needs are kept:
the root package, `pkg/edge` and `internal/w32`; the demo and the embedded
`WebView2Loader.dll` are left out.

## Changes from upstream

- Import paths point into this module, and every file builds on Windows only.
- `webviewloader` is replaced by a loader written in Go. Upstream searches
  the DLL search path for `WebView2Loader.dll`, which starts in the folder of
  the executable, and otherwise loads an embedded copy from memory with
  `go-winloader`. The new loader reads the folder of the machine-wide stable
  WebView2 Runtime from `HKEY_LOCAL_MACHINE`, loads its
  `EmbeddedBrowserWebView.dll` with its dependencies searched only in its own
  folder and in System32, and calls `CreateWebViewEnvironmentWithOptionsInternal`
  with a fixed environment options object. Runtimes older than
  `webviewloader.MinimumVersion` are refused, and the `WEBVIEW2_*`
  environment variables are removed from the process first.
- The event handlers answer `QueryInterface` with themselves for `IUnknown`
  and their own interface, and refuse any other. Upstream returned success
  without a result, which the runtime rejects with `E_POINTER` when it is
  called without `WebView2Loader.dll`.
- `go vet` fixes for conversions of system pointers held in `uintptr`.
