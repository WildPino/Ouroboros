package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
	"github.com/WildPino/ouroboros/internal/space"
)

func TestReclaimRequiresDryRun(t *testing.T) {
	p := writeConfig(t, testConfig)
	code, _, errOut := runCLI("reclaim", "--config", p)
	if code != exitError || !strings.Contains(errOut, "only supports --dry-run") {
		t.Fatalf("code %d stderr %q", code, errOut)
	}
}

func planSnap(id, job string, at time.Time) backend.Snapshot {
	return backend.Snapshot{ID: id, Time: at, Tags: []string{backend.TagOuroboros, backend.JobTag(job)}}
}

func TestWritePlan(t *testing.T) {
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	targets := space.Targets{MinFree: 10 << 30, PruneHeadroom: 1 << 30}
	plan := space.PlanReport{
		Usage:    space.Usage{Total: 100 << 30, Free: 4 << 30},
		NeedFree: 11 << 30,
		Missing:  7 << 30,
		Selection: space.Selection{
			Candidates: []backend.Snapshot{
				planSnap("0123456789abcdef", "docs", at),
				planSnap("fedcba9876543210", "photos", at.Add(time.Hour)),
			},
			Protected: []space.Protected{
				{Snapshot: planSnap("aaaa", "docs", at), Reason: space.ReasonNewest},
				{Snapshot: planSnap("bbbb", "docs", at), Reason: space.ReasonPinned},
			},
		},
	}
	var out bytes.Buffer
	writePlan(&out, plan, targets)
	got := out.String()
	for _, want := range []string{
		"4.0 GB free", "Target:     11.0 GB free", "Missing:    7.0 GB",
		"1. ", "docs", "01234567", "2. ", "photos", "fedcba98",
		"Protected:  2 snapshots (1 newest of their job, 0 recent, 1 pinned)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "01234567") > strings.Index(got, "fedcba98") {
		t.Error("candidates not listed oldest first")
	}

	plan.Missing, plan.Selection.Candidates = 0, nil
	out.Reset()
	writePlan(&out, plan, targets)
	if !strings.Contains(out.String(), "Nothing to reclaim") {
		t.Errorf("no-op plan:\n%s", out.String())
	}

	plan.Missing = 1 << 30
	out.Reset()
	writePlan(&out, plan, targets)
	if !strings.Contains(out.String(), "all of them are protected") {
		t.Errorf("all protected:\n%s", out.String())
	}
}

func TestReclaimPlanCommand(t *testing.T) {
	c := newRepoCase(t)
	if err := os.MkdirAll(filepath.Join(c.dir, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runReclaimPlan(context.Background(), c.opts, c.deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.out.String(), "free") || c.locked != 0 {
		t.Errorf("locked %d, output:\n%s", c.locked, c.out)
	}

	c.deps.isElevated = func() (bool, error) { return false, nil }
	err := runReclaimPlan(context.Background(), c.opts, c.deps)
	if err == nil || !strings.Contains(err.Error(), "Administrator") {
		t.Fatalf("not elevated: %v", err)
	}
}
