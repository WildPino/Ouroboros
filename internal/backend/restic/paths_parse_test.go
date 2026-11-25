package restic

import (
	"bytes"
	"testing"
)

func TestParseLsLines(t *testing.T) {
	var nodes []string
	for line := range bytes.Lines(fixture(t, "ls.jsonl")) {
		n, ok, err := parseLsLine(bytes.TrimSpace(line))
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			nodes = append(nodes, n.Type+" "+n.Path)
		}
	}
	want := []string{"dir /C/data/src", "file /C/data/src/f1.bin", "dir /C/data/src/sub"}
	if len(nodes) != len(want) {
		t.Fatalf("nodes %q", nodes)
	}
	for i := range want {
		if nodes[i] != want[i] {
			t.Errorf("node %d = %q, want %q", i, nodes[i], want[i])
		}
	}
	if _, _, err := parseLsLine([]byte("garbage")); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestSnapshotAndWindowsPath(t *testing.T) {
	for win, snap := range map[string]string{
		`C:\Users\me`:      "/C/Users/me",
		`c:\Users\me\`:     "/C/Users/me",
		`C:\`:              "/C",
		`D:`:               "/D",
		"/C/Users/me":      "/C/Users/me",
		"/":                "/",
		`\\server\share\x`: "//server/share/x",
	} {
		if got := SnapshotPath(win); got != snap {
			t.Errorf("SnapshotPath(%q) = %q, want %q", win, got, snap)
		}
	}
	for snap, win := range map[string]string{
		"/C/Users/me": `C:\Users\me`,
		"/C":          `C:\`,
		"/":           `\`,
	} {
		if got := WindowsPath(snap); got != win {
			t.Errorf("WindowsPath(%q) = %q, want %q", snap, got, win)
		}
	}
}
