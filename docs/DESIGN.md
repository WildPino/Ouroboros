# Ouroboros: Design

This document describes what Ouroboros does and how. It is the reference for
implementation: if code and this document disagree, one of them is a bug.

## 1. Goals and non-goals

### Goals

1. Keep versioned backups of configured folders and volumes on a dedicated
   partition, unattended.
2. Use the partition as fully as possible: maximum history that fits, while
   always leaving a configured free-space margin.
3. Delete history oldest-first, never below configured safety limits.
4. Never leave the repository in an inconsistent state, even on power loss,
   full disk or interrupted runs.
5. Keep backups readable with plain `restic`, without Ouroboros.

### Non-goals (v1)

- A new archive or compression format. Storage is delegated to restic.
- Cloud or remote repositories (planned later as mirroring).
- Linux and macOS support (the core is portable, platform code is not; planned later).

## 2. Architecture

Two builds of the same Go code: `ouroboros.exe`, the command line, and
`Ouroboros.exe`, the graphical interface and installer, which carries the
command line build and `restic.exe` inside it (§12). No other runtime
dependency: the interface uses WebView2, part of Windows 10 and 11.

```
cmd/ouroboros/            CLI entry point (subcommands)
internal/config/          TOML parsing, defaults, validation, size/duration types
internal/backend/         Backend interface + shared types
internal/backend/restic/  restic implementation (process runner, JSON parsing)
internal/space/           free space, repo size, growth predictor, planner, ballast
internal/scheduler/       due-job computation, run orchestration
internal/state/           persistent state and history (atomic writes)
internal/lock/            single-instance lock (named mutex)
internal/secret/          repository password storage (DPAPI)
internal/platform/windows Task Scheduler, Event Log, disk APIs, privileges
internal/logging/         structured logs, rotation
internal/gui/             window, embedded web interface, bridge to the CLI
internal/setup/           installation into Program Files, uninstallation
```

### 2.1 Backend interface

All storage operations go through one interface so that other engines (Kopia,
Borg on Linux) can be added later without touching the planner.

```go
type Backend interface {
    Version(ctx context.Context) (string, error)
    Init(ctx context.Context) error
    Backup(ctx context.Context, job Job) (BackupResult, error)
    EstimateBackup(ctx context.Context, job Job) (uint64, error) // dry run
    Snapshots(ctx context.Context, filter SnapshotFilter) ([]Snapshot, error)
    Forget(ctx context.Context, ids []string) error
    Prune(ctx context.Context, opts PruneOptions) (PruneResult, error)
    Check(ctx context.Context, opts CheckOptions) error
    Restore(ctx context.Context, opts RestoreOptions) error
    Tag(ctx context.Context, id string, add, remove []string) error
}
```

`BackupResult` carries at least: snapshot ID, start/end time, files new/changed/
unmodified, bytes processed, bytes added (uncompressed) and bytes added (packed,
i.e. actual growth on disk).

### 2.2 restic backend

- Minimum restic version: 0.17. Checked by `doctor` and at startup.
- Every invocation uses `--json` where available and parses the output; no
  scraping of human-readable text.
- Password is supplied via `--password-command "ouroboros.exe internal password"`
  so it never appears in environment variables, command lines or logs.
- Repository created with format version 2 (compression support).
- Backup flags: `--compression <config>`, `--use-fs-snapshot` when `vss = true`,
  `--tag ouroboros --tag job:<name>`, `--host <hostname>`,
  `--iexclude` / `--iexclude-file` (case-insensitive, like Windows paths),
  `--read-concurrency` and `--pack-size` from the memory profile.
- Snapshots are attributed to jobs exclusively through the `job:<name>` tag.
- Pinned snapshots carry the tag `ouroboros:pinned`.
- Never use `--no-lock`. Never touch files inside the repository directly.
- A restic process killed by a crash or a power loss leaves its lock in the
  repository, and restic does not remove it by itself before exclusive
  operations (prune, check). When a command fails because the repository is
  locked, Ouroboros runs `restic unlock` without `--remove-all`, which removes
  only stale locks (process gone from this host, or not refreshed for 30
  minutes), and retries the command once. The lock of a running process is
  never removed.
