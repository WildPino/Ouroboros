package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// Progress is the content of progress.json, written by a run while it works
// so that other processes can show what it is doing (DESIGN.md §12.8). It is
// informational: a run that dies leaves a stale file behind, so readers
// trust it only while the run lock is held.
type Progress struct {
	Started      time.Time       `json:"started"`
	Updated      time.Time       `json:"updated"`
	Phase        string          `json:"phase"`
	PhaseStarted time.Time       `json:"phase_started"`
	Job          string          `json:"job,omitempty"`
	Backup       *BackupProgress `json:"backup,omitempty"`
}

// BackupProgress is restic's last progress report for the running backup.
type BackupProgress struct {
	PercentDone float64 `json:"percent_done"`
	FilesDone   uint64  `json:"files_done"`
	TotalFiles  uint64  `json:"total_files"`
	BytesDone   uint64  `json:"bytes_done"`
	TotalBytes  uint64  `json:"total_bytes"`
}

// SaveProgress replaces progress.json. It does not flush to disk: losing the
// file in a crash costs nothing.
func SaveProgress(path string, p Progress) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return writeAtomic(path, data, false)
}

// LoadProgress reads progress.json; ok is false when there is none.
func LoadProgress(path string) (p Progress, ok bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Progress{}, false, nil
	}
	if err != nil {
		return Progress{}, false, err
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return Progress{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return p, true, nil
}

// RemoveProgress deletes progress.json; a missing file is not an error.
func RemoveProgress(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
