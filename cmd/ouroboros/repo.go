package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/WildPino/ouroboros/internal/backend"
	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/lock"
	"github.com/WildPino/ouroboros/internal/space"
	"github.com/WildPino/ouroboros/internal/state"
)

// errRunInProgress is returned by commands that change the repository
// while a run holds the lock.
var errRunInProgress = errors.New("a backup run is in progress; try again when it has finished")

// notElevatedError is returned when a command needs the repository password
// but runs without administrator rights.
type notElevatedError struct{ what string }

func (e notElevatedError) Error() string {
	return fmt.Sprintf("%s must be run as Administrator: the repository password is readable only by administrators", e.what)
}

func (e notElevatedError) Is(target error) bool {
	_, ok := target.(notElevatedError)
	return ok
}

var errNotElevated = notElevatedError{}

// errAdminRequired marks commands that change the system and need
// administrator rights for that, not for the password.
var errAdminRequired = errors.New("it changes the scheduled task")

// errCheckFailed wraps the error of a check that found problems.
var errCheckFailed = errors.New("repository check failed")

// repoSession is an open repository for one command.
type repoSession struct {
	cfg     *config.Config
	backend backend.Backend
	host    string
	release func()
}

// openRepo prepares a repository command. With exclusive it takes the
// single-instance lock and returns lock.ErrLocked if a run holds it.
func openRepo(ctx context.Context, opts *options, d repoDeps, what string, exclusive bool) (*repoSession, error) {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return nil, err
	}
	elevated, err := d.isElevated()
	if err != nil {
		return nil, fmt.Errorf("check privileges: %w", err)
	}
	if !elevated {
		return nil, notElevatedError{what}
	}
	s := &repoSession{cfg: cfg, release: func() {}}
	if exclusive {
		l, err := d.acquire()
		if err != nil {
			return nil, err
		}
		s.release = func() { l.Release() }
	}
	ok := false
	defer func() {
		if !ok {
			s.release()
		}
	}()
	if err := d.checkRestic(ctx, cfg); err != nil {
		return nil, err
	}
	if s.backend, err = d.newBackend(cfg, opts.configPath); err != nil {
		return nil, err
	}
	if s.host, err = d.hostname(); err != nil {
		return nil, err
	}
	ok = true
	return s, nil
}

// openRepoExclusive is openRepo for commands that change the repository:
// they refuse to run alongside a backup.
func openRepoExclusive(ctx context.Context, opts *options, d repoDeps, what string) (*repoSession, error) {
	s, err := openRepo(ctx, opts, d, what, true)
	if errors.Is(err, lock.ErrLocked) {
		return nil, errRunInProgress
	}
	return s, err
}

var snapshotRef = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

// resolveSnapshot finds the snapshot whose ID starts with ref.
func resolveSnapshot(snaps []backend.Snapshot, ref string) (backend.Snapshot, error) {
	ref = strings.ToLower(ref)
	if !snapshotRef.MatchString(ref) {
		return backend.Snapshot{}, fmt.Errorf("%q is not a snapshot ID", ref)
	}
	var found []backend.Snapshot
	for _, s := range snaps {
		if strings.HasPrefix(s.ID, ref) {
			found = append(found, s)
		}
	}
	switch len(found) {
	case 0:
		return backend.Snapshot{}, fmt.Errorf("no snapshot with ID %s", ref)
	case 1:
		return found[0], nil
	default:
		return backend.Snapshot{}, fmt.Errorf("snapshot ID %s is ambiguous: %d snapshots match", ref, len(found))
	}
}

func newSnapshotsCmd(opts *options) *cobra.Command {
	var job string
	cmd := &cobra.Command{
		Use:   "snapshots",
		Short: "List snapshots, oldest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSnapshots(cmd.Context(), opts, defaultRepoDeps(), job)
		},
	}
	cmd.Flags().StringVar(&job, "job", "", "only snapshots of this job")
	return supportsJSON(cmd)
}

