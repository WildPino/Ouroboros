package backend

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
)

// Tags that Ouroboros puts on snapshots (DESIGN.md §2.2).
const (
	TagOuroboros = "ouroboros"
	TagPinned    = "ouroboros:pinned"
	JobTagPrefix = "job:"
)

// JobTag returns the tag that attributes a snapshot to a job.
func JobTag(name string) string { return JobTagPrefix + name }

// Backend is the storage engine. All repository access goes through it so
// that the space planner does not depend on a specific engine.
type Backend interface {
	Version(ctx context.Context) (string, error)
	Init(ctx context.Context) error
	Backup(ctx context.Context, job Job) (BackupResult, error)
	// EstimateBackup simulates a backup of job without writing anything and
	// returns the uncompressed size of the data it would add: an upper
	// bound for the growth of the repository.
	EstimateBackup(ctx context.Context, job Job) (uint64, error)
	Snapshots(ctx context.Context, filter SnapshotFilter) ([]Snapshot, error)
	Forget(ctx context.Context, ids []string) error
	Prune(ctx context.Context, opts PruneOptions) (PruneResult, error)
	Check(ctx context.Context, opts CheckOptions) error
	Restore(ctx context.Context, opts RestoreOptions) (RestoreResult, error)
	Tag(ctx context.Context, id string, add, remove []string) error
	// List returns the entries directly inside dir in a snapshot, dir in
	// the snapshot's own form ("/" or "/C/Users/me"), matched with exact
	// letter case.
	List(ctx context.Context, id, dir string) ([]Node, error)
}

// Job describes what one backup run reads.
type Job struct {
	Name        string
	Paths       []string
	Exclude     []string
	ExcludeFile string
	VSS         bool

	// Progress, when set, receives restic's periodic progress reports
	// (about one per second) from the goroutine reading its output.
	Progress func(BackupProgress)
}

// BackupProgress is one progress report of a running backup. Totals grow
// while restic is still scanning the sources.
type BackupProgress struct {
	PercentDone float64
	FilesDone   uint64
	TotalFiles  uint64
	BytesDone   uint64
	TotalBytes  uint64
}

// BackupResult reports what a backup did. BytesAddedPacked is the actual
// growth of the repository on disk.
type BackupResult struct {
	SnapshotID       string
	Start, End       time.Time
	FilesNew         uint64
	FilesChanged     uint64
	FilesUnmodified  uint64
	BytesProcessed   uint64
	BytesAdded       uint64
	BytesAddedPacked uint64
	// Incomplete is set when some source files could not be read; the
	// snapshot was still created (restic exit code 3).
	Incomplete bool
	// Warnings holds the per-file errors reported during the backup,
	// truncated to a reasonable number.
	Warnings []string
}

// Snapshot is one entry of the repository history.
type Snapshot struct {
	ID       string
	ShortID  string
	Time     time.Time
	Hostname string
	Paths    []string
	Tags     []string
	// Files and Bytes are the size of the backed-up data as recorded by
	// restic 0.17 and later; zero for older snapshots.
	Files uint64
	Bytes uint64
	// AddedPacked is the new data the backup stored, after compression;
	// the snapshot holds at least that much space while no later snapshot
	// shares it.
	AddedPacked uint64
}

// Job returns the name of the job the snapshot belongs to, or "" if it
// carries no job tag.
func (s Snapshot) Job() string {
	for _, t := range s.Tags {
		if name, ok := strings.CutPrefix(t, JobTagPrefix); ok {
			return name
		}
	}
	return ""
}

// Pinned reports whether the snapshot is protected from automatic deletion.
func (s Snapshot) Pinned() bool { return slices.Contains(s.Tags, TagPinned) }

// SnapshotFilter selects snapshots. Empty fields do not filter.
type SnapshotFilter struct {
	Host string
	// Tags must all be present on a snapshot for it to match.
	Tags []string
}

// PruneOptions controls how much unused space prune may leave behind.
type PruneOptions struct {
	// MaxUnused is passed to restic --max-unused, e.g. "0" or "5%".
	MaxUnused string
}

// PruneResult reports a prune run.
type PruneResult struct {
	Duration time.Duration
}

// CheckOptions controls repository verification.
type CheckOptions struct {
	// ReadDataSubset, when set, also reads back that part of the data,
	// e.g. "5%".
	ReadDataSubset string
}

// RestoreOptions selects what to restore and where.
type RestoreOptions struct {
	SnapshotID string
	Target     string
	Include    []string

	// Progress, when set, receives restic's periodic progress reports from
	// the goroutine reading its output.
	Progress func(RestoreProgress)
}

// RestoreProgress is one progress report of a running restore.
type RestoreProgress struct {
	PercentDone   float64
	FilesRestored uint64
	TotalFiles    uint64
	BytesRestored uint64
	TotalBytes    uint64
}

// RestoreResult reports what a restore did. TotalFiles counts the files
// and folders the selection matched; FilesRestored excludes those already
// present and identical.
type RestoreResult struct {
	TotalFiles    uint64
	FilesRestored uint64
	TotalBytes    uint64
	BytesRestored uint64
}

// Node is one entry of a snapshot listing.
type Node struct {
	Name    string
	Type    string // "dir", "file" or "symlink"
	Path    string // in the snapshot's form, such as /C/Users/me/file.txt
	Size    uint64 // files only
	ModTime time.Time
}

// ErrPathNotFound is returned by List for a folder the snapshot lacks.
var ErrPathNotFound = errors.New("no such folder in the snapshot")
