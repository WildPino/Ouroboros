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

## Builds of Ouroboros.exe

```powershell
.\build.ps1 build             # bin\ouroboros.exe, and bin\app\Ouroboros.exe without restic
.\build.ps1 release-snapshot  # dist\: the command line zip, Ouroboros.exe with restic, checksums
```

`release-snapshot` downloads the restic release pinned in
`third_party/restic/restic.json` and stops if its SHA-256 differs. Both
targets then run `Ouroboros.exe --embedded-versions`, a hidden argument that
checks every embedded file against its recorded size and SHA-256 and prints
its version; the build fails if any file does not match. To run it by hand,
capture the output, since a windowed program has no console:

```powershell
$env:__COMPAT_LAYER = 'RunAsInvoker'   # only from a console that is not elevated
.\dist\Ouroboros.exe --embedded-versions | Out-String
```

## Install, update and uninstall by hand

The tests of `internal/setup` run setup in temporary folders and a registry
key below `HKEY_CURRENT_USER`. The real installation, with the UAC prompt,
Program Files, the machine `PATH` and "Installed apps", is checked by hand.
Use a virtual machine, Windows Sandbox or a spare computer, never the
computer whose backups matter, and a test repository on a test drive or
folder.

Build two versions from two different commits, so that the second is newer:

```powershell
.\build.ps1 release-snapshot; Copy-Item dist\Ouroboros.exe Ouroboros-old.exe
# commit something, then
.\build.ps1 release-snapshot; Copy-Item dist\Ouroboros.exe Ouroboros-new.exe
```

Copy both to the test machine. The command lines below run in an
Administrator PowerShell unless they say otherwise.

1. **Install.** Double-click `Ouroboros-old.exe` from Explorer: Windows asks
   for administrator rights and shows the icon; the message says the window
   is not part of this build. Then run `.\Ouroboros-old.exe install` and
   confirm. Check:
   - `C:\Program Files\Ouroboros` holds `Ouroboros.exe`, `LICENSE`,
     `bin\ouroboros.exe`, `restic\restic.exe` and `restic\LICENSE`, and
     nothing ending in `.old` or `.new`;
   - the Start menu has "Ouroboros", which opens the same message;
   - Settings, Apps, "Installed apps" lists Ouroboros with its version,
     the publisher "SirProjects" and its size;
   - in a new terminal that is not elevated, `ouroboros version` prints the
     version, `where.exe ouroboros` finds only `bin\ouroboros.exe`, and
     `where.exe restic` does not list the Ouroboros folder;
   - `icacls C:\ProgramData\Ouroboros` lists only `NT AUTHORITY\SYSTEM` and
     `BUILTIN\Administrators`.
2. **Configure and schedule.** Create the configuration with
   `ouroboros config init > C:\ProgramData\Ouroboros\ouroboros.toml`, set the
   test repository and one job in it, and run
   `ouroboros config validate`. Run `.\Ouroboros-old.exe install` again: the
   same version repairs the installation, and because the configuration was
   edited by hand and has no `restic_path`, the final message asks to set
   `restic_path = 'C:\Program Files\Ouroboros\restic\restic.exe'`, without
   changing the file. Set it, then run `ouroboros init`,
   `ouroboros run --force` and `ouroboros install`. `ouroboros status` shows
   the backup and the task.
3. **Update while a backup runs.** Start `ouroboros run --force` in one
   terminal and, while it runs, `.\Ouroboros-new.exe install` in another:
   setup asks to retry until the backup ends, then updates. Check that the
   message mentions the scheduled task, that `ouroboros version` prints the
   new version, that "Installed apps" shows it, and that
   `schtasks /Query /TN \Ouroboros\Tick /XML` still runs
   `C:\Program Files\Ouroboros\bin\ouroboros.exe`.
4. **Update while the installed program runs.** Open "Ouroboros" from the
   Start menu and leave its message open; run `.\Ouroboros-new.exe install`
   again. The message says that replaced files are deleted at the next
   restart; `Ouroboros.exe.old` is in the program folder until then, and is
   gone after a restart.
5. **Refuse an older version.** `.\Ouroboros-old.exe install` must refuse,
   saying that a newer version is installed, and change nothing.
6. **Pause survives an update.** `ouroboros pause`, then
   `.\Ouroboros-new.exe install`: `ouroboros status` still says paused. Run
   `ouroboros resume`.
7. **Uninstall, keeping the data.** From Settings, Apps, "Installed apps",
   uninstall Ouroboros: Windows asks for administrator rights; answer Yes to
   removing Ouroboros and No to removing the configuration. Check that the
   task is gone (`schtasks /Query /TN \Ouroboros\Tick` fails), the Start menu
   entry is gone, a new terminal no longer finds `ouroboros`, and
   `C:\ProgramData\Ouroboros` and the test repository are still there. The
   message says that some files go at the next restart; restart, and
   `C:\Program Files\Ouroboros` is gone.
8. **Reinstall, then uninstall everything.** `.\Ouroboros-new.exe install`;
   the configuration is used again (`ouroboros status`). Uninstall with
   `& 'C:\Program Files\Ouroboros\Ouroboros.exe' uninstall`, answering Yes
   twice, and restart. Nothing may be left:
   - `C:\Program Files\Ouroboros` and `C:\ProgramData\Ouroboros`;
   - the key
     `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\Ouroboros`;
   - the `bin` folder in the machine `PATH`
     (`[Environment]::GetEnvironmentVariable('Path', 'Machine')`);
   - the Start menu entry and the task;
   - the Event Log source
     `HKLM\SYSTEM\CurrentControlSet\Services\EventLog\Application\Ouroboros`.

   The test repository must still be there, with its snapshots
   (`restic -r <repository> snapshots`, with the written-down password).
9. **Version 0.1.0 installed by hand.** On a machine where version 0.1.0 runs
   from `C:\Program Files\Ouroboros\ouroboros.exe` with its scheduled task,
   run `.\Ouroboros-new.exe install`. The old `ouroboros.exe` is replaced by
   `Ouroboros.exe` (the same name for Windows), the message says that the
   scheduled task now runs the installed command line, and the task XML
   names `bin\ouroboros.exe`. The next scheduled run succeeds
   (`ouroboros status`).
