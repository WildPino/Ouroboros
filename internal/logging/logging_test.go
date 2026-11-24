package logging

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestRotatingFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.Local)
	w, err := OpenRotating(dir, 3)
	if err != nil {
		t.Fatal(err)
	}
	w.Now = func() time.Time { return now }
	w.MaxSize = 100

	// Old files, one foreign file that must survive cleanup.
	for _, n := range []string{"ouroboros-2026-05-01.log", "ouroboros-2026-05-06.2.log", "ouroboros-2026-05-08.log", "notes.txt"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644)
	}

	line := strings.Repeat("a", 39) + "\n" // 40 bytes
	for range 5 {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	// 2 lines per file: 80 bytes, a third would exceed 100.
	want := []string{"notes.txt", "ouroboros-2026-05-08.log", "ouroboros-2026-05-10.1.log", "ouroboros-2026-05-10.2.log", "ouroboros-2026-05-10.log"}
	if got := listDir(t, dir); !slices.Equal(got, want) {
		t.Fatalf("files %v, want %v", got, want)
	}

	// Next day: new file; a later run continues the last file of the day.
	now = now.Add(24 * time.Hour)
	w.Write([]byte(line))
	w.Close()
	w2, _ := OpenRotating(dir, 3)
	w2.Now, w2.MaxSize = w.Now, 100
	w2.Write([]byte(line))
	w2.Write([]byte(line))
	w2.Close()
	data, _ := os.ReadFile(filepath.Join(dir, "ouroboros-2026-05-11.log"))
	if len(data) != 80 {
		t.Fatalf("day file has %d bytes", len(data))
	}
	if !slices.Contains(listDir(t, dir), "ouroboros-2026-05-11.1.log") {
		t.Fatalf("overflow file missing: %v", listDir(t, dir))
	}
}

type sink struct{ warnings, errors []string }

func (s *sink) Warning(eid uint32, msg string) error {
	if eid != EventWarning {
		return errors.New("bad id")
	}
	s.warnings = append(s.warnings, msg)
	return nil
}

func (s *sink) Error(eid uint32, msg string) error {
	if eid != EventError {
		return errors.New("bad id")
	}
	s.errors = append(s.errors, msg)
	return nil
}

func TestEventHandler(t *testing.T) {
	s := &sink{}
	log := slog.New(NewEventHandler(s)).With("job", "docs")
	log.Info("ignored")
	log.Warn("unreadable source", "path", `C:\x y`)
	log.WithGroup("g").Error("backup failed", "err", "disk full")
	if !slices.Equal(s.warnings, []string{`msg="unreadable source" job=docs path="C:\\x y"`}) {
		t.Fatalf("warnings %q", s.warnings)
	}
	if !slices.Equal(s.errors, []string{`msg="backup failed" job=docs g.err="disk full"`}) {
		t.Fatalf("errors %q", s.errors)
	}
	if NewEventHandler(s).Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("info enabled")
	}
}
