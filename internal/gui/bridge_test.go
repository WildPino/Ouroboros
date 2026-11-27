package gui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeCLI answers commands from a table keyed by the joined arguments.
type fakeCLI struct {
	mu      sync.Mutex
	calls   []string
	results map[string]string
	errs    map[string]error
	started []string
	done    func(json.RawMessage, error)
}

func (f *fakeCLI) Run(_ context.Context, args []string, _ io.Reader, _ func(string, json.RawMessage)) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.Join(args, " ")
	f.calls = append(f.calls, key)
	if err := f.errs[key]; err != nil {
		return nil, err
	}
	if r, ok := f.results[key]; ok {
		return json.RawMessage(r), nil
	}
	return json.RawMessage(`{}`), nil
}

func (f *fakeCLI) Start(args []string, done func(json.RawMessage, error)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, strings.Join(args, " "))
	f.done = done
	return nil
}

type fixture struct {
	cli    *fakeCLI
	bridge *Bridge
	picked []string
	events []Event
}

func newFixture() *fixture {
	f := &fixture{cli: &fakeCLI{results: map[string]string{}, errs: map[string]error{}}}
	f.bridge = NewBridge(Deps{
		CLI:  f.cli,
		Info: func() (AppInfo, error) { return AppInfo{Version: "v0.2.0", InstalledVersion: "v0.1.0"}, nil },
		PickFolder: func(_ context.Context, start string) (string, error) {
			f.picked = append(f.picked, start)
			return `D:\Backups`, nil
		},
		Emit: func(e Event) { f.events = append(f.events, e) },
	})
	return f
}

type gotReply struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

func (f *fixture) call(t *testing.T, message string) gotReply {
	t.Helper()
	out, ok := f.bridge.Handle(context.Background(), message)
	if !ok {
		t.Fatalf("no reply to %s", message)
	}
	var r gotReply
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("reply %s: %v", out, err)
	}
	return r
}

func TestBridgeRejectsWhatIsNotACall(t *testing.T) {
	f := newFixture()
	for _, m := range []string{
		``, `not json`, `[]`, `{"id":0,"call":"app.info"}`, `{"id":-1,"call":"app.info"}`,
		`{"id":"1","call":"app.info"}`, `{"id":1.5,"call":"app.info"}`, `{"call":"app.info"}`,
		`{"id":1,"call":"app.info","extra":true}`, `{"id":1,"call":"app.info"} {}`,
		`{"id":1,"call":"app.info","args":{}}` + strings.Repeat(" ", maxMessage),
	} {
		if out, ok := f.bridge.Handle(context.Background(), m); ok {
			t.Errorf("Handle(%.60q) replied %s", m, out)
		}
	}
	if len(f.cli.calls) != 0 {
		t.Errorf("commands ran: %q", f.cli.calls)
	}
}

func TestBridgeInvalidArguments(t *testing.T) {
	f := newFixture()
	for _, m := range []string{
		`{"id":1,"call":"no.such.call"}`,
		`{"id":1,"call":"Status.get","args":{"withSnapshots":true}}`,
		`{"id":1,"call":"app.info","args":{"x":1}}`,
		`{"id":1,"call":"app.info","args":[]}`,
		`{"id":1,"call":"status.get"}`,
		`{"id":1,"call":"status.get","args":{"withSnapshots":"yes"}}`,
		`{"id":1,"call":"status.get","args":{"withSnapshots":true,"cmd":"forget"}}`,
		`{"id":1,"call":"job.backupNow","args":{"job":"-x"}}`,
		`{"id":1,"call":"job.backupNow","args":{"job":"--config=x.toml"}}`,
		`{"id":1,"call":"job.backupNow","args":{"job":"a b"}}`,
		`{"id":1,"call":"job.backupNow","args":{}}`,
		`{"id":1,"call":"snapshots.list","args":{"job":""}}`,
		`{"id":1,"call":"snapshots.ls","args":{"snapshot":"--help"}}`,
		`{"id":1,"call":"snapshots.ls","args":{"snapshot":"ABCDEF12"}}`,
		`{"id":1,"call":"snapshots.ls","args":{"snapshot":"abcdef1"}}`,
		`{"id":1,"call":"snapshots.ls","args":{"snapshot":"abcdef12","folder":"relative\\path"}}`,
		`{"id":1,"call":"snapshots.ls","args":{"snapshot":"abcdef12","folder":"-C:\\x"}}`,
		`{"id":1,"call":"snapshots.ls","args":{"snapshot":"abcdef12","folder":"C:\\a\u0000b"}}`,
		`{"id":1,"call":"dialog.pickFolder","args":{"start":"\\\\server\\share"}}`,
		`{"id":1,"call":"task.pause","args":{"force":true}}`,
	} {
		r := f.call(t, m)
		if r.Error == nil || r.Error.Code != CodeInvalidArgument {
			t.Errorf("%s: reply %+v, want invalid_argument", m, r)
		}
	}
	if len(f.cli.calls)+len(f.cli.started)+len(f.picked) != 0 {
		t.Errorf("something ran: %q %q %q", f.cli.calls, f.cli.started, f.picked)
	}
}

