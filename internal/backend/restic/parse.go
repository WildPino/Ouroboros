package restic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
)

// maxWarnings bounds the per-file errors kept from one backup.
const maxWarnings = 100

type messageType struct {
	MessageType string `json:"message_type"`
}

// backupSummary is the final "summary" line of "restic backup --json".
type backupSummary struct {
	FilesNew            uint64    `json:"files_new"`
	FilesChanged        uint64    `json:"files_changed"`
	FilesUnmodified     uint64    `json:"files_unmodified"`
	DataAdded           uint64    `json:"data_added"`
	DataAddedPacked     uint64    `json:"data_added_packed"`
	TotalBytesProcessed uint64    `json:"total_bytes_processed"`
	BackupStart         time.Time `json:"backup_start"`
	BackupEnd           time.Time `json:"backup_end"`
	SnapshotID          string    `json:"snapshot_id"`
}

// backupStatus is a periodic "status" line of "restic backup --json".
type backupStatus struct {
	PercentDone float64 `json:"percent_done"`
	TotalFiles  uint64  `json:"total_files"`
	FilesDone   uint64  `json:"files_done"`
	TotalBytes  uint64  `json:"total_bytes"`
	BytesDone   uint64  `json:"bytes_done"`
}

// parseBackupLine applies one stdout line of "restic backup --json" to res
// and reports whether it was the summary. Status lines go to progress, if
// set; other lines are ignored.
func parseBackupLine(line []byte, res *backend.BackupResult, progress func(backend.BackupProgress)) (bool, error) {
	var mt messageType
	if err := json.Unmarshal(line, &mt); err != nil {
		return false, fmt.Errorf("unexpected backup output %q: %w", truncate(line), err)
	}
	if mt.MessageType == "status" && progress != nil {
		var st backupStatus
		if err := json.Unmarshal(line, &st); err != nil {
			return false, fmt.Errorf("parse backup status: %w", err)
		}
		progress(backend.BackupProgress{PercentDone: st.PercentDone, FilesDone: st.FilesDone,
			TotalFiles: st.TotalFiles, BytesDone: st.BytesDone, TotalBytes: st.TotalBytes})
		return false, nil
	}
	if mt.MessageType != "summary" {
		return false, nil
	}
	var s backupSummary
	if err := json.Unmarshal(line, &s); err != nil {
		return false, fmt.Errorf("parse backup summary: %w", err)
	}
	res.SnapshotID = s.SnapshotID
	res.FilesNew = s.FilesNew
	res.FilesChanged = s.FilesChanged
	res.FilesUnmodified = s.FilesUnmodified
	res.BytesProcessed = s.TotalBytesProcessed
	res.BytesAdded = s.DataAdded
	res.BytesAddedPacked = s.DataAddedPacked
	if !s.BackupStart.IsZero() {
		res.Start = s.BackupStart
	}
	if !s.BackupEnd.IsZero() {
		res.End = s.BackupEnd
	}
	return true, nil
}

type snapshotJSON struct {
	ID       string    `json:"id"`
	ShortID  string    `json:"short_id"`
	Time     time.Time `json:"time"`
	Hostname string    `json:"hostname"`
	Paths    []string  `json:"paths"`
	Tags     []string  `json:"tags"`
	Summary  *struct {
		Files uint64 `json:"total_files_processed"`
		Bytes uint64 `json:"total_bytes_processed"`
	} `json:"summary"`
}

// parseSnapshots decodes "restic snapshots --json" and returns the snapshots
// oldest first (ties broken by ID, as in DESIGN.md §3.5).
func parseSnapshots(data []byte) ([]backend.Snapshot, error) {
	var raw []snapshotJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse snapshots: %w", err)
	}
	out := make([]backend.Snapshot, 0, len(raw))
	for _, s := range raw {
		if s.ID == "" {
			return nil, fmt.Errorf("parse snapshots: entry without id")
		}
		sn := backend.Snapshot{
			ID: s.ID, ShortID: s.ShortID, Time: s.Time,
			Hostname: s.Hostname, Paths: s.Paths, Tags: s.Tags,
		}
		if s.Summary != nil {
			sn.Files, sn.Bytes = s.Summary.Files, s.Summary.Bytes
		}
		out = append(out, sn)
	}
	slices.SortStableFunc(out, func(a, b backend.Snapshot) int {
		if c := a.Time.Compare(b.Time); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

type stderrJSON struct {
	MessageType string `json:"message_type"`
	Message     string `json:"message"`
	Error       struct {
		Message string `json:"message"`
	} `json:"error"`
	Item string `json:"item"`
}

type stderrInfo struct {
	message  string   // final error message (exit_error), if any
	warnings []string // per-item errors
	dropped  int      // warnings beyond maxWarnings
}

// parseStderr extracts restic's messages from stderr. With --json, restic
// prints "error" and "exit_error" objects; anything else (a password command
// failing, an older restic) is kept as plain text.
func parseStderr(b []byte) stderrInfo {
	var info stderrInfo
	var plain []string
	for line := range bytes.Lines(b) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var m stderrJSON
		if line[0] != '{' || json.Unmarshal(line, &m) != nil || m.MessageType == "" {
			plain = append(plain, string(line))
			continue
		}
		switch m.MessageType {
		case "exit_error":
			info.message = m.Message
		case "error":
			if len(info.warnings) < maxWarnings {
				info.warnings = append(info.warnings, m.Error.Message)
			} else {
				info.dropped++
			}
		}
	}
	if info.message == "" && len(plain) > 0 {
		// The last lines carry the reason; earlier ones are usually context.
		info.message = strings.Join(plain[max(0, len(plain)-5):], "\n")
	}
	return info
}

func truncate(b []byte) string {
	const n = 200
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}

// restoreTotalFiles returns the number of files that "restic restore --json"
// selected, from its summary line; 0 when the summary has none.
func parseRestoreLine(line []byte, res *backend.RestoreResult, progress func(backend.RestoreProgress)) error {
	var s struct {
		MessageType   string  `json:"message_type"`
		PercentDone   float64 `json:"percent_done"`
		TotalFiles    uint64  `json:"total_files"`
		FilesRestored uint64  `json:"files_restored"`
		TotalBytes    uint64  `json:"total_bytes"`
		BytesRestored uint64  `json:"bytes_restored"`
	}
	if err := json.Unmarshal(line, &s); err != nil {
		return fmt.Errorf("unexpected restore output %q: %w", truncate(line), err)
	}
	switch s.MessageType {
	case "status":
		if progress != nil {
			progress(backend.RestoreProgress{PercentDone: s.PercentDone, FilesRestored: s.FilesRestored,
				TotalFiles: s.TotalFiles, BytesRestored: s.BytesRestored, TotalBytes: s.TotalBytes})
		}
	case "summary":
		*res = backend.RestoreResult{TotalFiles: s.TotalFiles, FilesRestored: s.FilesRestored,
			TotalBytes: s.TotalBytes, BytesRestored: s.BytesRestored}
	}
	return nil
}
