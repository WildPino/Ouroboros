package restic

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
)

// Timeouts bound each kind of restic operation.
type Timeouts struct {
	Short   time.Duration // init, snapshots, forget, tag, ls
	Backup  time.Duration
	Prune   time.Duration
	Check   time.Duration
	Restore time.Duration
}

// DefaultTimeouts are generous enough for a first backup of several
// terabytes; they exist to recover from a hung process, not to limit work.
func DefaultTimeouts() Timeouts {
	return Timeouts{
		Short:   15 * time.Minute,
		Backup:  72 * time.Hour,
		Prune:   24 * time.Hour,
		Check:   48 * time.Hour,
		Restore: 72 * time.Hour,
	}
}

// Options configures the restic backend.
type Options struct {
	Executable string
	Repository string
	// PasswordCommand is run by restic to obtain the password; see
	// PasswordCommand for how to build it.
	PasswordCommand string
	// Hostname recorded in snapshots; defaults to the machine name.
	Hostname        string
	Compression     string // off, auto, max
	PackSizeMB      int    // 0 = restic default
	ReadConcurrency int    // 0 = restic default
	Priority        string // config.Priority* value
	// CacheDir overrides the restic cache location; empty = restic default.
	CacheDir string
	Timeouts Timeouts
}

// Restic implements backend.Backend with the restic executable.
type Restic struct {
	opts Options
	r    runner
}

var _ backend.Backend = (*Restic)(nil)

// New validates opts and returns a backend.
func New(opts Options) (*Restic, error) {
	switch {
	case opts.Executable == "":
		return nil, errors.New("restic: executable not set")
	case opts.Repository == "":
		return nil, errors.New("restic: repository not set")
	case opts.PasswordCommand == "":
		return nil, errors.New("restic: password command not set")
	}
	if opts.Hostname == "" {
		h, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("restic: hostname: %w", err)
		}
		opts.Hostname = h
	}
	if opts.Timeouts == (Timeouts{}) {
		opts.Timeouts = DefaultTimeouts()
	}
	global := []string{"--repo", opts.Repository, "--password-command", opts.PasswordCommand}
	if opts.Compression != "" {
		global = append(global, "--compression", opts.Compression)
	}
	if opts.PackSizeMB > 0 {
		global = append(global, "--pack-size", strconv.Itoa(opts.PackSizeMB))
	}
	if opts.CacheDir != "" {
		global = append(global, "--cache-dir", opts.CacheDir)
	}
	return &Restic{
		opts: opts,
		r:    runner{exe: opts.Executable, global: global, priority: opts.Priority},
	}, nil
}

// PasswordCommand builds the --password-command value that runs
// "<exe> internal password <args...>". restic splits the string with shell
// rules, so every element is double-quoted.
func PasswordCommand(exe string, args ...string) string {
	parts := make([]string, 0, len(args)+3)
	for _, a := range append([]string{exe, "internal", "password"}, args...) {
		parts = append(parts, `"`+a+`"`)
	}
	return strings.Join(parts, " ")
}

// Version returns the restic version string.
func (b *Restic) Version(ctx context.Context) (string, error) {
	v, err := ExecutableVersion(ctx, b.opts.Executable)
	if err != nil {
		return "", err
	}
	return v.String(), nil
}

// Init creates the repository with format version 2 (compression support).
func (b *Restic) Init(ctx context.Context) error {
	_, err := b.run(ctx, call{
		args:    []string{"init", "--repository-version", "2", "--json"},
		timeout: b.opts.Timeouts.Short,
	})
	return err
}

// Backup creates a snapshot of job. A partial backup (some files unreadable)
// is not an error: it returns Incomplete and the per-file warnings.
func (b *Restic) Backup(ctx context.Context, job backend.Job) (backend.BackupResult, error) {
	args, err := b.backupArgs(job, false)
	if err != nil {
		return backend.BackupResult{}, err
	}

	res := backend.BackupResult{Start: time.Now()}
	gotSummary := false
	out, err := b.run(ctx, call{
		args:    args,
		timeout: b.opts.Timeouts.Backup,
		onLine: func(line []byte) error {
			ok, err := parseBackupLine(line, &res, job.Progress)
			gotSummary = gotSummary || ok
			return err
		},
	})
	if res.End.IsZero() {
		res.End = time.Now()
	}
	info := parseStderr(out.stderr)
	res.Warnings = info.warnings
	if info.dropped > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("... and %d more", info.dropped))
	}
	if errors.Is(err, ErrIncomplete) && gotSummary {
		res.Incomplete = true
		err = nil
	}
	if err != nil {
		return res, err
	}
	if !gotSummary || res.SnapshotID == "" {
		return res, errors.New("restic backup: finished without reporting a snapshot")
	}
	return res, nil
}