func TestBridgeCalls(t *testing.T) {
	f := newFixture()
	f.cli.results["status --json --no-snapshots"] = `{"running":false,"jobs":[]}`
	f.cli.results["snapshots --json --job docs"] = `{"snapshots":[]}`

	tests := []struct {
		message, command, result string
	}{
		{`{"id":1,"call":"app.info"}`, "", `{"version":"v0.2.0","installedVersion":"v0.1.0","setupNeeded":false}`},
		{`{"id":2,"call":"app.info","args":null}`, "", `{"version":"v0.2.0","installedVersion":"v0.1.0","setupNeeded":false}`},
		{`{"id":3,"call":"status.get","args":{"withSnapshots":false}}`, "status --json --no-snapshots", `{"running":false,"jobs":[]}`},
		{`{"id":4,"call":"status.get","args":{"withSnapshots":true}}`, "status --json", `{}`},
		{`{"id":5,"call":"task.pause","args":{}}`, "pause --json", `{}`},
		{`{"id":6,"call":"task.resume"}`, "resume --json", `{}`},
		{`{"id":7,"call":"snapshots.list","args":{"job":"docs"}}`, "snapshots --json --job docs", `{"snapshots":[]}`},
		{`{"id":8,"call":"snapshots.ls","args":{"snapshot":"abcdef12"}}`, "ls --json -- abcdef12", `{}`},
		{`{"id":9,"call":"snapshots.ls","args":{"snapshot":"abcdef12","folder":"C:\\Users\\me"}}`, `ls --json -- abcdef12 C:\Users\me`, `{}`},
		{`{"id":10,"call":"dialog.pickFolder","args":{"start":"E:\\"}}`, "", `{"path":"D:\\Backups"}`},
	}
	for _, tt := range tests {
		before := len(f.cli.calls)
		r := f.call(t, tt.message)
		if r.Error != nil {
			t.Errorf("%s: error %+v", tt.message, r.Error)
			continue
		}
		if string(r.Result) != tt.result {
			t.Errorf("%s: result %s, want %s", tt.message, r.Result, tt.result)
		}
		var ran []string
		if len(f.cli.calls) > before {
			ran = f.cli.calls[before:]
		}
		if (tt.command == "" && len(ran) != 0) || (tt.command != "" && !slices.Equal(ran, []string{tt.command})) {
			t.Errorf("%s: ran %q, want %q", tt.message, ran, tt.command)
		}
	}
	if !slices.Equal(f.picked, []string{`E:\`}) {
		t.Errorf("picker started in %q", f.picked)
	}
}

func TestBridgeErrors(t *testing.T) {
	f := newFixture()
	f.cli.errs["pause --json"] = &CLIError{Code: "task_not_installed", Message: "the scheduled task is not installed"}
	f.cli.errs["snapshots --json --job docs"] = &CLIError{Code: "wrong_password", Message: "wrong password"}
	f.cli.errs["snapshots --json --job gone"] = &CLIError{Code: "repository_unavailable", Message: "no repository"}
	f.cli.errs["resume --json"] = ErrNotInstalled
	f.cli.errs["status --json"] = context.Canceled
	tests := []struct{ message, code, text string }{
		{`{"id":1,"call":"task.pause"}`, CodeFailed, "the scheduled task is not installed"},
		{`{"id":1,"call":"snapshots.list","args":{"job":"docs"}}`, CodeWrongPassword, "wrong password"},
		{`{"id":1,"call":"snapshots.list","args":{"job":"gone"}}`, CodeRepositoryUnavailable, "no repository"},
		{`{"id":1,"call":"task.resume"}`, CodeNotInstalled, ErrNotInstalled.Error()},
		{`{"id":1,"call":"status.get","args":{"withSnapshots":true}}`, CodeCancelled, "cancelled"},
	}
	for _, tt := range tests {
		r := f.call(t, tt.message)
		if r.Error == nil || r.Error.Code != tt.code || r.Error.Message != tt.text {
			t.Errorf("%s: reply %+v, want %s %q", tt.message, r.Error, tt.code, tt.text)
		}
		if string(r.Result) != "null" {
			t.Errorf("%s: result %s with an error", tt.message, r.Result)
		}
	}
}

func TestBackupNow(t *testing.T) {
	f := newFixture()
	f.cli.results["status --json --no-snapshots"] = `{"running":true}`
	r := f.call(t, `{"id":1,"call":"job.backupNow","args":{"job":"docs"}}`)
	if r.Error == nil || r.Error.Code != CodeLocked {
		t.Fatalf("while running: %+v, want locked", r)
	}
	if len(f.cli.started) != 0 {
		t.Fatalf("started %q while a backup runs", f.cli.started)
	}

	f.cli.results["status --json --no-snapshots"] = `{"running":false}`
	r = f.call(t, `{"id":2,"call":"job.backupNow","args":{"job":"docs"}}`)
	if r.Error != nil || string(r.Result) != `{"started":true}` {
		t.Fatalf("reply %+v", r)
	}
	if !slices.Equal(f.cli.started, []string{"run --json --force --job docs"}) {
		t.Fatalf("started %q", f.cli.started)
	}
	f.cli.done(json.RawMessage(`{"locked":true,"jobs":[]}`), nil)
	f.cli.done(nil, &CLIError{Code: "cannot_reclaim", Message: "not enough space"})
	if len(f.events) != 2 {
		t.Fatalf("events %+v", f.events)
	}
	b, _ := json.Marshal(f.events)
	want := `[{"event":"backup","data":{"job":"docs","result":{"locked":true,"jobs":[]}}},` +
		`{"event":"backup","data":{"job":"docs","error":{"code":"failed","message":"not enough space"}}}]`
	if string(b) != want {
		t.Errorf("events\n%s\nwant\n%s", b, want)
	}
}

func TestStatusFailureStopsBackupNow(t *testing.T) {
	f := newFixture()
	f.cli.errs["status --json --no-snapshots"] = errors.New("boom")
	r := f.call(t, `{"id":1,"call":"job.backupNow","args":{"job":"docs"}}`)
	if r.Error == nil || r.Error.Code != CodeFailed || len(f.cli.started) != 0 {
		t.Errorf("reply %+v, started %q", r, f.cli.started)
	}
}

// TestPageCallsMatchBridge keeps the page's bridge.js and the Go table in
// step: the page may name only calls that exist, and every call has a
// function on the page.
func TestPageCallsMatchBridge(t *testing.T) {
	src, err := os.ReadFile("web/bridge.js")
	if err != nil {
		t.Fatal(err)
	}
	var page []string
	for _, m := range regexp.MustCompile(`call\("([a-zA-Z.]+)"`).FindAllStringSubmatch(string(src), -1) {
		page = append(page, m[1])
	}
	slices.Sort(page)
	page = slices.Compact(page)
	goCalls := newFixture().bridge.Calls()
	slices.Sort(goCalls)
	if !slices.Equal(page, goCalls) {
		t.Errorf("bridge.js calls %q, the bridge has %q", page, goCalls)
	}
}
