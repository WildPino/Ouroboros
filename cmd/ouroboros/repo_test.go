package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/lock"
	"github.com/WildPino/ouroboros/internal/state"
)

// fakeRepo is a backend.Backend that records the calls that change or
// read data.
type fakeRepo struct {
	snaps      []backend.Snapshot
	calls      []string
	checkErr   error
	restored   backend.RestoreOptions
	restoreErr error
}

func (f *fakeRepo) Version(context.Context) (string, error) { return "0.20.0", nil }
func (f *fakeRepo) Init(context.Context) error              { return errors.New("unexpected init") }
func (f *fakeRepo) Backup(context.Context, backend.Job) (backend.BackupResult, error) {
	return backend.BackupResult{}, errors.New("unexpected backup")
}
func (f *fakeRepo) EstimateBackup(context.Context, backend.Job) (uint64, error) { return 0, nil }
func (f *fakeRepo) Snapshots(context.Context, backend.SnapshotFilter) ([]backend.Snapshot, error) {
	return f.snaps, nil
}
func (f *fakeRepo) Forget(context.Context, []string) error { return errors.New("unexpected forget") }
func (f *fakeRepo) Prune(context.Context, backend.PruneOptions) (backend.PruneResult, error) {
	return backend.PruneResult{}, errors.New("unexpected prune")
}
func (f *fakeRepo) Check(_ context.Context, o backend.CheckOptions) error {
	f.calls = append(f.calls, "check "+o.ReadDataSubset)
	return f.checkErr
}
func (f *fakeRepo) Restore(_ context.Context, o backend.RestoreOptions) (backend.RestoreResult, error) {
	f.calls = append(f.calls, "restore "+o.SnapshotID)
	f.restored = o
	if o.Progress != nil {
		o.Progress(backend.RestoreProgress{PercentDone: 0.5, FilesRestored: 1, TotalFiles: 2})
	}
	return backend.RestoreResult{TotalFiles: 2, FilesRestored: 2, TotalBytes: 20, BytesRestored: 20}, f.restoreErr
}
func (f *fakeRepo) List(_ context.Context, id, dir string) ([]backend.Node, error) {
	f.calls = append(f.calls, "ls "+id+" "+dir)
	if dir == "/missing" || dir == "/C/missing" {
		return nil, backend.ErrPathNotFound
	}
	t := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	return []backend.Node{
		{Name: "a.txt", Type: "file", Path: path.Join(dir, "a.txt"), Size: 5, ModTime: t},
		{Name: "sub", Type: "dir", Path: path.Join(dir, "sub"), ModTime: t},
	}, nil
}
func (f *fakeRepo) Tag(_ context.Context, id string, add, remove []string) error {
	f.calls = append(f.calls, "tag "+id+" +"+strings.Join(add, ",")+" -"+strings.Join(remove, ","))
	return nil
}

type repoCase struct {
	opts    *options
	out     *bytes.Buffer
	errOut  *bytes.Buffer
	repo    *fakeRepo
	deps    repoDeps
	dir     string
	lockErr error
	locked  int
}

func newRepoCase(t *testing.T) *repoCase {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ouroboros.toml")
	cfg := "[repository]\npath = '" + filepath.Join(dir, "repo") + "'\n[[job]]\nname = \"docs\"\npaths = ['C:\\docs']\ninterval = \"6h\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	c := &repoCase{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}, dir: dir, repo: &fakeRepo{snaps: []backend.Snapshot{
		{ID: "aaaa1111bbbb", Time: at, Paths: []string{`C:\docs`}, Tags: []string{backend.TagOuroboros, backend.JobTag("docs")}},
		{ID: "aaaa2222cccc", Time: at.Add(time.Hour), Paths: []string{`C:\photos`}, Tags: []string{backend.TagOuroboros, backend.JobTag("photos"), backend.TagPinned}},
	}}}
	c.opts = &options{configPath: cfgPath, stdout: c.out, stderr: c.errOut}
	c.deps = repoDeps{
		isElevated: func() (bool, error) { return true, nil },
		acquire: func() (*lock.Lock, error) {
			c.locked++
			return nil, c.lockErr
		},
		checkRestic: func(context.Context, *config.Config) error { return nil },
		newBackend:  func(*config.Config, string) (backend.Backend, error) { return c.repo, nil },
		hostname:    func() (string, error) { return "host", nil },
	}
	return c
}

func TestResolveSnapshot(t *testing.T) {
	snaps := newRepoCase(t).repo.snaps
	if s, err := resolveSnapshot(snaps, "AAAA1"); err != nil || s.ID != "aaaa1111bbbb" {
		t.Fatalf("got %v %v", s.ID, err)
	}
	for ref, want := range map[string]string{
		"aaaa":   "ambiguous",
		"ffff":   "no snapshot",
		"abc":    "not a snapshot ID",
		"--help": "not a snapshot ID",
	} {
		if _, err := resolveSnapshot(snaps, ref); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", ref, err, want)
		}
	}
}

