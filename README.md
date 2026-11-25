# Ouroboros

**Space-aware, set-and-forget versioned backups for Windows.**

Give Ouroboros a dedicated partition and a list of folders or volumes. It takes
deduplicated, compressed, encrypted snapshots on a schedule and keeps as much
history as the partition can hold: when space runs low, the oldest snapshots are
removed automatically, never the newest ones.

Like the serpent eating its own tail, the backup history consumes its oldest
part to make room for the new.

> **Status:** 0.1.0, the first release. Run it alongside your existing backup
> until it has proven itself on your machine.

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
- [restic](https://restic.net) 0.17 or later, on the system `PATH` or set
  with `restic_path` in the configuration (the scheduled task runs as
  `SYSTEM` and does not see a `PATH` set for one user)
- Administrator rights (needed for VSS and for the scheduled task)
- A dedicated partition or drive for the repository (NTFS or ReFS)

## Quick start

Download the zip of the latest release, check it against `checksums.txt`
(`Get-FileHash ouroboros_*.zip`), and extract `ouroboros.exe` into a folder
that only administrators can change, such as `C:\Program Files\Ouroboros`. The
scheduled task runs it as `SYSTEM`, so `install` refuses an executable in a
temporary folder and warns when other users could replace it.

In an Administrator PowerShell:

```powershell
# 1. Install restic for all users, so that the scheduled task finds it
winget install restic.restic --scope machine

# 2. Create a config from the example and edit it
New-Item -ItemType Directory -Force C:\ProgramData\Ouroboros
& 'C:\Program Files\Ouroboros\ouroboros.exe' config init > C:\ProgramData\Ouroboros\ouroboros.toml
notepad C:\ProgramData\Ouroboros\ouroboros.toml

# 3. Check the environment
& 'C:\Program Files\Ouroboros\ouroboros.exe' doctor

# 4. Create the repository (asks for a password, stores it encrypted for this machine)
& 'C:\Program Files\Ouroboros\ouroboros.exe' init

# 5. First backup, interactive
& 'C:\Program Files\Ouroboros\ouroboros.exe' run --force

# 6. Install the scheduled task
& 'C:\Program Files\Ouroboros\ouroboros.exe' install
```

The examples below write just `ouroboros`; add the folder to `PATH` to do the
same. Instead of installing restic for all users, you can put `restic.exe` in a
folder only administrators can change and set `restic_path` in the
configuration; `doctor` tells whether the scheduled task will find restic.

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
| `ouroboros init [--password-stdin]` | Create the repository and store its password, asked on the console or read from standard input |
| `ouroboros run [--job NAME] [--force]` | Run the due jobs, or only the named one; `--force` runs them even if not due |
| `ouroboros status` | Jobs, last results, space usage, history depth |
| `ouroboros snapshots [--job NAME]` | List snapshots |
| `ouroboros ls SNAPSHOT [FOLDER]` | List one folder of a snapshot, or its drives |
| `ouroboros restore SNAPSHOT --target DIR [--include PATH] [--overwrite]` | Restore files (into an empty folder unless `--overwrite`) |
| `ouroboros reclaim --dry-run` | Show what would be deleted to reach the target free space |
| `ouroboros forget-job NAME --dry-run` / `--confirm NAME` | List, then delete, the backups of a job removed from the configuration |
| `ouroboros pin SNAPSHOT` / `unpin` | Protect a snapshot from automatic deletion |
| `ouroboros check [--read-data PERCENT]` | Verify repository integrity |
| `ouroboros install` / `uninstall` | Register or remove the scheduled task |
| `ouroboros pause` / `resume` | Stop and restart automatic backups, keeping the task |
| `ouroboros doctor` | Check restic version, permissions, VSS, config, free space |
| `ouroboros config init` / `validate` | Print an example config / validate the current one |

All commands except `doctor`, `config` and `version` need an Administrator
console: only administrators can read the stored password, and VSS and the
scheduled task need the rights too. Without them, `status` shows only what it
can read. `pin`, `unpin` and `check` refuse to run while a backup is in
progress.

`init`, `status`, `snapshots`, `ls`, `run`, `restore`, `forget-job`, `check`,
`doctor`, `pause`, `resume` and `version` accept `--json` for machine-readable output, described in
[docs/JSON.md](docs/JSON.md).

`--config PATH` selects another configuration; the default is
`%ProgramData%\Ouroboros\ouroboros.toml`. The stored password, the state and
the logs live in the same folder. Logs are in its `logs` subfolder; warnings
and errors also go to the Windows Event Log (Application, source `Ouroboros`).

Restoring, with Ouroboros or with plain restic, is described in
[docs/RESTORE.md](docs/RESTORE.md).

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
