# Roadmap

Milestones are done in order. Each task is done when its code is merged, it has
tests, `go vet` / `staticcheck` / `go test ./...` pass, and docs are updated if
behaviour changed.

## M0 — Project setup

- [x] Choose the license (default: MIT) and add `LICENSE`
- [x] `go mod init github.com/<owner>/ouroboros`, Go version pinned in `go.mod`
- [x] Directory layout as in DESIGN.md §2
- [x] `.gitignore` (build output, `dist/`, test artifacts, `*.ballast`)
- [x] `.editorconfig`, `gofmt` enforced
- [x] `Makefile` or `build.ps1` with targets: build, test, lint, release-snapshot
- [x] GitHub Actions on `windows-latest`: vet, staticcheck, test, build
- [x] Version injection at build time (`-ldflags -X main.version=...`)

## M1 — Config and CLI skeleton

- [x] CLI framework (standard `flag` with subcommands, or `spf13/cobra`)
- [x] `version`, `config init`, `config validate`
- [x] Config types with `Size` (bytes or percent) and `Duration` (with `d`) parsing
- [x] Defaults and validation rules (DESIGN.md §10), unknown keys rejected
- [x] Unit tests for every validation rule
- [x] `doctor`: config, admin rights, restic present and ≥ 0.17, repo path writable,
      partition free space, VSS service available

## M2 — Secrets and restic backend

- [x] `internal/secret`: DPAPI machine-scope encrypt/decrypt, ACL on `secret.bin`
- [x] Hidden `internal password` subcommand for `--password-command`
- [x] Process runner: job object, timeout, priority, streaming stdout/stderr
- [x] restic: `Version`, `Init` (format v2), `Snapshots`, `Forget`, `Prune`, `Check`,
      `Restore`, `Tag`
- [x] restic: `Backup` with JSON progress/summary parsing, VSS flag, tags, excludes
- [x] Exit code → typed error mapping (exit 3 = warning)
- [x] Recorded JSON fixtures + parser tests
- [x] Integration tests with real restic in temp dirs (skipped when unavailable)
- [x] `init` command (password prompt twice, store secret, create repo, create ballast,
      print "record your password" warning)

## M3 — Space manager

- [x] `FreeSpaceProvider` (Windows: `GetDiskFreeSpaceExW`) + fake
- [x] Repository size walker
- [x] Ballast create/delete/recreate
- [x] Target computation (`min_free` absolute/percent, `prune_headroom`, `max_repo_size`)
- [x] Growth predictor (history-based and first-run dry-run)
- [x] Candidate selection (min per job, protection window, pinned)
- [x] Reclaim loop with exponential batching (DESIGN.md §3.5)
- [x] `reclaim --dry-run`
- [x] Simulation test harness (fake repo with shared chunks + fixed disk) and
      invariant checks over long random runs
- [x] Out-of-space recovery path (DESIGN.md §3.9)

## M4 — Scheduler, state, locking

- [x] `state.json` atomic persistence, `history.jsonl` append/read
- [x] Named mutex single-instance lock
- [x] Due-job computation (interval, retry_after) with tests using a fake clock
- [x] `run` orchestration: retention → space check → backup → post-check →
      maintenance (routine prune, check)
- [x] `run --job NAME --force`
- [x] `install` / `uninstall`: Task Scheduler task as SYSTEM via COM or `schtasks` XML
      (missed-run catch-up, battery setting)

## M5 — User-facing commands and logging

- [x] `status` (DESIGN.md §9)
- [x] `snapshots`, `restore`, `pin`, `unpin`, `check`
- [x] Structured logging with rotation
- [x] Windows Event Log source registration (in `install`) and error reporting
- [x] Exit codes as specified

## M6 — Hardening

- [x] Interrupt tests: kill during backup, during prune; repository still passes check
- [x] Disk-full end-to-end test on a small VHDX (documented manual procedure in
      `docs/TESTING.md`)
- [x] Locked-file test with VSS on and off
- [x] Long paths (> 260 chars) and Unicode file names
- [x] Clock changes / DST: due-time logic uses monotonic + wall time safely
- [x] Memory profile `auto` detection and overrides

## M7 — Release v0.1.0

- [x] `docs/TESTING.md`, `docs/RESTORE.md` (restore with Ouroboros and with plain restic)
- [x] `CHANGELOG.md`
- [x] Release workflow: tagged builds, zip with `ouroboros.exe`, example config,
      SHA-256 checksums
- [x] README reviewed against actual behaviour
- [x] Tag `v0.1.0`

## M8 — Graphical interface and installer (v0.2.0)

The goal and the user experience are in DESIGN.md §12. M8 is split into
phases done in order. Each phase starts by settling its open questions with
the owner (recorded in DESIGN.md §12) and ends when its "done when" holds.
No phase writes code for a later one.

### M8.0 — Study and specification (no product code)

Experiments live outside the repository; only their conclusions enter
DESIGN.md §12.

- [x] Distribution risks: SmartScreen on an unsigned download, antivirus
      reactions to an executable that embeds executables and asks for
      elevation; options (code signing, separate files) and their cost
- [ ] WebView2: runtime presence on Windows 10 and 11 (including LTSC), user
      data folder for an elevated process, disabling developer tools,
      context menu, navigation and remote content; behaviour when the
      runtime is missing