func TestSnapshotsCommand(t *testing.T) {
	c := newRepoCase(t)
	if err := runSnapshots(context.Background(), c.opts, c.deps, ""); err != nil {
		t.Fatal(err)
	}
	out := c.out.String()
	for _, want := range []string{"ID", "aaaa1111", "docs", "aaaa2222", "photos", "pinned", "2 snapshots"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if c.locked != 0 {
		t.Error("listing must not take the lock")
	}
	c.out.Reset()
	runSnapshots(context.Background(), c.opts, c.deps, "DOCS")
	if strings.Contains(c.out.String(), "photos") || !strings.Contains(c.out.String(), "1 snapshots") {
		t.Errorf("job filter:\n%s", c.out)
	}
	c.out.Reset()
	runSnapshots(context.Background(), c.opts, c.deps, "none")
	if c.out.String() != "No snapshots.\n" {
		t.Errorf("got %q", c.out)
	}
}

func TestPinUnpin(t *testing.T) {
	c := newRepoCase(t)
	ctx := context.Background()
	if err := runPin(ctx, c.opts, c.deps, "aaaa1", true); err != nil {
		t.Fatal(err)
	}
	if err := runPin(ctx, c.opts, c.deps, "aaaa2", false); err != nil {
		t.Fatal(err)
	}
	// Already in the requested state: nothing changes.
	if err := runPin(ctx, c.opts, c.deps, "aaaa2", true); err != nil {
		t.Fatal(err)
	}
	want := []string{"tag aaaa1111bbbb +ouroboros:pinned -", "tag aaaa2222cccc + -ouroboros:pinned"}
	if !slices.Equal(c.repo.calls, want) || c.locked != 3 {
		t.Fatalf("calls %v locked %d", c.repo.calls, c.locked)
	}
	if !strings.Contains(c.out.String(), "already pinned") {
		t.Fatalf("output %q", c.out)
	}
}

func TestCommandsRefuseDuringRun(t *testing.T) {
	c := newRepoCase(t)
	c.lockErr = lock.ErrLocked
	ctx := context.Background()
	for name, err := range map[string]error{
		"pin":   runPin(ctx, c.opts, c.deps, "aaaa1", true),
		"check": runCheck(ctx, c.opts, c.deps, 0),
	} {
		if !errors.Is(err, errRunInProgress) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if len(c.repo.calls) != 0 {
		t.Fatalf("calls %v", c.repo.calls)
	}
}

func TestCheckCommand(t *testing.T) {
	c := newRepoCase(t)
	ctx := context.Background()
	if err := runCheck(ctx, c.opts, c.deps, 2.5); err != nil {
		t.Fatal(err)
	}
	c.repo.checkErr = errors.New("pack damaged")
	if err := runCheck(ctx, c.opts, c.deps, 0); err == nil || !strings.Contains(err.Error(), "pack damaged") {
		t.Fatalf("got %v", err)
	}
	if err := runCheck(ctx, c.opts, c.deps, 101); err == nil {
		t.Fatal("101% accepted")
	}
	if !slices.Equal(c.repo.calls, []string{"check 2.5%", "check "}) {
		t.Fatalf("calls %v", c.repo.calls)
	}
	recs, _, _ := state.ReadHistory(filepath.Join(c.dir, state.HistoryFile))
	if len(recs) != 2 || !recs[0].OK || recs[0].ReadData != "2.5%" || recs[1].OK {
		t.Fatalf("history %+v", recs)
	}
	st, _ := state.Load(filepath.Join(c.dir, state.StateFile))
	if st.LastCheck.IsZero() || st.LastReadDataCheck.IsZero() || !st.LastCheck.After(st.LastReadDataCheck) {
		t.Fatalf("state %+v", st)
	}
}

func TestRestoreCommand(t *testing.T) {
	c := newRepoCase(t)
	ctx := context.Background()
	target := filepath.Join(c.dir, "restore")
	ro := restoreOptions{target: target, include: []string{`C:\docs\a.txt`}}
	if err := runRestore(ctx, c.opts, c.deps, "aaaa1", ro); err != nil {
		t.Fatal(err)
	}
	if c.repo.restored.SnapshotID != "aaaa1111bbbb" || c.repo.restored.Target != target || len(c.repo.restored.Include) != 1 {
		t.Fatalf("restored %+v", c.repo.restored)
	}
	if c.locked != 0 {
		t.Error("restore must not take the lock")
	}

	os.MkdirAll(target, 0o755)
	os.WriteFile(filepath.Join(target, "existing.txt"), []byte("x"), 0o644)
	repo := filepath.Join(c.dir, "repo")
	tests := []struct {
		name   string
		target string
		over   bool
		want   string
	}{
		{"not empty", target, false, "not empty"},
		{"inside repository", filepath.Join(repo, "data"), true, "overlaps the repository"},
		{"repository itself", repo, true, "overlaps the repository"},
		{"parent of repository", c.dir, true, "overlaps the repository"},
		{"ballast", repo + ".ballast", true, "overlaps the repository"},
		{"a file", filepath.Join(target, "existing.txt"), true, "is a file"},
	}
	for _, tt := range tests {
		err := runRestore(ctx, c.opts, c.deps, "aaaa1", restoreOptions{target: tt.target, overwrite: tt.over})
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: got %v, want %q", tt.name, err, tt.want)
		}
	}
	if err := runRestore(ctx, c.opts, c.deps, "aaaa1", restoreOptions{target: target, overwrite: true}); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if n := len(c.repo.calls); n != 2 {
		t.Fatalf("calls %v", c.repo.calls)
	}
}

func TestSnapshotsJSON(t *testing.T) {
	c := newRepoCase(t)
	c.repo.snaps[0].Files, c.repo.snaps[0].Bytes = 3, 300
	c.opts.out = &jsonLines{w: c.out, command: "snapshots"}
	if err := runSnapshots(context.Background(), c.opts, c.deps, "docs"); err != nil {
		t.Fatal(err)
	}
	decodeJSONLines(t, c.out.String())
	var got struct {
		Data struct {
			Snapshots []snapshotOut `json:"snapshots"`
		} `json:"data"`
	}
	if err := json.Unmarshal(c.out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	s := got.Data.Snapshots
	if len(s) != 1 || s[0].Job != "docs" || s[0].ShortID != "aaaa1111" || len(s[0].ID) <= 8 ||
		s[0].SizeBytes == nil || *s[0].SizeBytes != 300 || *s[0].Files != 3 {
		t.Fatalf("%+v", s)
	}

	c.out.Reset()
	c.opts.out = &jsonLines{w: c.out, command: "snapshots"}
	runSnapshots(context.Background(), c.opts, c.deps, "none")
	if !strings.Contains(c.out.String(), `"data":{"snapshots":[]}`) {
		t.Fatalf("empty list: %s", c.out)
	}
}

func TestCheckJSON(t *testing.T) {
	c := newRepoCase(t)
	c.opts.out = &jsonLines{w: c.out, command: "check"}
	if err := runCheck(context.Background(), c.opts, c.deps, 5); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.out.String(), `"type":"result"`) || !strings.Contains(c.out.String(), `"data":{"read_data":"5%"}`) {
		t.Fatalf("%s", c.out)
	}
	c.repo.checkErr = errors.New("pack damaged")
	err := runCheck(context.Background(), c.opts, c.deps, 0)
	if errorCode(err) != "check_failed" || !strings.Contains(err.Error(), "repository check failed: pack damaged") {
		t.Fatalf("got %v (%s)", err, errorCode(err))
	}
}

func TestRestoreJSON(t *testing.T) {
	c := newRepoCase(t)
	c.opts.out = &jsonLines{w: c.out, command: "restore"}
	target := filepath.Join(c.dir, "restore")
	if err := runRestore(context.Background(), c.opts, c.deps, "aaaa1", restoreOptions{target: target}); err != nil {
		t.Fatal(err)
	}
	lines := decodeJSONLines(t, c.out.String())
	if len(lines) != 2 || lines[0]["event"] != "progress" || lines[0]["data"].(map[string]any)["files_restored"] != 1.0 {
		t.Fatalf("%s", c.out)
	}
	res := lines[1]["data"].(map[string]any)
	if res["snapshot_id"] != "aaaa1111bbbb" || res["target"] != target || res["files_restored"] != 2.0 || res["job"] != "docs" {
		t.Fatalf("result %v", res)
	}
}

func TestLsCommand(t *testing.T) {
	c := newRepoCase(t)
	ctx := context.Background()
	if err := runLs(ctx, c.opts, c.deps, "aaaa1", `C:\docs`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.out.String(), `2 entries in C:\docs`) || !strings.Contains(c.out.String(), "5 B") {
		t.Fatalf("%s", c.out)
	}
	if c.repo.calls[len(c.repo.calls)-1] != "ls aaaa1111bbbb /C/docs" || c.locked != 0 {
		t.Fatalf("calls %v locked %d", c.repo.calls, c.locked)
	}

	c.out.Reset()
	c.opts.out = &jsonLines{w: c.out, command: "ls"}
	if err := runLs(ctx, c.opts, c.deps, "aaaa1", ""); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Data lsOut `json:"data"`
	}
	if err := json.Unmarshal(c.out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	e := got.Data.Entries
	if got.Data.Folder != "" || len(e) != 2 || e[0].SizeBytes == nil || e[1].SizeBytes != nil || e[1].Path != "\\sub" {
		t.Fatalf("%+v", got.Data)
	}
	err := runLs(ctx, c.opts, c.deps, "aaaa1", `C:\missing`)
	if errorCode(err) != "path_not_found" {
		t.Fatalf("got %v", err)
	}
}
