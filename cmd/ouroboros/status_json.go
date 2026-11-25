package main

import (
	"time"

	"github.com/WildPino/ouroboros/internal/platform/windows"
	"github.com/WildPino/ouroboros/internal/state"
)

// Schema of "status --json"; documented in docs/JSON.md. Pointers and
// omitted fields mean the value is unknown, never zero.
type statusOut struct {
	Repository  repositoryOut   `json:"repository"`
	Partition   *partitionOut   `json:"partition"`
	Snapshots   *snapshotsOut   `json:"snapshots"`
	Task        taskOut         `json:"task"`
	Running     *bool           `json:"running"`
	Progress    *state.Progress `json:"progress"`
	Maintenance maintenanceOut  `json:"maintenance"`
	Jobs        []jobOut        `json:"jobs"`
	Recent      []recordOut     `json:"recent_problems"`
	Problems    []string        `json:"problems"`
}

type repositoryOut struct {
	Path        string  `json:"path"`
	SizeBytes   *uint64 `json:"size_bytes"`
	MaxRepoSize *uint64 `json:"max_repo_size_bytes"`
}

type partitionOut struct {
	TotalBytes         uint64 `json:"total_bytes"`
	FreeBytes          uint64 `json:"free_bytes"`
	MinFreeBytes       uint64 `json:"min_free_bytes"`
	PruneHeadroomBytes uint64 `json:"prune_headroom_bytes"`
	RequiredFreeBytes  uint64 `json:"required_free_bytes"`
}

type snapshotsOut struct {
	Count  int        `json:"count"`
	Oldest *time.Time `json:"oldest"`
}

type taskOut struct {
	// Installed is null when the scheduled task could not be queried.
	Installed *bool `json:"installed"`
	// Paused is true when the task is installed but disabled with "pause";
	// null when the task could not be queried.
	Paused *bool  `json:"paused"`
	Tick   string `json:"tick"`
}

type maintenanceOut struct {
	LastPrune         *time.Time `json:"last_prune"`
	LastCheck         *time.Time `json:"last_check"`
	LastReadDataCheck *time.Time `json:"last_read_data_check"`
}

type jobOut struct {
	Name           string     `json:"name"`
	LastSuccess    *time.Time `json:"last_success"`
	LastAttempt    *time.Time `json:"last_attempt"`
	Failed         bool       `json:"failed"`
	LastError      string     `json:"last_error,omitempty"`
	NextRun        *time.Time `json:"next_run"`
	Due            bool       `json:"due"`
	Snapshots      *int       `json:"snapshots"`
	PredictedBytes *uint64    `json:"predicted_growth_bytes"`
}

type recordOut struct {
	Time     time.Time `json:"time"`
	Kind     string    `json:"kind"`
	Job      string    `json:"job,omitempty"`
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	Warnings int       `json:"warnings,omitempty"`
}

func statusJSON(r statusReport) statusOut {
	out := statusOut{
		Repository: repositoryOut{Path: r.repoPath},
		Task:       taskOut{Tick: shortDuration(r.interval)},
		Maintenance: maintenanceOut{
			LastPrune:         timeOrNil(r.st.LastPrune),
			LastCheck:         timeOrNil(r.st.LastCheck),
			LastReadDataCheck: timeOrNil(r.st.LastReadDataCheck),
		},
		Jobs:     []jobOut{},
		Recent:   []recordOut{},
		Problems: append([]string{}, r.problems...),
	}
	if r.sizeKnown {
		out.Repository.SizeBytes = ptr(r.repoSize)
	}
	if r.targets.MaxRepoSize > 0 {
		out.Repository.MaxRepoSize = ptr(r.targets.MaxRepoSize)
	}
	if r.diskKnown {
		out.Partition = &partitionOut{
			TotalBytes: r.total, FreeBytes: r.free,
			MinFreeBytes: r.targets.MinFree, PruneHeadroomBytes: r.targets.PruneHeadroom,
			RequiredFreeBytes: r.targets.RequiredFree(0),
		}
	}
	if r.snapsKnown {
		out.Snapshots = &snapshotsOut{Count: len(r.snapshots)}
		if len(r.snapshots) > 0 {
			out.Snapshots.Oldest = timeOrNil(r.snapshots[0].Time)
		}
	}
	if r.taskKnown {
		out.Task.Installed = ptr(r.task != windows.TaskMissing)
		out.Task.Paused = ptr(r.task == windows.TaskDisabled)
	}
	if r.runKnown {
		out.Running = ptr(r.running)
	}
	out.Progress = r.progress
	for _, j := range r.jobs {
		jo := jobOut{
			Name:        j.name,
			LastSuccess: timeOrNil(j.state.LastSuccess),
			LastAttempt: timeOrNil(j.state.LastAttempt),
			Failed:      j.state.Failed(),
			NextRun:     timeOrNil(j.next),
			Due:         j.due,
		}
		if jo.Failed {
			jo.LastError = j.state.LastError
		}
		if j.knowsSnaps {
			jo.Snapshots = ptr(j.snapshots)
		}
		if j.hasHistory {
			jo.PredictedBytes = ptr(j.predicted)
		}
		out.Jobs = append(out.Jobs, jo)
	}
	for _, rec := range r.recent {
		out.Recent = append(out.Recent, recordJSON(rec))
	}
	return out
}

func recordJSON(r state.Record) recordOut {
	return recordOut{Time: r.Time.UTC(), Kind: r.Kind, Job: r.Job, OK: r.OK, Error: r.Error, Warnings: r.Warnings}
}

func ptr[T any](v T) *T { return &v }
