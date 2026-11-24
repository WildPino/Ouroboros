# Ouroboros

**Space-aware, set-and-forget versioned backups for Windows.**

Give Ouroboros a dedicated partition and a list of folders or volumes. It takes
deduplicated, compressed, encrypted snapshots on a schedule and keeps as much
history as the partition can hold: when space runs low, the oldest snapshots are
removed automatically, never the newest ones.

Like the serpent eating its own tail, the backup history consumes its oldest
part to make room for the new.

> **Status:** pre-alpha, under active development. Do not rely on it as your
> only backup yet.

---

## Why

Mature backup engines such as [restic](https://restic.net) already handle
deduplication, compression and encryption very well. What they do not do is
manage a fixed amount of disk space: retention is always expressed as *time* or
*count* ("keep 7 daily, 4 weekly"), so a growing dataset eventually fills the
disk and backups start failing.

Ouroboros adds the missing layer:

- **Capacity-based retention**: keep everything that fits, drop the oldest
  first, always leave a configurable free-space margin.
- **Predictive check before each backup**: estimates how much the next snapshot
  will add and frees space *before* running out, not after.
- **Built-in scheduler**: one Windows scheduled task, with catch-up after the PC
  was off or asleep.
- **Safety rails**: minimum number of snapshots that are never deleted,
  protection window for recent snapshots, pinned snapshots, an emergency
  ballast file so cleanup can always run even on a full disk.

Ouroboros does **not** implement its own storage format. All data is written by
restic, so your backups stay readable with plain `restic` even without
Ouroboros.

## Features

| Feature | How |
|---|---|
| Incremental, versioned history | restic snapshots (content-defined chunking, deduplication across files, folders and versions) |
| Maximum lossless compression | restic repository format v2 with `--compression max` (zstd) |
| Encryption | restic (always on) |
| Folders and whole volumes | any path, including `E:\` |
| Open/locked files | Windows Volume Shadow Copy (VSS) |
| Automatic space management | Ouroboros space planner |
| Scheduling | Ouroboros tick via Windows Task Scheduler |
| Integrity checks | periodic `restic check`, with partial data verification |
| Tuning for large-RAM machines | memory profile adjusts read concurrency and pack size |

## How it works

```
 Task Scheduler (every 15 min)
          │
          ▼
   ouroboros run ──► which jobs are due? ──► none ──► exit
          │
          ▼  for each due job
   space planner: free space ≥ predicted growth + margin ?
          │ no                                   │ yes
          ▼                                      │
   reclaim: forget oldest eligible snapshots,    │
            prune, measure again                 │
          │                                      │
          ▼                                      ▼
   restic backup (VSS, max compression) ──► record stats ──► post-backup space check
```

See [docs/DESIGN.md](docs/DESIGN.md) for the full design.

## Requirements

- Windows 10 or 11 (x64)
- [restic](https://restic.net) 0.17 or later on `PATH` (or path set in config)
- Administrator rights (needed for VSS and for the scheduled task)
- A dedicated partition or drive for the repository (NTFS or ReFS)

## Quick start

```powershell
# 1. Install restic
winget install restic.restic

# 2. Create a config from the example and edit it
ouroboros config init > C:\ProgramData\Ouroboros\ouroboros.toml
notepad C:\ProgramData\Ouroboros\ouroboros.toml

# 3. Check the environment
ouroboros doctor

# 4. Create the repository (asks for a password, stores it encrypted for this machine)
ouroboros init

# 5. First backup, interactive
ouroboros run --force

# 6. Install the scheduled task
ouroboros install
```

**Write down the repository password and keep it somewhere else.** Without it
the backups cannot be decrypted, by Ouroboros or by anyone.

## Configuration

Minimal example (see [`ouroboros.example.toml`](ouroboros.example.toml) for
every option):

```toml
[repository]
path = 'B:\ouroboros'
compression = "max"

[space]
min_free = "10%"
keep_min_snapshots = 3

[[job]]
name = "documents"
paths = ['C:\Users\me\Documents', 'D:\Projects']
exclude = ['**\node_modules', '**\*.tmp']
interval = "6h"
vss = true
```

## Commands

| Command | Description |
|---|---|
| `ouroboros init` | Create the repository and store its password |
| `ouroboros run [--job NAME] [--force]` | Run due jobs (or the named job immediately) |
| `ouroboros status` | Jobs, last results, space usage, history depth |
| `ouroboros snapshots [--job NAME]` | List snapshots |
| `ouroboros restore SNAPSHOT --target DIR [--include PATTERN] [--overwrite]` | Restore files (into an empty folder unless `--overwrite`) |
| `ouroboros reclaim --dry-run` | Show what would be deleted to reach the target free space |
| `ouroboros pin SNAPSHOT` / `unpin` | Protect a snapshot from automatic deletion |
| `ouroboros check [--read-data PERCENT]` | Verify repository integrity |
| `ouroboros install` / `uninstall` | Register or remove the scheduled task |
| `ouroboros doctor` | Check restic version, permissions, VSS, config, free space |
| `ouroboros config init` / `validate` | Print an example config / validate the current one |

`snapshots`, `restore`, `pin`, `unpin`, `check`, `run` and `init` need an
Administrator console, because only administrators can read the stored password.
`pin`, `unpin` and `check` refuse to run while a backup is in progress.

Logs are written to `%ProgramData%\Ouroboros\logs\`; warnings and errors also
go to the Windows Event Log (Application, source `Ouroboros`).

Exit codes: `0` success, `1` error, `2` configuration error, `3` completed with
warnings, `4` a backup was skipped because not enough space could be freed.

## FAQ

**Why restic and not Borg?** Borg does not run natively on Windows. restic is a
single native executable, supports VSS, and its `prune` reclaims space
immediately, which capacity-based retention depends on.

**Does it use a lot of RAM?** Not by default. On machines with plenty of memory
the `high` memory profile raises read concurrency and pack size, which mostly
helps with many small files and fast disks. Compression ratio does not improve
meaningfully with more RAM once data is deduplicated.

**Is one local partition enough?** It protects against deletion, corruption and
accidental changes, not against losing the whole machine. Keep an off-site
copy as well (see the roadmap: repository mirroring).

## Roadmap

See [docs/ROADMAP.md](docs/ROADMAP.md).

## License

MIT (see [LICENSE](LICENSE)).
