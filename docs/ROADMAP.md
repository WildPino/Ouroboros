# Roadmap

Milestones are done in order. Each task is done when its code is merged, it has
tests, `go vet` / `staticcheck` / `go test ./...` pass, and docs are updated if
behaviour changed.

## M0 — Project setup

- [x] Choose the license (default: MIT) and add `LICENSE`
- [ ] `go mod init github.com/<owner>/ouroboros`, Go version pinned in `go.mod`
- [ ] Directory layout as in DESIGN.md §2
- [ ] `.gitignore` (build output, `dist/`, test artifacts, `*.ballast`)
- [ ] `.editorconfig`, `gofmt` enforced
- [ ] `Makefile` or `build.ps1` with targets: build, test, lint, release-snapshot
- [ ] GitHub Actions on `windows-latest`: vet, staticcheck, test, build
- [ ] Version injection at build time (`-ldflags -X main.version=...`)

## M1 — Config and CLI skeleton

- [ ] CLI framework (standard `flag` with subcommands, or `spf13/cobra`)
- [ ] `version`, `config init`, `config validate`
- [ ] Config types with `Size` (bytes or percent) and `Duration` (with `d`) parsing
- [ ] Defaults and validation rules (DESIGN.md §10), unknown keys rejected
- [ ] Unit tests for every validation rule
- [ ] `doctor`: config, admin rights, restic present and ≥ 0.17, repo path writable,
      partition free space, VSS service available

## M2 — Secrets and restic backend

- [ ] `internal/secret`: DPAPI machine-scope encrypt/decrypt, ACL on `secret.bin`
- [ ] Hidden `internal password` subcommand for `--password-command`
- [ ] Process runner: job object, timeout, priority, streaming stdout/stderr
- [ ] restic: `Version`, `Init` (format v2), `Snapshots`, `Forget`, `Prune`, `Check`,
      `Restore`, `Tag`
- [ ] restic: `Backup` with JSON progress/summary parsing, VSS flag, tags, excludes
- [ ] Exit code → typed error mapping (exit 3 = warning)
- [ ] Recorded JSON fixtures + parser tests
- [ ] Integration tests with real restic in temp dirs (skipped when unavailable)
- [ ] `init` command (password prompt twice, store secret, create repo, create ballast,
      print "record your password" warning)

## M3 — Space manager

- [ ] `FreeSpaceProvider` (Windows: `GetDiskFreeSpaceExW`) + fake
- [ ] Repository size walker
- [ ] Ballast create/delete/recreate
- [ ] Target computation (`min_free` absolute/percent, `prune_headroom`, `max_repo_size`)
- [ ] Growth predictor (history-based and first-run dry-run)
- [ ] Candidate selection (min per job, protection window, pinned)
- [ ] Reclaim loop with exponential batching (DESIGN.md §3.5)
- [ ] `reclaim --dry-run`
- [ ] Simulation test harness (fake repo with shared chunks + fixed disk) and
      invariant checks over long random runs
- [ ] Out-of-space recovery path (DESIGN.md §3.9)

## M4 — Scheduler, state, locking

- [ ] `state.json` atomic persistence, `history.jsonl` append/read
- [ ] Named mutex single-instance lock
- [ ] Due-job computation (interval, retry_after) with tests using a fake clock
- [ ] `run` orchestration: retention → space check → backup → post-check →
      maintenance (routine prune, check)
- [ ] `run --job NAME --force`
- [ ] `install` / `uninstall`: Task Scheduler task as SYSTEM via COM or `schtasks` XML
      (missed-run catch-up, battery setting)

## M5 — User-facing commands and logging

- [ ] `status` (DESIGN.md §9)
- [ ] `snapshots`, `restore`, `pin`, `unpin`, `check`
- [ ] Structured logging with rotation
- [ ] Windows Event Log source registration (in `install`) and error reporting
- [ ] Exit codes as specified

## M6 — Hardening

- [ ] Interrupt tests: kill during backup, during prune; repository still passes check
- [ ] Disk-full end-to-end test on a small VHDX (documented manual procedure in
      `docs/TESTING.md`)
- [ ] Locked-file test with VSS on and off
- [ ] Long paths (> 260 chars) and Unicode file names
- [ ] Clock changes / DST: due-time logic uses monotonic + wall time safely
- [ ] Memory profile `auto` detection and overrides

## M7 — Release v0.1.0

- [ ] `docs/TESTING.md`, `docs/RESTORE.md` (restore with Ouroboros and with plain restic)
- [ ] `CHANGELOG.md`
- [ ] Release workflow: tagged builds, zip with `ouroboros.exe`, example config,
      SHA-256 checksums
- [ ] README reviewed against actual behaviour
- [ ] Tag `v0.1.0`

## Later

- [ ] Notifications (Windows toast, e-mail, webhook)
- [ ] Repository mirroring to a second location (`restic copy`), local or remote
- [ ] Tray application with status and "back up now"
- [ ] Kopia backend
- [ ] Linux support (systemd timer, `statfs`, Borg backend option)
- [ ] `winget` package
