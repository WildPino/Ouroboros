# Restoring files

Backups are ordinary [restic](https://restic.net) snapshots. They can be
restored with Ouroboros on the machine that made them, or with plain restic on
any computer, as long as you have the repository and its password.

All commands below need an Administrator console: only administrators can read
the password that Ouroboros stores.

## With Ouroboros

### 1. Find the snapshot

```powershell
ouroboros snapshots                 # all snapshots, oldest first
ouroboros snapshots --job documents # only one job
```

```
ID        Time                 Job        Pinned  Paths
3f2a9c1e  2026-03-01 18:00:12  documents          C:\Users\me\Documents
8b71d0aa  2026-03-02 00:00:09  documents  pinned  C:\Users\me\Documents
2 snapshots
```

A snapshot is named by its ID, or by the first characters of it (at least 4)
as long as no other snapshot starts the same way.

### 2. Restore

```powershell
# The whole snapshot into a new, empty folder
ouroboros restore 8b71d0aa --target D:\restore

# Only some files or folders
ouroboros restore 8b71d0aa --target D:\restore --include 'C:\Users\me\Documents\taxes'
ouroboros restore 8b71d0aa --target D:\restore --include '*.xlsx'
```

`--include` takes a full path, as above, or a pattern such as `*.xlsx` or
`taxes\2025`, which matches anywhere in the path. Letter case does not matter.
It can be repeated. A restore whose patterns match nothing fails instead of
restoring an empty folder.

Files keep their full original path below the target folder:
`C:\Users\me\Documents\a.txt` restored to `D:\restore` becomes
`D:\restore\C\Users\me\Documents\a.txt`. Copy them back where you want them
once you have checked them.

A target folder that already contains files is refused, so a restore never
mixes old and new files by accident. `--overwrite` allows it, and then files
with the same name are replaced. Restoring straight over the original folders
is possible this way, but restoring elsewhere first is safer.

The target cannot be inside the repository, and the repository cannot be
inside the target.

### Keeping a snapshot

Before a long investigation, protect the snapshot from automatic deletion:

```powershell
ouroboros pin 8b71d0aa
# ... later
ouroboros unpin 8b71d0aa
```

Pinned snapshots are never removed to make room, even when the partition is
full.

## With plain restic

Use this on another computer, after a reinstall of Windows, or if Ouroboros
does not start. Install restic 0.17 or later (`winget install restic.restic`)
and make the repository reachable, for example by connecting the backup disk.

### The password

You need the repository password you wrote down when you created it.

On the original machine, while the configuration folder is intact, Ouroboros
can still supply it from an Administrator console:

```powershell
$env:RESTIC_PASSWORD_COMMAND = '"C:\Program Files\Ouroboros\bin\ouroboros.exe" internal password'
```

Use the path where `ouroboros.exe` is installed: `Ouroboros.exe` puts it in
`C:\Program Files\Ouroboros\bin`, while version 0.1.0 was copied by hand,
usually straight into `C:\Program Files\Ouroboros`. Add
`--config <path>` after `password` if the configuration is not in
`C:\ProgramData\Ouroboros\ouroboros.toml`. The stored copy of the password is
encrypted for that Windows installation only: after a reinstall, or on
another computer, only the written-down password works.

Without the password the backups cannot be decrypted, by anyone.

### Restore

Replace `B:\ouroboros` with the repository path (`[repository] path` in the
configuration). restic asks for the password unless it can get it as shown
above.

```powershell
$env:RESTIC_REPOSITORY = 'B:\ouroboros'

restic snapshots                        # all snapshots
restic snapshots --tag job:documents    # only one job

restic restore 8b71d0aa --target D:\restore
restic restore latest --tag job:documents --target D:\restore

# Browse one snapshot without restoring it
restic ls 8b71d0aa /C/Users/me/Documents

# Only some files or folders
restic restore 8b71d0aa --target D:\restore --iinclude /C/Users/me/Documents/taxes

# One file to standard output
restic dump 8b71d0aa /C/Users/me/Documents/a.txt > D:\a.txt
```

Inside a snapshot, restic names Windows paths as `/C/Users/me/...`: drive
letter without the colon, forward slashes. Write paths for `--include`, `ls`
and `dump` this way. A pattern written as `C:\Users\me\...` matches nothing,
and restic still reports success. `--include` is case-sensitive, `--iinclude`
is not.

Ouroboros tags every snapshot with `ouroboros` and `job:<name>`, and pinned
snapshots also with `ouroboros:pinned`.

While Ouroboros is still installed and scheduled, use plain restic only to read
and restore. Do not run `forget`, `prune` or `tag` by hand: the space planner
assumes it is the only one deleting snapshots.

## If the machine is lost

The repository is self-contained: a copy of the repository folder and the
password are all you need. On a new computer, install restic, attach the
disk or copy the folder, and follow "With plain restic".

Ouroboros 0.1.0 cannot take over an existing repository: `ouroboros init`
only creates new ones. To back up the new computer with Ouroboros, create a
new repository, after restoring what you need from the old one.
