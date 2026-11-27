package gui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingCLI answers every command with an empty result and records it.
type recordingCLI struct {
	mu    sync.Mutex
	calls [][]string
}

func (r *recordingCLI) Run(_ context.Context, args []string, _ io.Reader, _ func(string, json.RawMessage)) (json.RawMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, args)
	return json.RawMessage(`{}`), nil
}

func (r *recordingCLI) Start(args []string, _ func(json.RawMessage, error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, args)
	return nil
}

// TestWindowHardening opens the real window on a hostile page and checks
// that everything the page tries outside the contract is refused. It needs
// the WebView2 Runtime and a desktop, so it runs only with
// OUROBOROS_WINDOW_TESTS=1.
func TestWindowHardening(t *testing.T) {
	if os.Getenv("OUROBOROS_WINDOW_TESTS") != "1" {
		t.Skip("set OUROBOROS_WINDOW_TESTS=1 to open the window")
	}
	var (
		mu      sync.Mutex
		refused []string
		infos   int
	)
	finished := make(chan struct{})
	closeCh := make(chan func(), 1)
	cli := &recordingCLI{}
	opts := Options{
		DataPath:  webViewData(t),
		CLI:       cli,
		Files:     os.DirFS("testdata/hostile"),
		StartPage: Origin + "/index.html",
		Refused: func(what, uri string) {
			mu.Lock()
			defer mu.Unlock()
			refused = append(refused, what+" "+uri)
		},
		Info: func() (AppInfo, error) {
			mu.Lock()
			infos++
			mu.Unlock()
			select {
			case <-finished:
			default:
				close(finished)
			}
			return AppInfo{Version: "test"}, nil
		},
		Ready: func(close func()) { closeCh <- close },
	}

	errCh := make(chan error, 1)
	go func() { errCh <- Run(opts) }()

	var closeWindow func()
	select {
	case closeWindow = <-closeCh:
	case err := <-errCh:
		if errors.Is(err, ErrRuntimeMissing) {
			t.Skip(err)
		}
		t.Fatalf("Run ended before the page loaded: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the page did not load")
	}

	if _, err := singleInstance(); !errors.Is(err, ErrAlreadyOpen) {
		t.Errorf("a second window: err = %v, want ErrAlreadyOpen", err)
	}

	select {
	case <-finished:
		time.Sleep(time.Second) // let the last refusals arrive
	case <-time.After(30 * time.Second):
		t.Error("the page did not finish")
	}
	closeWindow()
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, r := range refused {
		t.Log("refused:", r)
	}
	want := []string{
		"new window https://example.com/popup",
		"new window " + Origin + "/index.html",
		"download",
		"navigation https://example.com/away",
		"request GET (404) " + Origin + "/missing.txt",
		"request POST (405) " + Origin + "/index.html",
		"malformed message",
	}
	for _, w := range want {
		if !slices.ContainsFunc(refused, func(r string) bool { return strings.HasPrefix(r, w) }) {
			t.Errorf("nothing refused as %q", w)
		}
	}
	if infos != 1 {
		t.Errorf("app.info ran %d times, want 1: only the valid call may run, and inline scripts must not", infos)
	}
	cli.mu.Lock()
	defer cli.mu.Unlock()
	wantCalls := [][]string{{"status", "--json", "--no-snapshots"}}
	if !slices.EqualFunc(cli.calls, wantCalls, slices.Equal) {
		t.Errorf("command line calls = %q, want %q", cli.calls, wantCalls)
	}
}

// webViewData returns a temporary user data folder. The browser processes
// keep files open for a moment after the window closes, so removal is
// retried.
func webViewData(t *testing.T) string {
	dir, err := os.MkdirTemp("", "ouroboros-webview")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := 0; i < 50; i++ {
			if err = os.RemoveAll(dir); err == nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Logf("could not remove %s: %v", dir, err)
	})
	return dir
}

// TestWindowPage opens the real page and waits for it to ask, through the
// bridge, for the versions and for the status.
func TestWindowPage(t *testing.T) {
	if os.Getenv("OUROBOROS_WINDOW_TESTS") != "1" {
		t.Skip("set OUROBOROS_WINDOW_TESTS=1 to open the window")
	}
	cli := &recordingCLI{}
	closeCh := make(chan func(), 1)
	infoCh := make(chan struct{}, 1)
	var refused []string
	var mu sync.Mutex
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(Options{
			DataPath: webViewData(t),
			CLI:      cli,
			Info: func() (AppInfo, error) {
				infoCh <- struct{}{}
				return AppInfo{Version: "test"}, nil
			},
			Refused: func(what, uri string) {
				mu.Lock()
				defer mu.Unlock()
				refused = append(refused, what+" "+uri)
			},
			Ready: func(close func()) { closeCh <- close },
		})
	}()
	var closeWindow func()
	select {
	case closeWindow = <-closeCh:
	case err := <-errCh:
		if errors.Is(err, ErrRuntimeMissing) {
			t.Skip(err)
		}
		t.Fatalf("Run ended before the page loaded: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the page did not load")
	}
	select {
	case <-infoCh:
	case <-time.After(15 * time.Second):
		t.Error("the page did not call app.info")
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		cli.mu.Lock()
		n := len(cli.calls)
		cli.mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	closeWindow()
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	cli.mu.Lock()
	defer cli.mu.Unlock()
	if want := [][]string{{"status", "--json", "--no-snapshots"}}; !slices.EqualFunc(cli.calls, want, slices.Equal) {
		t.Errorf("command line calls = %q, want %q", cli.calls, want)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, r := range refused {
		if !strings.HasPrefix(r, "request GET (404) "+Origin+"/favicon.ico") {
			t.Errorf("the page itself was refused: %s", r)
		}
	}
}
