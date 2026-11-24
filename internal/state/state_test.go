package state

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

var t0 = time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", StateFile)
	st, err := Load(path)
	if err != nil || st.Jobs == nil || len(st.Jobs) != 0 {
		t.Fatalf("missing file: %+v, %v", st, err)
	}
	st.Jobs["docs"] = JobState{LastAttempt: t0, LastSuccess: t0, LastError: ""}
	st.Jobs["photos"] = JobState{LastAttempt: t0.Add(time.Hour), LastSuccess: t0, LastError: "disk full"}
	st.LastPrune = t0
	for range 3 { // repeated saves replace the file
		if err := Save(path, st); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Jobs["docs"].LastSuccess.Equal(t0) || got.Jobs["photos"].LastError != "disk full" || !got.LastPrune.Equal(t0) {
		t.Fatalf("got %+v", got)
	}
	if !got.Jobs["photos"].Failed() || got.Jobs["docs"].Failed() {
		t.Fatal("Failed() wrong")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temporary files left: %v", entries)
	}
}

func TestStateCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), StateFile)
	os.WriteFile(path, []byte("{not json"), 0o644)
	st, err := Load(path)
	if err == nil {
		t.Fatal("want error")
	}
	if st.Jobs == nil {
		t.Fatal("state must stay usable")
	}
}

func TestJobStateFailed(t *testing.T) {
	tests := []struct {
		js   JobState
		want bool
	}{
		{JobState{}, false},
		{JobState{LastAttempt: t0, LastSuccess: t0}, false},
		{JobState{LastAttempt: t0.Add(time.Minute), LastSuccess: t0}, true},
		{JobState{LastAttempt: t0}, true}, // died during its first run
	}
	for _, tt := range tests {
		if got := tt.js.Failed(); got != tt.want {
			t.Errorf("%+v.Failed() = %v", tt.js, got)
		}
	}
}

func TestHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), HistoryFile)
	if recs, skipped, err := ReadHistory(path); err != nil || len(recs) != 0 || skipped != 0 {
		t.Fatalf("missing file: %v %d %v", recs, skipped, err)
	}
	add := func(r Record) {
		t.Helper()
		if err := Append(path, r); err != nil {
			t.Fatal(err)
		}
	}
	add(Record{Time: t0, Kind: KindBackup, Job: "docs", OK: true, BytesAddedPacked: 100})
	add(Record{Time: t0, Kind: KindBackup, Job: "photos", OK: true, BytesAddedPacked: 999})
	add(Record{Time: t0, Kind: KindBackup, Job: "docs", OK: false, Error: "boom", BytesAddedPacked: 5000})
	add(Record{Time: t0, Kind: KindReclaim, Forgotten: 3, Freed: 1 << 20})
	add(Record{Time: t0, Kind: KindBackup, Job: "docs", OK: true, BytesAddedPacked: 300})

	// A crash in the middle of an append leaves a truncated last line.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"time":"2026-05-01T10:00:00Z","kind":"backup","jo`)
	f.Close()

	recs, skipped, err := ReadHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 5 || skipped != 1 {
		t.Fatalf("records %d skipped %d", len(recs), skipped)
	}
	if got := BackupGrowth(recs, "docs"); !slices.Equal(got, []uint64{100, 300}) {
		t.Fatalf("growth %v", got)
	}

	// Appending after a truncated line still produces readable records.
	add(Record{Time: t0, Kind: KindPrune, OK: true})
	recs, skipped, err = ReadHistory(path)
	if err != nil || len(recs) != 6 || skipped != 1 || recs[5].Kind != KindPrune {
		t.Fatalf("after append: %d records, %d skipped, %v", len(recs), skipped, err)
	}
}