func runSnapshots(ctx context.Context, opts *options, d repoDeps, job string) error {
	s, err := openRepo(ctx, opts, d, "snapshots", false)
	if err != nil {
		return err
	}
	defer s.release()
	snaps, err := s.backend.Snapshots(ctx, backend.SnapshotFilter{})
	if err != nil {
		return err
	}
	var shown []backend.Snapshot
	for _, sn := range snaps {
		if job == "" || strings.EqualFold(sn.Job(), job) {
			shown = append(shown, sn)
		}
	}
	if opts.out != nil {
		opts.out.result(snapshotsJSON(shown))
		return nil
	}
	if len(shown) == 0 {
		fmt.Fprintln(opts.stdout, "No snapshots.")
		return nil
	}
	tw := tabwriter.NewWriter(opts.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTime\tJob\tPinned\tPaths")
	for _, sn := range shown {
		pinned := ""
		if sn.Pinned() {
			pinned = "pinned"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", shortID(sn.ID), sn.Time.Local().Format(time.DateTime),
			sn.Job(), pinned, strings.Join(sn.Paths, ", "))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(opts.stdout, "%d snapshots\n", len(shown))
	return nil
}

func newPinCmd(opts *options, pin bool) *cobra.Command {
	use, short := "pin SNAPSHOT", "Protect a snapshot from automatic deletion"
	if !pin {
		use, short = "unpin SNAPSHOT", "Allow a pinned snapshot to be deleted automatically again"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPin(cmd.Context(), opts, defaultRepoDeps(), args[0], pin)
		},
	}
}

func runPin(ctx context.Context, opts *options, d repoDeps, ref string, pin bool) error {
	what := "pin"
	if !pin {
		what = "unpin"
	}
	s, err := openRepoExclusive(ctx, opts, d, what)
	if err != nil {
		return err
	}
	defer s.release()
	snaps, err := s.backend.Snapshots(ctx, backend.SnapshotFilter{})
	if err != nil {
		return err
	}
	sn, err := resolveSnapshot(snaps, ref)
	if err != nil {
		return err
	}
	if sn.Pinned() == pin {
		fmt.Fprintf(opts.stdout, "Snapshot %s is already %sned.\n", shortID(sn.ID), what)
		return nil
	}
	var add, remove []string
	if pin {
		add = []string{backend.TagPinned}
	} else {
		remove = []string{backend.TagPinned}
	}
	if err := s.backend.Tag(ctx, sn.ID, add, remove); err != nil {
		return err
	}
	// restic rewrites a snapshot to change its tags, so the ID changes.
	fmt.Fprintf(opts.stdout, "Snapshot %s (%s, %s) %sned; restic gave it a new ID, see \"ouroboros snapshots\".\n",
		shortID(sn.ID), sn.Job(), sn.Time.Local().Format(time.DateTime), what)
	return nil
}

func newCheckCmd(opts *options) *cobra.Command {
	var readData float64
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Verify repository integrity",
		Long: "Verify the structure of the repository. With --read-data, also read back that\n" +
			"percentage of the data, chosen at random, and verify it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(cmd.Context(), opts, defaultRepoDeps(), readData)
		},
	}
	cmd.Flags().Float64Var(&readData, "read-data", 0, "also read back this `PERCENT` of the data (0-100)")
	return supportsJSON(cmd)
}

func runCheck(ctx context.Context, opts *options, d repoDeps, readData float64) error {
	if readData < 0 || readData > 100 {
		return fmt.Errorf("--read-data must be between 0 and 100, got %g", readData)
	}
	s, err := openRepoExclusive(ctx, opts, d, "check")
	if err != nil {
		return err
	}
	defer s.release()
	var copts backend.CheckOptions
	if readData > 0 {
		copts.ReadDataSubset = strconv.FormatFloat(readData, 'f', -1, 64) + "%"
	}
	start := time.Now()
	checkErr := s.backend.Check(ctx, copts)

	// A manual check counts as the periodic one.
	dir := filepath.Dir(opts.configPath)
	rec := state.Record{Time: time.Now(), Kind: state.KindCheck, OK: checkErr == nil,
		ReadData: copts.ReadDataSubset, Duration: time.Since(start).Seconds()}
	if checkErr != nil {
		rec.Error = checkErr.Error()
	}
	if err := state.Append(filepath.Join(dir, state.HistoryFile), rec); err != nil {
		fmt.Fprintf(opts.stderr, "warning: cannot record the check in the history: %v\n", err)
	}
	statePath := filepath.Join(dir, state.StateFile)
	if st, err := state.Load(statePath); err == nil {
		st.LastCheck = start
		if readData > 0 {
			st.LastReadDataCheck = start
		}
		if err := state.Save(statePath, st); err != nil {
			fmt.Fprintf(opts.stderr, "warning: cannot save the state: %v\n", err)
		}
	}

	if checkErr != nil {
		return fmt.Errorf("%w: %w", errCheckFailed, checkErr)
	}
	if opts.out != nil {
		opts.out.result(map[string]any{"read_data": copts.ReadDataSubset})
		return nil
	}
	fmt.Fprintln(opts.stdout, "No errors were found.")
	return nil
}

type restoreOptions struct {
	target    string
	include   []string
	overwrite bool
}

func newRestoreCmd(opts *options) *cobra.Command {
	var ro restoreOptions
	cmd := &cobra.Command{
		Use:   "restore SNAPSHOT --target DIR",
		Short: "Restore files from a snapshot",
		Long: "Restore a snapshot, or the parts of it matching --include, into the target folder.\n" +
			"Files keep their full original path under the target, for example\n" +
			`C:\Users\me\file.txt restored to D:\restore becomes D:\restore\C\Users\me\file.txt.` + "\n" +
			"A target folder that is not empty is refused unless --overwrite is given; then\n" +
			"files with the same name are replaced.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestore(cmd.Context(), opts, defaultRepoDeps(), args[0], ro)
		},
	}
	cmd.Flags().StringVar(&ro.target, "target", "", "folder to restore into (required)")
	cmd.Flags().StringArrayVar(&ro.include, "include", nil, "restore only paths matching this `PATTERN` (repeatable)")
	cmd.Flags().BoolVar(&ro.overwrite, "overwrite", false, "allow a target folder that is not empty")
	cmd.MarkFlagRequired("target")
	return supportsJSON(cmd)
}

