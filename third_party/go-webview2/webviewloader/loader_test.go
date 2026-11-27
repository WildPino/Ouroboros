//go:build windows

package webviewloader

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"110.0.0.0", "110.0.0.0", 0},
		{"131.0.2903.70", "110.0.0.0", 1},
		{"109.0.1518.140", "110.0.0.0", -1},
		{"110.0.0.10", "110.0.0.9", 1},
		{"110.0", "110.0.0.0", -1},
		{"110.0.0.0.1", "110.0.0.0", -1},
		{"110.0.0.x", "1.0.0.0", -1},
		{"110.0.0.01", "110.0.0.1", -1},
		{"", "", 0},
	}
	for _, tt := range tests {
		if got := CompareVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestRuntimeIn(t *testing.T) {
	arch, err := archFolder()
	if err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	install := func(version string) string {
		folder := filepath.Join(root, version)
		dir := filepath.Join(folder, "EBWebView", arch)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "EmbeddedBrowserWebView.dll"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		return folder
	}

	good := install("131.0.2903.70")
	rt, err := runtimeIn(good)
	if err != nil {
		t.Fatal(err)
	}
	if rt.Version != "131.0.2903.70" || rt.DLL != filepath.Join(good, "EBWebView", arch, "EmbeddedBrowserWebView.dll") {
		t.Errorf("runtimeIn = %+v", rt)
	}

	bad := []string{
		install("109.0.1518.140"),                  // too old
		filepath.Join(root, "132.0.0.0"),           // no DLL
		install("current"),                         // not a version
		`EdgeWebView\Application\131.0.2903.70`,    // relative
		`\\server\share\Application\131.0.2903.70`, // network
	}
	for _, folder := range bad {
		if _, err := runtimeIn(folder); !errors.Is(err, ErrRuntimeMissing) {
			t.Errorf("runtimeIn(%q) = %v, want ErrRuntimeMissing", folder, err)
		}
	}
}

func TestClearOverrides(t *testing.T) {
	t.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--remote-debugging-port=9222")
	t.Setenv("WebView2_User_Data_Folder", `C:\elsewhere`)
	t.Setenv("OUROBOROS_KEEP", "1")
	clearOverrides()
	for _, name := range []string{"WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "WEBVIEW2_USER_DATA_FOLDER"} {
		if v, ok := os.LookupEnv(name); ok {
			t.Errorf("%s = %q after clearOverrides", name, v)
		}
	}
	if os.Getenv("OUROBOROS_KEEP") != "1" {
		t.Error("clearOverrides removed an unrelated variable")
	}
}

func TestFindInstalled(t *testing.T) {
	rt, err := Find()
	if errors.Is(err, ErrRuntimeMissing) {
		t.Skip("no WebView2 Runtime on this machine:", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rt.DLL); err != nil {
		t.Errorf("Find returned a missing DLL: %v", err)
	}
}
