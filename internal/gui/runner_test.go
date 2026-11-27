package gui

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadLines(t *testing.T) {
	const (
		ev  = `{"type":"event","command":"run","time":"2026-11-25T10:00:00Z","event":"progress","data":{"phase":"backup"}}`
		res = `{"type":"result","command":"run","time":"2026-11-25T10:00:01Z","data":{"locked":false}}`
		bad = `{"type":"error","command":"run","time":"2026-11-25T10:00:01Z","error":{"code":"locked","message":"a backup is running"}}`
	)
	var events []string
	onEvent := func(name string, data json.RawMessage) { events = append(events, name+" "+string(data)) }

	data, err := readLines(strings.NewReader(ev+"\n\n"+ev+"\r\n"+res+"\n"+ev+"\n"), onEvent)
	if err != nil || string(data) != `{"locked":false}` {
		t.Errorf("result: %s, %v", data, err)
	}
	if len(events) != 2 || events[0] != `progress {"phase":"backup"}` {
		t.Errorf("events %q", events)
	}

	_, err = readLines(strings.NewReader(ev+"\n"+bad+"\n"), nil)
	var c *CLIError
	if !errors.As(err, &c) || c.Code != "locked" || c.Message != "a backup is running" {
		t.Errorf("error line: %v", err)
	}

	data, err = readLines(strings.NewReader(`{"type":"result","command":"pause","time":"2026-11-25T10:00:01Z"}`), nil)
	if err != nil || string(data) != "null" {
		t.Errorf("result without data: %s, %v", data, err)
	}

	for _, in := range []string{"", ev + "\n", "not json\n", `{"type":"log"}` + "\n", `{"type":"error"}` + "\n"} {
		if _, err := readLines(strings.NewReader(in), nil); err == nil {
			t.Errorf("readLines(%q) succeeded", in)
		}
	}
}

func TestExecRunnerNotInstalled(t *testing.T) {
	x := ExecRunner{Path: filepath.Join(t.TempDir(), "bin", "ouroboros.exe")}
	if _, err := x.Run(context.Background(), []string{"status", "--json"}, nil, nil); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("Run: %v, want ErrNotInstalled", err)
	}
	if err := x.Start([]string{"run", "--json"}, nil); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("Start: %v, want ErrNotInstalled", err)
	}
	if e := toError(ErrNotInstalled); e.Code != CodeNotInstalled {
		t.Errorf("code %q", e.Code)
	}
}