func runRestore(ctx context.Context, opts *options, d repoDeps, ref string, ro restoreOptions) error {
	if ro.target == "" {
		return errors.New("--target is required")
	}
	target, err := filepath.Abs(ro.target)
	if err != nil {
		return err
	}
	s, err := openRepo(ctx, opts, d, "restore", false)
	if err != nil {
		return err
	}
	defer s.release()
	if err := checkRestoreTarget(target, s.cfg.Repository.Path, ro.overwrite); err != nil {
		return err
	}
	snaps, err := s.backend.Snapshots(ctx, backend.SnapshotFilter{})
	if err != nil {
		return err
	}
	sn, err := resolveSnapshot(snaps, ref)
	if err != nil {
		return err
	}
	bo := backend.RestoreOptions{SnapshotID: sn.ID, Target: target, Include: ro.include}
	if opts.out != nil {
		bo.Progress = func(p backend.RestoreProgress) {
			opts.out.event("progress", restoreProgressOut{PercentDone: p.PercentDone, FilesRestored: p.FilesRestored,
				TotalFiles: p.TotalFiles, BytesRestored: p.BytesRestored, TotalBytes: p.TotalBytes})
		}
	}
	res, err := s.backend.Restore(ctx, bo)
	if err != nil {
		return err
	}
	if opts.out != nil {
		opts.out.result(restoreOut{
			SnapshotID: sn.ID, Job: sn.Job(), Time: sn.Time.UTC(), Target: target,
			TotalFiles: res.TotalFiles, FilesRestored: res.FilesRestored,
			TotalBytes: res.TotalBytes, BytesRestored: res.BytesRestored,
		})
		return nil
	}
	fmt.Fprintf(opts.stdout, "Restored snapshot %s (%s, %s) to %s\n", shortID(sn.ID), sn.Job(),
		sn.Time.Local().Format(time.DateTime), target)
	return nil
}

// checkRestoreTarget refuses targets that would write into the repository
// or its ballast, and, without overwrite, folders that already hold files.
func checkRestoreTarget(target, repo string, overwrite bool) error {
	if config.IsWithin(target, repo) || config.IsWithin(repo, target) || config.IsWithin(target, space.BallastPath(repo)) {
		return fmt.Errorf("cannot restore into %s: it overlaps the repository %s", target, repo)
	}
	fi, err := os.Stat(target)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case !fi.IsDir():
		return fmt.Errorf("%s is a file, not a folder", target)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return err
	}
	if len(entries) > 0 && !overwrite {
		return fmt.Errorf("%s is not empty: choose an empty folder, or pass --overwrite to replace files with the same name", target)
	}
	return nil
}

// Schema of "snapshots --json"; documented in docs/JSON.md.
type snapshotOut struct {
	ID        string    `json:"id"`
	ShortID   string    `json:"short_id"`
	Time      time.Time `json:"time"`
	Job       string    `json:"job"`
	Pinned    bool      `json:"pinned"`
	Hostname  string    `json:"hostname"`
	Paths     []string  `json:"paths"`
	Files     *uint64   `json:"files"`
	SizeBytes *uint64   `json:"size_bytes"`
}

func snapshotsJSON(snaps []backend.Snapshot) map[string][]snapshotOut {
	out := []snapshotOut{}
	for _, sn := range snaps {
		o := snapshotOut{
			ID: sn.ID, ShortID: shortID(sn.ID), Time: sn.Time.UTC(), Job: sn.Job(),
			Pinned: sn.Pinned(), Hostname: sn.Hostname, Paths: sn.Paths,
		}
		if o.Paths == nil {
			o.Paths = []string{}
		}
		if sn.Bytes > 0 {
			o.Files, o.SizeBytes = ptr(sn.Files), ptr(sn.Bytes)
		}
		out = append(out, o)
	}
	return map[string][]snapshotOut{"snapshots": out}
}

// Schema of "restore --json"; documented in docs/JSON.md.
type restoreProgressOut struct {
	PercentDone   float64 `json:"percent_done"`
	FilesRestored uint64  `json:"files_restored"`
	TotalFiles    uint64  `json:"total_files"`
	BytesRestored uint64  `json:"bytes_restored"`
	TotalBytes    uint64  `json:"total_bytes"`
}

type restoreOut struct {
	SnapshotID    string    `json:"snapshot_id"`
	Job           string    `json:"job"`
	Time          time.Time `json:"time"`
	Target        string    `json:"target"`
	TotalFiles    uint64    `json:"total_files"`
	FilesRestored uint64    `json:"files_restored"`
	TotalBytes    uint64    `json:"total_bytes"`
	BytesRestored uint64    `json:"bytes_restored"`
}