- Exit codes are mapped to typed errors (wrong password, repository locked,
  repository missing, partial backup with unreadable files, fatal).
  restic exit code 3 (some source files unreadable, snapshot still created) is
  a warning, not a failure.

### 2.3 Process execution

- Child processes are started with a job object so they die with Ouroboros.
- Every call has a context with a timeout, configurable per operation in
  `[timeouts]` (`backup`, `prune`, `check`, `restore`, and `other` for init,
  snapshots, forget and tag). The defaults (3d, 1d, 2d, 3d, 15m) only stop a
  hung process; they do not limit normal work.
- stdout is streamed and parsed line by line; stderr is captured for logs.
- Process priority for backups: below normal (configurable), so the PC stays
  responsive.

## 3. Space management

This is the core of the project. All of it lives in `internal/space` and must
be pure, deterministic logic wherever possible, with I/O behind interfaces so
it can be unit tested exhaustively.

### 3.1 Measurements

- **Free space**: `GetDiskFreeSpaceExW` on the repository path (works for drive
  letters and mounted folders). Abstracted as `FreeSpaceProvider`.
- **Repository size**: sum of file sizes under the repository directory
  (fast directory walk), not `restic stats` (slow on large repos).
- **Ballast**: size of the ballast file, counted as reclaimable.

### 3.2 Targets

From config:

- `min_free`: absolute (`"50GB"`) or percentage of the partition (`"10%"`).
  The free space that must remain after every operation.
- `max_repo_size` (optional): hard cap on repository size even if the
  partition has more room.
- `keep_min_snapshots`: per job, the newest N snapshots are never deleted.
- `protect_newer_than`: snapshots younger than this are never deleted.
- Pinned snapshots are never deleted automatically.

### 3.3 Growth prediction

Before each backup the planner needs an estimate `G` of how much the repository
will grow.

- History available (≥ 1 previous run of this job): `G = max(packed bytes added
  in the last 10 runs of this job) × growth_factor` (default 1.5).
- First run of a job: run `restic backup --dry-run --json` and use the reported
  data size (uncompressed, an upper bound) × `first_run_factor` (default 1.0).
  If that is larger than what the partition can ever hold, fail early with a
  clear error instead of deleting history.

### 3.4 Required free space

```
need = G + min_free_bytes + prune_headroom
```

`prune_headroom` (default 2% of partition, minimum 1 GB) is the space prune
needs to write repacked data before deleting old packs.

If `max_repo_size` is set, also require `repo_size + G ≤ max_repo_size`.

### 3.5 Reclaim algorithm

Input: bytes to free `D`. Output: snapshots forgotten, bytes actually freed.

1. List all snapshots (all jobs, this host). Build the candidate list by
   removing: the newest `keep_min_snapshots` per job, snapshots newer than
   `protect_newer_than`, pinned snapshots.
2. Sort candidates oldest first (by snapshot time, tie-break by ID).
3. Batch size `b = 1`.
4. Loop:
   1. If no candidates left: stop, report `ErrCannotReclaim` with details
      (how much was freed, how much was still needed). The backup for this job
      is skipped. Protected snapshots are **never** deleted to make room.
   2. Take the oldest `b` candidates, `forget` them.
   3. `prune --max-unused 0` (under pressure all unused space is reclaimed).
      If free space is below `prune_headroom`, delete the ballast first.
   4. Measure free space again. If `free ≥ need`: stop, success.
   5. `b = min(b × 2, 16)`. Repeat.
5. Recreate the ballast if it was deleted and space allows.

Rationale: with deduplication, the space freed by deleting a snapshot cannot be
known in advance (most of its data may be shared with newer snapshots).
Measuring after each prune is the only reliable method. Exponential batching
keeps the number of slow prune runs logarithmic.

`ouroboros reclaim --dry-run` prints the candidate list and the order of
deletion without changing anything.

### 3.6 Normal-operation pruning

When there is no space pressure, restic `prune` runs at most once per
`prune_interval` (default 7 days) with `--max-unused 5%` to keep the repository
tidy without heavy repacking.

### 3.7 Optional time-based retention

`[retention]` (`keep_hourly`, `keep_daily`, `keep_weekly`, `keep_monthly`,
`keep_yearly`) uses the semantics of `restic forget --keep-*`, applied to each
job separately: walking from the newest snapshot, each rule keeps the newest
snapshot of each of the last N hours/days/weeks/months/years, in local time.
It runs before capacity checks. Empty by default: capacity alone decides.

