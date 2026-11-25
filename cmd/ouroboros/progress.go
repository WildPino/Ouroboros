package main

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/WildPino/ouroboros/internal/scheduler"
	"github.com/WildPino/ouroboros/internal/state"
)

// progressInterval bounds how often progress.json is rewritten and progress
// events are written while a phase goes on; a new phase is written at once.
const progressInterval = 2 * time.Second

// progressReporter publishes what a run is doing in progress.json and, with
// --json, as "progress" events (DESIGN.md §12.8, question 8). Failures to
// write the file are reported once and otherwise ignored: progress is
// informational and must never stop a backup.
type progressReporter struct {
	path   string
	out    *jsonLines // nil without --json
	stderr io.Writer
	now    func() time.Time

	mu        sync.Mutex
	cur       state.Progress
	lastWrite time.Time
	warned    bool
}

func newProgressReporter(path string, out *jsonLines, stderr io.Writer, now func() time.Time) *progressReporter {
	return &progressReporter{path: path, out: out, stderr: stderr, now: now, cur: state.Progress{Started: now().UTC()}}
}

// report is scheduler.Runner.Progress; restic's output reader calls it
// concurrently with the run itself.
func (p *progressReporter) report(sp scheduler.Progress) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now().UTC()
	newPhase := sp.Phase != p.cur.Phase || sp.Job != p.cur.Job
	if newPhase {
		p.cur.Phase, p.cur.Job, p.cur.PhaseStarted, p.cur.Backup = sp.Phase, sp.Job, now, nil
	}
	if b := sp.Backup; b != nil {
		p.cur.Backup = &state.BackupProgress{PercentDone: b.PercentDone, FilesDone: b.FilesDone,
			TotalFiles: b.TotalFiles, BytesDone: b.BytesDone, TotalBytes: b.TotalBytes}
	}
	if !newPhase && now.Sub(p.lastWrite) < progressInterval {
		return
	}
	p.cur.Updated, p.lastWrite = now, now
	// The window may be reading the file, which makes the rename fail for a
	// moment; the next report writes it again.
	if err := state.SaveProgress(p.path, p.cur); err != nil && !p.warned {
		p.warned = true
		fmt.Fprintf(p.stderr, "warning: cannot write %s: %v\n", state.ProgressFile, err)
	}
	if p.out != nil {
		p.out.event("progress", p.cur)
	}
}

// finish removes progress.json at the end of the run.
func (p *progressReporter) finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := state.RemoveProgress(p.path); err != nil {
		fmt.Fprintf(p.stderr, "warning: cannot remove %s: %v\n", state.ProgressFile, err)
	}
}