- [ ] Prototype window in a scratch module: one page, one bound function,
      native folder picker, elevated launch; measure start time and size
- [ ] Packaging: tool to embed the manifest, icon and version information
      (new build-time dependency), size of the final download
- [ ] Self-removal on uninstall and replacing a running executable on update
- [ ] Identifying the interactive user's known folders from an elevated
      process
- [ ] Interface language (English only, or English and Italian)
- [ ] Bridge contract: the list of calls the page can make, their arguments,
      results and errors; nothing else is reachable from the page
- [ ] Wireframes of every screen: setup steps, main window, job editor,
      settings, restore, uninstall
- [ ] Deleting a removed job's snapshots: allowed or not, and under which
      protections (the agent rules allow deletions only through the planner
      with the §3.5 protections)
- [ ] Owner sign-off on DESIGN.md §12 before M8.1

Done when: DESIGN.md §12 has no open question left and the owner approved it.

### M8.1 — Command line for machines

Everything the window needs exists first as a tested command, so the window
adds no logic of its own.

- [ ] Stable `--json` output for `status`, `snapshots`, `run`, `check`,
      `restore`, `doctor`, with documented schemas and recorded fixtures
- [ ] Progress events during `run` and `restore` (one JSON line per event)
- [ ] `ls SNAPSHOT [PATH]` to browse a snapshot one folder at a time
- [ ] Configuration editing commands or a library API used by both the
      window and tests: add, change and remove jobs and settings, validated
      before writing
- [ ] `pause` / `resume` of the scheduled task, shown by `status`
- [ ] Deleting a removed job's snapshots, as decided in M8.0
- [ ] `init` without a console prompt (password from the window), keeping
      the rule that the password never appears in arguments or environment

Done when: every action of DESIGN.md §12.1 can be done from the command line
with `--json`, and the tests cover each command and schema.

### M8.2 — Packaging

- [ ] `Ouroboros.exe`: windowed build with administrator manifest, icon and
      version information
- [ ] Embedded `ouroboros.exe` and `restic.exe`; restic pinned by version and
      SHA-256 in the repository, with its license
- [ ] Release workflow publishes `Ouroboros.exe`, the command line zip and
      checksums for both
- [ ] Development build without embedded restic

Done when: the release workflow produces `Ouroboros.exe`, and running it with
a hidden diagnostic argument prints the versions of the embedded programs.

### M8.3 — Install, update, uninstall (no window yet)

- [ ] `internal/setup` with target folders and registry root as parameters
- [ ] Install: Program Files files, ProgramData permissions, `restic_path`,
      Start menu shortcut, "Installed apps" entry, machine `PATH`
- [ ] Update over an older version; refusal over a newer one
- [ ] Uninstall, including self-removal; configuration and password only on
      request; never the repository
- [ ] Tests in temporary folders and a test registry key
- [ ] Manual procedure in `docs/TESTING.md`, run by the owner on a real
      machine

Done when: install, update and uninstall pass the tests and the owner's
manual run leaves no trace after uninstall.

### M8.4 — Window shell

- [ ] WebView2 host with the M8.0 hardening, embedded assets only
- [ ] Bridge implementing the M8.0 contract, calling `ouroboros.exe`; tested
      with a fake command runner
- [ ] Native folder picker, confirmation dialogs, missing-runtime message
- [ ] Single window: opening it again focuses the existing one

Done when: an empty window opens elevated, calls one command through the
bridge and shows its result, and the bridge rejects anything outside the
contract.

### M8.5 — First setup

- [ ] Three steps: backup drive and folder, folders to back up, password
- [ ] Runs install (M8.3), writes the configuration, `init`, task, first
      backup with progress

Done when: on a clean machine, download → double-click → three answers →
first backup finished, without a terminal (owner's manual run).

### M8.6 — Main window

- [ ] Jobs with last result, next run, snapshots; partition usage and margin;
      recent warnings and errors
- [ ] Back up now with progress; pause and resume; refresh while open

Done when: the window shows the same facts as `status`, and the actions work
while the scheduled task is also running.

### M8.7 — Jobs and settings

- [ ] Add, edit and remove jobs; advanced settings page
- [ ] Warning before overwriting a hand-edited configuration
- [ ] Deleting a removed job's snapshots, if allowed in M8.0

Done when: every configuration change made in the window passes
`config validate` and is used by the next run.

### M8.8 — Restore

- [ ] Pick job and snapshot by date, browse folders, select files and
      folders, choose the target, restore with progress
- [ ] Extra confirmation to restore over the original location

Done when: files chosen in the window are restored identical, checked by a
test and by the owner's manual run.

### M8.9 — Release v0.2.0

- [ ] README, `docs/RESTORE.md`, `docs/TESTING.md` and CHANGELOG for the
      window
- [ ] Full manual test by the owner on a machine without development tools
- [ ] Tag `v0.2.0`

## Later

- [ ] Notifications (Windows toast, e-mail, webhook)
- [ ] Repository mirroring to a second location (`restic copy`), local or remote
- [ ] Tray application with status and "back up now"
- [ ] Kopia backend
- [ ] Linux support (systemd timer, `statfs`, Borg backend option)
- [ ] `winget` package
