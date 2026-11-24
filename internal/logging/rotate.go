// Package logging writes structured logs to rotated files and reports
// warnings and errors to the Windows Event Log.
package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// DefaultMaxSize is the size after which a new file is started on the
// same day.
const DefaultMaxSize = 10 << 20

const prefix = "ouroboros-"

// fileName matches ouroboros-YYYY-MM-DD.log and ouroboros-YYYY-MM-DD.N.log.
var fileName = regexp.MustCompile(`^ouroboros-(\d{4}-\d{2}-\d{2})(?:\.(\d+))?\.log$`)

// RotatingFile is an io.Writer that writes one file per day, starts a new
// file when the current one exceeds MaxSize, and deletes files older than
// KeepDays. It is safe for concurrent use.
type RotatingFile struct {
	Dir      string
	MaxSize  int64
	KeepDays int
	Now      func() time.Time

	mu   sync.Mutex
	f    *os.File
	day  string
	seq  int
	size int64
}

// OpenRotating creates dir if needed and returns a writer into it.
func OpenRotating(dir string, keepDays int) (*RotatingFile, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &RotatingFile{Dir: dir, MaxSize: DefaultMaxSize, KeepDays: keepDays, Now: time.Now}, nil
}

func (w *RotatingFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	day := w.Now().Format(time.DateOnly)
	if w.f == nil || day != w.day || (w.size > 0 && w.size+int64(len(p)) > w.MaxSize) {
		if err := w.rotate(day); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate opens the file for day, continuing the last file of that day if
// it still has room.
func (w *RotatingFile) rotate(day string) error {
	if w.f != nil {
		w.f.Close()
		w.f = nil
	}
	if day != w.day {
		w.day, w.seq = day, w.lastSeq(day)
		w.cleanup()
	} else {
		w.seq++
	}
	for {
		path := filepath.Join(w.Dir, name(day, w.seq))
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
		if err != nil {
			return err
		}
		fi, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		if fi.Size() > 0 && fi.Size() >= w.MaxSize {
			f.Close()
			w.seq++
			continue
		}
		w.f, w.size = f, fi.Size()
		return nil
	}
}

func name(day string, seq int) string {
	if seq == 0 {
		return prefix + day + ".log"
	}
	return fmt.Sprintf("%s%s.%d.log", prefix, day, seq)
}

func (w *RotatingFile) lastSeq(day string) int {
	entries, _ := os.ReadDir(w.Dir)
	last := 0
	for _, e := range entries {
		m := fileName.FindStringSubmatch(e.Name())
		if m == nil || m[1] != day || m[2] == "" {
			continue
		}
		if n, err := strconv.Atoi(m[2]); err == nil && n > last {
			last = n
		}
	}
	return last
}

// cleanup deletes log files of days older than KeepDays. Only names this
// package writes are considered.
func (w *RotatingFile) cleanup() {
	if w.KeepDays <= 0 {
		return
	}
	cutoff := w.Now().AddDate(0, 0, -w.KeepDays).Format(time.DateOnly)
	entries, err := os.ReadDir(w.Dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		m := fileName.FindStringSubmatch(e.Name())
		if m != nil && !e.IsDir() && m[1] < cutoff {
			os.Remove(filepath.Join(w.Dir, e.Name()))
		}
	}
}

// Close closes the current file.
func (w *RotatingFile) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