The selection is computed by the space manager, not by restic, so that the
protections of §3.5 hold here too: the `keep_min_snapshots` newest of each
job, snapshots newer than `protect_newer_than` and pinned snapshots are never
forgotten, whatever the rules say. Retention only forgets; the following
space check prunes the forgotten data before deleting anything else.

### 3.8 Ballast file

A file of `reserve_file_size` (default 2 GB, `0` disables) on the repository
partition, next to the repository directory: `<repository path>.ballast`
(for `B:\ouroboros`, `B:\ouroboros.ballast`). For this reason the repository
cannot be the root of a drive or share. It guarantees that prune can run even if the disk is
completely full. Created by `init`, recreated after each reclaim.

### 3.9 Out-of-space during backup

A failed backup is treated as out of space when the free space measured right
after the failure is below `prune_headroom`. Measuring works in every Windows
language and does not depend on restic's error text.

restic does not fail at once when a write fails: it retries for about 15
minutes. While a backup runs, Ouroboros measures the free space on the
repository partition every 5 seconds. If it stays below two packs (twice the
resolved pack size, §7) for 60 seconds, restic is stopped and the backup is
treated as failed on a full disk. The lock restic leaves behind is stale and
is removed before the next operation (§2.2).

If a backup fails because the disk is full:

1. Delete the ballast.
2. Run `prune` (removes packs left by the interrupted backup).
3. Run the reclaim algorithm with `D` = previous prediction × 2.
4. Retry the backup once. If it fails again, report and stop.

## 4. Scheduling

- One Windows scheduled task, `\Ouroboros\Tick`, runs `ouroboros run` every
  15 minutes (configurable), as `SYSTEM`, with highest privileges, "run as soon
  as possible after a scheduled start is missed". With `run_on_battery = false`
  (default) the task does not start on battery power; a run already in
  progress is never stopped by unplugging, since restic would be killed and
  the work lost.
- `ouroboros run` acquires a global named mutex `Global\Ouroboros`; if already
  held, it exits immediately with code 0 (another run in progress).
- A job is due when `now ≥ last_success + interval`, or, if the last attempt
  failed, when `now ≥ last_attempt + retry_after` (default 30 min). After a
  failure only `retry_after` counts, so a failing job is retried every
  `retry_after` instead of on every tick. An attempt with no recorded outcome
  (the process was killed) counts as failed. A job that never ran is due
  immediately.
- If a recorded time lies more than 5 minutes in the future (the system clock
  was set back), the task is due: waiting for the clock to catch up could stop
  backups for an arbitrary time.
- Jobs run sequentially, in config order.
- Maintenance tasks (normal prune, `check`) run after jobs, when due. Their
  timestamps record the attempt, not the success: a failing check is reported
  once per interval, not on every tick. Right after `init` both are due.
- `run --job NAME` runs only that job (when due, or always with `--force`)
  and skips maintenance.

## 5. State

