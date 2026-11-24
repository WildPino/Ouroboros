package restic

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func parseBackupFixture(t *testing.T, name string) (backend.BackupResult, bool) {
	t.Helper()
	var res backend.BackupResult
	summary := false
	for line := range bytes.Lines(fixture(t, name)) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		ok, err := parseBackupLine(line, &res)
		if err != nil {
			t.Fatal(err)
		}
		summary = summary || ok
	}
	return res, summary
}

func TestParseBackupFirst(t *testing.T) {
	res, ok := parseBackupFixture(t, "backup-first.jsonl")
	if !ok {
		t.Fatal("no summary")
	}
	if len(res.SnapshotID) != 64 {
		t.Errorf("snapshot id %q", res.SnapshotID)
	}
	if res.FilesNew != 3 || res.FilesChanged != 0 || res.FilesUnmodified != 0 {
		t.Errorf("files new/changed/unmodified = %d/%d/%d", res.FilesNew, res.FilesChanged, res.FilesUnmodified)
	}
	if res.BytesProcessed == 0 || res.BytesAdded == 0 || res.BytesAddedPacked == 0 {
		t.Errorf("bytes processed/added/packed = %d/%d/%d", res.BytesProcessed, res.BytesAdded, res.BytesAddedPacked)
	}
	if res.Start.IsZero() || !res.End.After(res.Start) {
		t.Errorf("start %v end %v", res.Start, res.End)
	}
}

func TestParseBackupPartial(t *testing.T) {
	res, ok := parseBackupFixture(t, "backup-partial.jsonl")
	if !ok || res.SnapshotID == "" {
		t.Fatal("partial backup must still report its snapshot")
	}
	if res.FilesChanged != 1 {
		t.Errorf("files changed = %d, want 1", res.FilesChanged)
	}
	info := parseStderr(fixture(t, "backup-partial.stderr.jsonl"))
	if len(info.warnings) != 1 || !strings.Contains(info.warnings[0], `locked.txt`) {
		t.Errorf("warnings = %q", info.warnings)
	}
	if info.message != "Warning: at least one source file could not be read" {
		t.Errorf("message = %q", info.message)
	}
}

func TestParseBackupDryRun(t *testing.T) {
	res, ok := parseBackupFixture(t, "backup-dryrun.jsonl")
	if !ok || res.BytesAdded == 0 {
		t.Fatalf("dry run: summary=%v bytes added=%d", ok, res.BytesAdded)
	}
}

func TestParseBackupLineIgnoresProgress(t *testing.T) {
	var res backend.BackupResult
	ok, err := parseBackupLine([]byte(`{"message_type":"status","percent_done":0.5}`), &res)
	if ok || err != nil {
		t.Fatalf("status line: ok=%v err=%v", ok, err)
	}
	if _, err := parseBackupLine([]byte("not json"), &res); err == nil {
		t.Fatal("garbage line: want error")
	}
}

func TestParseSnapshots(t *testing.T) {
	snaps, err := parseSnapshots(fixture(t, "snapshots.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 {
		t.Fatalf("got %d snapshots, want 2", len(snaps))
	}
	if !snaps[0].Time.Before(snaps[1].Time) {
		t.Error("snapshots not sorted oldest first")
	}
	for _, s := range snaps {
		if s.Job() != "docs" || s.Hostname != "fixturehost" || s.Pinned() || len(s.ID) != 64 || len(s.ShortID) != 8 {
			t.Errorf("snapshot %+v", s)
		}
		if len(s.Paths) != 1 || s.Paths[0] != `C:\data\src` {
			t.Errorf("paths %q", s.Paths)
		}
	}

	empty, err := parseSnapshots(fixture(t, "snapshots-empty.json"))
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty: %v, %v", empty, err)
	}
	if _, err := parseSnapshots([]byte(`[{"time":"2026-01-01T00:00:00Z"}]`)); err == nil {
		t.Fatal("entry without id: want error")
	}
	if _, err := parseSnapshots([]byte(`{`)); err == nil {
		t.Fatal("bad json: want error")
	}
}

func TestParseSnapshotsTieBreak(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	data := `[{"id":"bbbb0000","time":"` + ts + `"},{"id":"aaaa0000","time":"` + ts + `"}]`
	snaps, err := parseSnapshots([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if snaps[0].ID != "aaaa0000" {
		t.Fatalf("tie not broken by id: %v", snaps)
	}
}

func TestParseStderr(t *testing.T) {
	tests := []struct {
		name, fixture, want string
	}{
		{"wrong password", "wrong-password.stderr.jsonl", "Fatal: wrong password or no key found"},
		{"no repository", "no-repo.stderr.jsonl", "Fatal: repository does not exist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := parseStderr(fixture(t, tt.fixture))
			if !strings.HasPrefix(info.message, tt.want) {
				t.Fatalf("message = %q, want prefix %q", info.message, tt.want)
			}
		})
	}

	plain := parseStderr([]byte("a\nb\nc\nd\ne\nf\nFatal: boom\n"))
	if plain.message != "c\nd\ne\nf\nFatal: boom" {
		t.Errorf("plain message = %q", plain.message)
	}

	var many bytes.Buffer
	for range maxWarnings + 7 {
		many.WriteString(`{"message_type":"error","error":{"message":"x"},"during":"archival","item":"x"}` + "\n")
	}
	info := parseStderr(many.Bytes())
	if len(info.warnings) != maxWarnings || info.dropped != 7 {
		t.Errorf("warnings %d dropped %d", len(info.warnings), info.dropped)
	}
}

func TestCheckIDs(t *testing.T) {
	for _, ok := range []string{"0123abcd", strings.Repeat("a", 64)} {
		if err := checkIDs(ok); err != nil {
			t.Errorf("checkIDs(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "abc", "--host", "latest", "0123ABCD", strings.Repeat("a", 65), "0123abcd,x"} {
		if err := checkIDs(bad); err == nil {
			t.Errorf("checkIDs(%q): want error", bad)
		}
	}
}

func TestPasswordCommand(t *testing.T) {
	got := PasswordCommand(`C:\Program Files\Ouroboros\ouroboros.exe`, "--config", `C:\ProgramData\Ouroboros\ouroboros.toml`)
	want := `"C:\Program Files\Ouroboros\ouroboros.exe" "internal" "password" "--config" "C:\ProgramData\Ouroboros\ouroboros.toml"`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestNewValidatesOptions(t *testing.T) {
	base := Options{Executable: "restic.exe", Repository: `B:\repo`, PasswordCommand: "x", Hostname: "h"}
	if _, err := New(base); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Options){
		func(o *Options) { o.Executable = "" },
		func(o *Options) { o.Repository = "" },
		func(o *Options) { o.PasswordCommand = "" },
	} {
		o := base
		mutate(&o)
		if _, err := New(o); err == nil {
			t.Errorf("New(%+v): want error", o)
		}
	}
}