func (b *Restic) backupArgs(job backend.Job, dryRun bool) ([]string, error) {
	if len(job.Paths) == 0 {
		return nil, errors.New("restic backup: no paths")
	}
	args := []string{"backup", "--json",
		"--host", b.opts.Hostname,
		"--tag", backend.TagOuroboros,
		"--tag", backend.JobTag(job.Name),
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	if job.VSS {
		args = append(args, "--use-fs-snapshot")
	}
	for _, ex := range job.Exclude {
		args = append(args, "--iexclude", ex)
	}
	if job.ExcludeFile != "" {
		args = append(args, "--iexclude-file", job.ExcludeFile)
	}
	if b.opts.ReadConcurrency > 0 {
		args = append(args, "--read-concurrency", strconv.Itoa(b.opts.ReadConcurrency))
	}
	args = append(args, "--")
	return append(args, job.Paths...), nil
}

// EstimateBackup runs "restic backup --dry-run" for job and returns the
// uncompressed size of the data that a real backup would add.
func (b *Restic) EstimateBackup(ctx context.Context, job backend.Job) (uint64, error) {
	args, err := b.backupArgs(job, true)
	if err != nil {
		return 0, err
	}
	var res backend.BackupResult
	gotSummary := false
	_, err = b.run(ctx, call{
		args:    args,
		timeout: b.opts.Timeouts.Backup,
		onLine: func(line []byte) error {
			ok, err := parseBackupLine(line, &res, nil)
			gotSummary = gotSummary || ok
			return err
		},
	})
	// Unreadable files only make the estimate smaller; the estimate is
	// still usable.
	if errors.Is(err, ErrIncomplete) && gotSummary {
		err = nil
	}
	if err != nil {
		return 0, err
	}
	if !gotSummary {
		return 0, errors.New("restic backup --dry-run: finished without a summary")
	}
	return res.BytesAdded, nil
}

// Snapshots lists snapshots matching filter, oldest first.
func (b *Restic) Snapshots(ctx context.Context, filter backend.SnapshotFilter) ([]backend.Snapshot, error) {
	args := []string{"snapshots", "--json"}
	if filter.Host != "" {
		args = append(args, "--host", filter.Host)
	}
	if len(filter.Tags) > 0 {
		// One --tag with a comma-separated list means "all of these".
		args = append(args, "--tag", strings.Join(filter.Tags, ","))
	}
	out, err := b.run(ctx, call{args: args, timeout: b.opts.Timeouts.Short})
	if err != nil {
		return nil, err
	}
	return parseSnapshots(out.stdout)
}

// Forget removes the given snapshots from the index; their data is freed by
// a later Prune. Only internal/space decides what to forget.
func (b *Restic) Forget(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := checkIDs(ids...); err != nil {
		return err
	}
	_, err := b.run(ctx, call{
		args:    append([]string{"forget", "--"}, ids...),
		timeout: b.opts.Timeouts.Short,
	})
	return err
}

// Prune deletes data no longer referenced by any snapshot.
func (b *Restic) Prune(ctx context.Context, opts backend.PruneOptions) (backend.PruneResult, error) {
	args := []string{"prune"}
	if opts.MaxUnused != "" {
		args = append(args, "--max-unused", opts.MaxUnused)
	}
	start := time.Now()
	_, err := b.run(ctx, call{args: args, timeout: b.opts.Timeouts.Prune})
	return backend.PruneResult{Duration: time.Since(start)}, err
}

// Check verifies the repository structure and optionally part of the data.
func (b *Restic) Check(ctx context.Context, opts backend.CheckOptions) error {
	args := []string{"check"}
	if opts.ReadDataSubset != "" {
		args = append(args, "--read-data-subset", opts.ReadDataSubset)
	}
	_, err := b.run(ctx, call{args: args, timeout: b.opts.Timeouts.Check})
	return err
}

// Restore extracts a snapshot (or the included parts of it) into a directory.
func (b *Restic) Restore(ctx context.Context, opts backend.RestoreOptions) (backend.RestoreResult, error) {
	var res backend.RestoreResult
	if opts.Target == "" {
		return res, errors.New("restic restore: no target directory")
	}
	if err := checkIDs(opts.SnapshotID); err != nil {
		return res, err
	}
	args := []string{"restore", "--json", "--target", opts.Target}
	for _, inc := range opts.Include {
		// Case-insensitive, like the file systems the paths come from.
		args = append(args, "--iinclude", includePattern(inc))
	}
	args = append(args, "--", opts.SnapshotID)
	_, err := b.run(ctx, call{
		args:    args,
		timeout: b.opts.Timeouts.Restore,
		onLine:  func(line []byte) error { return parseRestoreLine(line, &res, opts.Progress) },
	})
	if err != nil {
		return res, err
	}
	// restic reports success when the patterns match nothing.
	if len(opts.Include) > 0 && res.TotalFiles == 0 {
		return res, fmt.Errorf("%w: %s", ErrNothingMatched, strings.Join(opts.Include, ", "))
	}
	return res, nil
}

// includePattern turns a Windows path into the form restic matches
// snapshot paths against: C:\Users\me becomes /C/Users/me. Without this an
// absolute Windows path matches nothing. Relative patterns only get forward
// slashes.
func includePattern(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if len(p) >= 2 && p[1] == ':' && isASCIILetter(p[0]) && (len(p) == 2 || p[2] == '/') {
		p = "/" + strings.ToUpper(p[:1]) + p[2:]
	}
	return p
}

func isASCIILetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

// List returns the entries directly inside dir in snapshot id.
func (b *Restic) List(ctx context.Context, id, dir string) ([]backend.Node, error) {
	if err := checkIDs(id); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(dir, "/") {
		return nil, fmt.Errorf("restic ls: %q is not a snapshot path", dir)
	}
	var nodes []backend.Node
	found := dir == "/"
	_, err := b.run(ctx, call{
		args:    []string{"ls", "--json", "--", id, dir},
		timeout: b.opts.Timeouts.Short,
		onLine: func(line []byte) error {
			n, ok, err := parseLsLine(line)
			if err != nil || !ok {
				return err
			}
			// restic lists the folder itself before its entries.
			if n.Path == dir {
				found = true
				return nil
			}
			nodes = append(nodes, n)
			return nil
		},
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", backend.ErrPathNotFound, dir)
	}
	return nodes, nil
}

// Tag adds and removes tags. restic rewrites the snapshot, so its ID changes.
func (b *Restic) Tag(ctx context.Context, id string, add, remove []string) error {
	if len(add) == 0 && len(remove) == 0 {
		return errors.New("restic tag: nothing to change")
	}
	if err := checkIDs(id); err != nil {
		return err
	}
	args := []string{"tag"}
	for _, t := range add {
		args = append(args, "--add", t)
	}
	for _, t := range remove {
		args = append(args, "--remove", t)
	}
	args = append(args, "--", id)
	_, err := b.run(ctx, call{args: args, timeout: b.opts.Timeouts.Short})
	return err
}

var snapshotID = regexp.MustCompile(`^[0-9a-f]{8,64}$`)

// checkIDs rejects anything that is not a (short) snapshot ID, so that a
// malformed value can never be taken by restic as a flag or a filter.
func checkIDs(ids ...string) error {
	for _, id := range ids {
		if !snapshotID.MatchString(id) {
			return fmt.Errorf("invalid snapshot id %q", id)
		}
	}
	return nil
}

// run executes a restic command. When the repository is locked it runs
// "restic unlock", which removes only stale locks: those of processes that
// no longer exist on this host, or not refreshed for 30 minutes. A process
// killed by a crash or a power loss leaves such a lock behind, and restic
// does not remove it by itself before exclusive operations such as prune
// and check. The command is retried once; the lock of a live process
// stays, and the second attempt fails as the first did.
func (b *Restic) run(ctx context.Context, c call) (output, error) {
	out, err := b.r.run(ctx, c)
	if !errors.Is(err, ErrRepoLocked) || c.args[0] == "unlock" || ctx.Err() != nil {
		return out, err
	}
	if _, uerr := b.r.run(ctx, call{args: []string{"unlock"}, timeout: b.opts.Timeouts.Short}); uerr != nil {
		return out, err
	}
	return b.r.run(ctx, c)
}