Stored in `%ProgramData%\Ouroboros\`:

| File | Content |
|---|---|
| `ouroboros.toml` | configuration |
| `secret.bin` | repository password, DPAPI machine scope; ACL: SYSTEM + Administrators only |
| `state.json` | per job: last attempt, last success, last error; maintenance timestamps |
| `history.jsonl` | one line per backup/reclaim/prune/check with stats (used by the predictor and `status`) |
| `logs\` | rotated log files |

All writes to `state.json` are atomic (write temp file, flush, rename).
`history.jsonl` is append-only; corrupted trailing lines are ignored on read.

## 6. Security

- Password stored with DPAPI (`CryptProtectData`, `CRYPTPROTECT_LOCAL_MACHINE`)
  plus a restrictive ACL. `init` reminds the user to record the password
  elsewhere.
- The password is never logged, printed, passed as an argument or placed in an
  environment variable.
- The scheduled task runs as SYSTEM; the config directory is writable only by
  Administrators so that a normal user cannot change what SYSTEM executes.
- `restic_path` in config must be an absolute path to a file writable only by
  Administrators; `doctor` warns otherwise.
- With `restic_path` empty, the task finds restic on the machine PATH, not on
  the PATH of the user who runs `install`. `install` refuses, and `doctor`
  fails, when restic is not on the machine PATH, and name the path to set in
  `restic_path`.

`init`:

- must run as Administrator, otherwise `secret.bin` could not be protected;
- requires a password of at least 8 characters that does not start or end
  with spaces (restic trims the output of the password command, so they would
  be lost);
- refuses to run when the repository directory exists and is not empty, or
  when `secret.bin` already exists; if creating the repository fails, the
  password it just stored is removed again;
- skips the ballast with a warning (exit code 3) when creating it would leave
  less than `min_free`.

## 7. Performance and memory

`[performance] memory_profile`:

| Profile | read concurrency | pack size | notes |
|---|---|---|---|
| `low` | 2 | 16 MiB | small machines |
| `auto` (default) | based on CPU count and free RAM | 32 MiB | |
| `high` | min(16, CPUs) | 64 MiB | large-RAM machines, fast disks |

`auto` reads with half the CPUs, between 2 and 8, and 32 MiB packs. restic
keeps about two packs per reader in memory, so readers are lowered, and then
the pack size to 16 MiB, until that stays within a quarter of the free RAM.
Below 1 GB of free RAM `auto` behaves like `low`. If the free RAM cannot be
read, it is not taken into account.

Explicit `read_concurrency` / `pack_size_mb` override the profile. Gains are
mostly for many small files and NVMe sources; compression ratio is not
affected.

`process_priority`: `idle`, `below_normal` (default), `normal`.

## 8. Integrity

- `check_interval` (default 7 days): `restic check`.
- `check_read_data_interval` (default 30 days): `restic check
  --read-data-subset=<check_read_data_percent>%` (default 5%), so over months
  all data gets read back.
- Failures are logged as errors and to the Windows Event Log.

## 9. Logging and reporting

- Structured logs (`log/slog`), text format in files, rotation by size and age.
- Errors and warnings also go to the Windows Event Log, source `Ouroboros`.
- `ouroboros status` shows: per job last success/failure, next due time,
  snapshot count, oldest snapshot date (history depth), partition usage,
  repository size, predicted next growth.
- Exit codes: `0` ok, `1` error, `2` config error, `3` completed with warnings,
  `4` could not reclaim enough space.

## 10. Configuration reference

See `ouroboros.example.toml`. Sizes accept `B, KB, MB, GB, TB` (powers of
1024) or `%` where documented. Durations accept Go syntax plus `d` (days):
`"30m"`, `"6h"`, `"7d"`.

Validation rejects: overlapping job names, empty paths, relative paths,
a repository path inside a backed-up path, `min_free` ≥ partition size,
unknown keys. All problems are reported together.

Further rules:

- `repository.path` must be a folder, not the root of a drive or UNC share
  (the ballast file lives next to it, see §3.8).
- At least one `[[job]]` is required. Each job needs an `interval`; there is
  no default. `vss` defaults to `true` when omitted.
- Job names may contain only letters, digits, `.`, `_` and `-`, and must start
  with a letter or digit, because they become restic tags (`job:<name>`).
  Names are compared case-insensitively when checking for duplicates.
- `keep_min_snapshots` ≥ 1, so the newest snapshot of a job is never deleted.
- `growth_factor` ≥ 1.0; `first_run_factor` > 0.
- `tick` ≥ 1 minute (the Task Scheduler minimum repetition interval).
- `pack_size_mb` is 0 (from profile) or between 4 and 128 (restic limits).
- Percentages are accepted only for `min_free` and `prune_headroom`;
  `max_repo_size` and `reserve_file_size` must be absolute.
- The configuration file may be UTF-8 (with or without BOM) or UTF-16 with
  BOM, which is what Windows PowerShell 5.1 writes when redirecting output.

`config validate` exits with code 3 (warning) when the repository partition
cannot be reached, because the `min_free` check against the partition size is
then skipped.

## 11. Testing strategy

- **Unit**: planner, candidate selection, predictor, config parsing, restic JSON
  parsing (with recorded fixtures), state persistence. Fake `Backend` and fake
  `FreeSpaceProvider`.
- **Simulation**: a fake backend that models a repository with shared chunks
  and a fixed-size disk; run hundreds of simulated days with random data churn
  and assert invariants (see below).
- **Integration**: real restic on temporary directories (skipped if restic is
  not installed).
- **End to end (manual, documented)**: small VHDX mounted as a drive letter to
  test real disk-full behaviour.

Invariants that must hold in every test:

1. Free space after any completed run ≥ `min_free`, unless reclaim reported
   failure.
2. Protected snapshots (min count, recent, pinned) are never deleted.
3. Deletion order is strictly oldest first among candidates.
4. No restic command runs concurrently with another on the same repository.
5. The repository passes `restic check` after any simulated interruption.

## 12. Graphical interface and installer

The goal is a program a non-technical user can download and use without a
terminal, a text editor or a separate restic installation.

Status: draft. The open questions in §12.8 are settled in ROADMAP phase M8.0,
before any code for the window is written.

### 12.1 What the user sees

1. They download one file, `Ouroboros.exe`, and double-click it. Windows asks
   for administrator rights (§12.3).
2. The first time, a guided setup asks three things:
   - where to store the backups: a list of drives with their free space, then
     a folder on the chosen one (default `<drive>:\Ouroboros`); the system
     drive is shown with a warning;
   - what to back up: Documents, Desktop and Pictures of the user who opened
     the program, preselected, plus any folder added with the Windows folder
     picker;
   - the repository password, twice, with the reminder to write it down.
3. Setup installs the program (§12.4), writes the configuration, creates the
   repository, installs the scheduled task and starts the first backup.
4. Later, double-clicking `Ouroboros.exe` or the Start menu entry opens the
   main window:
   - per job: last result, next run, number of snapshots, a "Back up now"
     button;
   - the backup partition: used, free, the `min_free` margin, history depth;
   - recent warnings and errors from the history;
   - automatic backups on or paused.
5. Jobs can be added, edited (folders, excludes, interval, VSS) and removed.
   Advanced settings (space margins, retention, performance) sit on a separate
   page with the defaults shown.
6. Restore: choose a job and a snapshot by date, browse its folders, tick
   files and folders, choose a target folder (default: a new folder on the
   desktop) and restore. Restoring over the original location needs an extra
   confirmation.
7. Uninstall from the window or from Windows "Installed apps".

The scheduled task keeps doing all the work; closing the window changes
nothing. The command line stays available for scripts and advanced users.

### 12.2 One download, two programs

A Windows executable is either a console or a windowed program. A console
program opens a terminal window when double-clicked; a windowed program
cannot be used properly from a terminal (the shell does not wait for it and
it cannot read a password from the console). Therefore:

- `ouroboros.exe` is the command line, a console program, as today. The
  scheduled task runs it.
- `Ouroboros.exe` is a windowed program: the window, the setup and the
  uninstaller. It embeds `ouroboros.exe` and `restic.exe` and writes them out
  during setup. It never runs restic itself: every action that touches the
  repository runs the installed `ouroboros.exe` with the same commands a user
  would type, with `--json` output for the window to read. One code path,
  already tested, does the work.

The release publishes `Ouroboros.exe` (the download for users) and the zip of
the command line alone, with SHA-256 checksums for both.

restic is the official release binary of a pinned version, downloaded by the
release workflow and checked against a SHA-256 recorded in the repository. Its
BSD-2-Clause license is installed next to it. A development build embeds no
restic and uses `restic_path` or `PATH` as today.

### 12.3 Privileges

`Ouroboros.exe` carries a manifest that requires administrator rights, so
Windows asks once when the window opens, as for disk and backup tools. The
password stays readable only by administrators and SYSTEM (§6). There is no
service and no communication channel to secure.

Because the window runs elevated, "the user who opened the program" (§12.1)
is taken from the Explorer process of the session, not from the elevated
token, so that Documents and Desktop are the right ones when another
administrator account approved the prompt.

### 12.4 Installation

- Files: `C:\Program Files\Ouroboros\` holds `Ouroboros.exe`,
  `ouroboros.exe`, `restic.exe` and the licenses. The folder keeps the
  inherited Program Files permissions, writable only by administrators.
- Configuration, password, state and logs: `C:\ProgramData\Ouroboros\`, as
  today, with the folder restricted to SYSTEM and Administrators (§6).
- The configuration sets `restic_path` to the installed `restic.exe`, so the
  task never depends on `PATH`.
- Start menu shortcut to `Ouroboros.exe`, and an entry in "Installed apps"
  (registry `Uninstall` key) whose uninstall command is
  `Ouroboros.exe uninstall`.
- The installation folder is added to the machine `PATH`, so `ouroboros` works
  in a new terminal.
- Running a newer `Ouroboros.exe` updates the installed files and keeps the
  configuration and the repository. An older one refuses.
- Uninstall removes the task, the Event Log source, the files, the shortcut,
  the `PATH` entry and the registry key. The configuration and the password
  are removed only if the user ticks that option; the repository and its
  backups are never removed by uninstall.

### 12.5 Editing the configuration

The window reads and writes `ouroboros.toml` through the `config` package,
validating before saving; an invalid change is not saved. Saving rewrites the
file in the format of `config init`, so comments written by hand are lost:
the window says so before saving a file that it did not write itself.

Removing a job leaves its snapshots in the repository. They are ordinary
deletion candidates (§3.5), except the newest `keep_min_snapshots` of the job,
which stay protected until the user deletes them. The window offers "Delete
this job's backups": the space manager forgets them and prunes, after showing
the list. This is an explicit request, so the protections for the newest and
recent snapshots do not apply; pinned snapshots are kept unless unpinned
first.

Pausing automatic backups disables the scheduled task; resuming enables it.

### 12.6 Technology

- Window: WebView2 through `github.com/jchv/go-webview2` (pure Go, no cgo),
  pinned to a commit. If the WebView2 runtime is missing, the program says so
  and links to Microsoft's installer.
- Interface: HTML, CSS and plain JavaScript embedded in the executable; no
  build step, no framework, no network access. The page talks to Go only
  through bound functions and loads nothing from outside the executable.
- Folder pickers and confirmations use the native Windows dialogs.

### 12.7 Testing

- The bridge between the page and the command line is plain Go and tested
  like the rest, with a fake command runner.
- Setup and uninstall take their target folders and registry root as
  parameters, so tests run them in temporary directories and a test registry
  key.
- The window itself, the UAC prompt and the real installation are checked by
  hand, with a procedure in `docs/TESTING.md`.

### 12.8 Open questions

1. Distribution. Settled:
   - SmartScreen keys reputation to each file hash for unsigned files, so
     every new unsigned release starts with the "Windows protected your PC"
     warning, which the user can pass with "More info, Run anyway". Smart
     App Control, when enabled on Windows 11, blocks unsigned files without
     reputation and offers no way past. Signing lets reputation carry over
     between versions; EV certificates no longer skip the warning.
   - 0.2.0 ships unsigned. The README shows the SmartScreen warning and how
     to pass it, and says that Smart App Control blocks the program.
   - `Ouroboros.exe` and `ouroboros.exe` carry a manifest and version
     information; each release is submitted to Microsoft's false-positive
     portal.
   - In parallel the owner applies to SignPath Foundation for free signing
     of open source releases (the publisher then shows as "SignPath
     Foundation"). Whether an executable embedding the unsigned official
     `restic.exe` is acceptable is asked to SignPath first; if not, setup
     downloads the official restic release and checks its SHA-256 instead of
     embedding it.
   - Whether embedding executables and requiring elevation raises antivirus
     detections is measured on the prototype (question 3).
2. WebView2: where its user data folder goes for an elevated process (by
   default it is created next to the executable, in Program Files); how
   developer tools, the context menu, navigation and remote content are
   disabled; what happens on systems without the runtime.
3. Packaging: which tool embeds the manifest, icon and version information
   (a new build-time dependency), and the size of the final download.
4. Uninstall: how `Ouroboros.exe` removes itself, and how an update replaces
   a running executable.
5. The interactive user's known folders from an elevated process (§12.3).
6. Interface language: English only, or English and Italian.
7. The bridge contract: every call the page can make, with arguments,
   results and errors.
8. Progress and concurrency: how "Back up now" reports progress and behaves
   when the scheduled task is already running.
9. Deleting a removed job's snapshots (§12.5): the agent rules allow
   deletions only through the planner with the §3.5 protections. Allowed as
   an explicit exception, not offered, or limited to what the planner could
   delete anyway.
10. Wireframes of every screen, approved before the interface is built.
