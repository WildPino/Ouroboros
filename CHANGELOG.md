# Changelog

All notable changes are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [0.1.0] - 2026-11-25

First release.

### Added

- Backups with restic 0.17 or later: one repository, several jobs with their
  own folders or volumes, excludes and interval; maximum compression; Volume
  Shadow Copies for open files.
- Capacity-based retention: before each backup the growth is predicted from
  the previous runs and the oldest snapshots are removed until it fits, keeping
  `min_free` and the room prune needs. Never below `keep_min_snapshots` per
  job, never the newest snapshot of a job, never snapshots newer than
  `protect_newer_than` or pinned.
- Optional time-based retention (`keep_daily`, `keep_weekly`, ...) and
  `max_repo_size`.
- Recovery when a backup fills the disk: a ballast file is released, the
  partial data pruned, older snapshots removed and the backup retried once. A
  backup that stays stuck on a full disk is stopped after a minute instead of
  waiting for restic's retries.
- Scheduling through one Windows scheduled task, running as `SYSTEM` every 15
  minutes by default, with catch-up after the PC was off and a retry delay after
  failures. Interval timing is not affected by daylight saving changes.
- Routine prune and integrity checks, with a partial read of the data at a
  longer interval.
- Commands: `init`, `run`, `status`, `snapshots`, `restore`, `pin`, `unpin`,
  `check`, `reclaim --dry-run`, `install`, `uninstall`, `doctor`,
  `config init`, `config validate`, `version`.
- Repository password stored encrypted with DPAPI and readable only by
  administrators; never passed in arguments, environment variables or logs.
- Daily rotating logs next to the configuration, and warnings and errors in
  the Windows Event Log.
- Memory profiles (`low`, `auto`, `high`) for restic's read concurrency and
  pack size.
- Documentation: restore guide, with Ouroboros and with plain restic, and test
  procedures, including a disk-full test on a virtual disk.

[0.1.0]: https://github.com/WildPino/Ouroboros/releases/tag/v0.1.0
