package state

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// File names inside the configuration directory (DESIGN.md §5).
const (
	StateFile    = "state.json"
	HistoryFile  = "history.jsonl"
	ProgressFile = "progress.json"
)

// JobState is the scheduling state of one job.
type JobState struct {
	LastAttempt time.Time `json:"last_attempt,omitzero"`
	LastSuccess time.Time `json:"last_success,omitzero"`
	LastError   string    `json:"last_error,omitempty"`
}

// Failed reports whether the most recent attempt did not succeed. An
// attempt without a recorded outcome (the process died) counts as failed.
func (j JobState) Failed() bool { return j.LastAttempt.After(j.LastSuccess) }

// State is the content of state.json.
type State struct {
	Jobs              map[string]JobState `json:"jobs"`
	LastPrune         time.Time           `json:"last_prune,omitzero"`
	LastCheck         time.Time           `json:"last_check,omitzero"`
	LastReadDataCheck time.Time           `json:"last_read_data_check,omitzero"`
}

// Load reads state.json; a missing file yields an empty state.
func Load(path string) (State, error) {
	st := State{Jobs: map[string]JobState{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return State{Jobs: map[string]JobState{}}, fmt.Errorf("parse %s: %w", path, err)
	}
	if st.Jobs == nil {
		st.Jobs = map[string]JobState{}
	}
	return st, nil
}

// Save writes state.json atomically: a temporary file in the same directory
// is written, flushed and renamed over the old one, so a crash leaves either
// the old or the new state, never a truncated file.
func Save(path string, st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, data, true)
}

// writeAtomic replaces path with data through a temporary file and a
// rename; sync also flushes the data to disk before the rename.
func writeAtomic(path string, data []byte, sync bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if sync {
		if err := tmp.Sync(); err != nil {
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// Record kinds in history.jsonl.
const (
	KindBackup    = "backup"
	KindReclaim   = "reclaim"
	KindRetention = "retention"
	KindPrune     = "prune"
	KindCheck     = "check"
)

// Record is one line of history.jsonl.
type Record struct {
	Time     time.Time `json:"time"`
	Kind     string    `json:"kind"`
	Job      string    `json:"job,omitempty"`
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	Duration float64   `json:"duration_s,omitempty"`

	// Backups.
	SnapshotID       string `json:"snapshot_id,omitempty"`
	PredictedGrowth  uint64 `json:"predicted_growth,omitempty"`
	BytesProcessed   uint64 `json:"bytes_processed,omitempty"`
	BytesAdded       uint64 `json:"bytes_added,omitempty"`
	BytesAddedPacked uint64 `json:"bytes_added_packed,omitempty"`
	FilesNew         uint64 `json:"files_new,omitempty"`
	FilesChanged     uint64 `json:"files_changed,omitempty"`
	Incomplete       bool   `json:"incomplete,omitempty"`
	Warnings         int    `json:"warnings,omitempty"`

	// Reclaim and retention.
	Forgotten int    `json:"forgotten,omitempty"`
	Freed     uint64 `json:"freed,omitempty"`

	// Checks.
	ReadData string `json:"read_data,omitempty"`
}

// Append adds a record to history.jsonl and flushes it to disk.
func Append(path string, r Record) error {
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	// After a crash the last line may be cut short; start on a new line so
	// that this record is not glued to the broken one.
	if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, fi.Size()-1); err == nil && last[0] != '\n' {
			line = append([]byte{'\n'}, line...)
		}
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ReadHistory returns every readable record of history.jsonl, oldest first,
// and the number of lines skipped because they were corrupted (typically a
// line cut short by a crash). A missing file is an empty history.
func ReadHistory(path string) ([]Record, int, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	var out []Record
	skipped := 0
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil || r.Kind == "" {
			skipped++
			continue
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return out, skipped, err
	}
	return out, skipped, nil
}

// BackupGrowth returns the packed bytes added by the successful backups of
// job, oldest first: the input of the growth predictor.
func BackupGrowth(records []Record, job string) []uint64 {
	var out []uint64
	for _, r := range records {
		if r.Kind == KindBackup && r.Job == job && r.OK {
			out = append(out, r.BytesAddedPacked)
		}
	}
	return out
}
