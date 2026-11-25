# Testing

All tests work only in temporary directories. None of them touches real
repositories, real backup partitions or the scheduled task.

## Normal tests

```powershell
.\build.ps1 test          # go test ./...
.\build.ps1 race          # with the race detector
go test -short ./...      # skip the tests that run the real restic
```

Integration tests run the real `restic` (0.17 or later, found on `PATH`)
against repositories in temporary directories, and are skipped when restic is
not installed. They cover, among others:

- backup, snapshots, forget, prune, check, restore (also of parts selected
  by Windows paths) and tag;
- restic killed in the middle of backups and prunes, as a crash or a power
  loss would: the repository must still pass a full `check --read-data`, and
  the stale lock left behind must not block later operations;
- paths longer than 260 characters and non-ASCII names (accented Latin, Greek,
  Cyrillic, CJK, characters outside the Basic Multilingual Plane);
- `init`, `run`, `snapshots`, `pin`, `unpin`, `check`, `restore` and `status`
  end to end through the command line code.

The space manager has a simulation (`internal/space/sim_test.go`) that runs
long random histories against a fake repository with shared chunks and a
fixed disk, and checks the invariants of DESIGN.md §11 after every step.
`OUROBOROS_SIM_SEEDS=200 go test -run Simulation ./internal/space/` runs a
longer simulation than the default.

## Tests that need an elevated console

If PowerShell refuses to run the scripts below because of the execution
policy, start them with
`powershell -ExecutionPolicy Bypass -File .\scripts\<name>.ps1 <arguments>`.

Tests whose name contains `Admin` use system facilities, such as Volume
Shadow Copies, and run only with `OUROBOROS_ADMIN_TESTS=1` in an elevated
console. Otherwise they are skipped.

```powershell
# In a PowerShell started with "Run as administrator", from the repository root
.\scripts\admin-tests.ps1
```

The output is saved to `test-results\admin-tests.log`. Covered:

- **Locked files and VSS** (`TestAdminLockedFileVSS`): a file held open with
  no sharing, as Outlook does with its data files. Without VSS the snapshot is
  marked incomplete and names the file; with VSS the file is read from a
  shadow copy and restored intact. This creates a temporary shadow copy of the
  volume that holds the temporary directory.

## Disk-full test on a virtual disk

`scripts\diskfull-test.ps1` checks the whole space management on a real, small
NTFS volume: reclaiming space, recovery after a backup that runs out of space
(DESIGN.md §3.9), and the behaviour when not enough space can be freed. It
needs an elevated console and about 8 GB free on the drive that holds the
working folder.

```powershell
# In a PowerShell started with "Run as administrator", from the repository root
.\scripts\diskfull-test.ps1 -WorkDir D:\OuroborosDiskTest
```

The working folder must not exist or must be empty. The script:

1. builds `ouroboros.exe` into the working folder;
2. creates an expandable VHDX (`-SizeMB`, default 8192) with `diskpart`,
   formats it as NTFS and mounts it in a folder, without a drive letter;
3. writes a test configuration (min_free 300 MB, ballast 200 MB,
   `keep_min_snapshots = 1`, Event Log off) and runs `ouroboros init`, which
   asks for a test password;
4. runs `ouroboros run --force` for `-Iterations` rounds (default 14), adding
   `-ChunkMB` (default 300) of random data each round and keeping the last
   three rounds in the source folder;
5. halfway, another file leaves only the free space that the prediction asks
   for, and the round adds much more data than predicted: the backup runs out
   of space and Ouroboros must recover and retry;
6. near the end, another file fills the disk, so the reclaim may report that
   it cannot free enough (exit code 4);
7. verifies all the data with `ouroboros check --read-data 100`;
8. detaches the VHDX and deletes the working folder (`-Keep` keeps both).

It fails if an exit code other than 0, 3 or 4 appears (1 is accepted only in
the round that runs out of space, when the retry fails too), if the free
space stays below min_free without exit code 4, if a round takes more than 5
minutes (a backup stuck on a full disk was not stopped), if the final check
fails or if no reclaim happened. The output is saved to `test-results\diskfull-test.log`.

If the script is interrupted, detach the disk by hand: open `diskpart`, then
`select vdisk file="<WorkDir>\disk.vhdx"` and `detach vdisk`.
