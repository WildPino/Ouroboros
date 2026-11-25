# Machine-readable output

Commands that support `--json` write [JSON Lines](https://jsonlines.org/) to
standard output instead of text: one JSON object per line. The graphical
interface reads only this output; scripts can use it too. Commands without
`--json` support refuse the flag.

## Protocol

Every line has these fields:

| Field | Meaning |
|---|---|
| `type` | `event`, `result` or `error` |
| `command` | the command, such as `status` or `config validate` |
| `time` | when the line was written, RFC 3339 in UTC |

A command writes any number of `event` lines, then exactly one final line,
`result` or `error`, and nothing after it.

- `event` lines carry `event` (its name) and `data`. They report progress;
  a reader may ignore those it does not know.
- `result` carries `data`, described per command below.
- `error` carries `error` with `code` and `message`. `message` is the same
  English text the command prints without `--json`.

Warnings and logs still go to standard error. The exit codes do not change
(`0` ok, `1` error, `2` configuration error, `3` completed with warnings,
`4` backup skipped for lack of space): a `result` line can come with exit
code `3`, and its data then lists the problems.

### Error codes

| Code | Meaning |
|---|---|
| `config_invalid` | the configuration file is missing or invalid |
| `locked` | another Ouroboros process holds the run lock |
| `not_elevated` | the command needs administrator rights |
| `wrong_password` | the stored password does not open the repository |
| `repository_unavailable` | the repository does not exist at its path |
| `repository_locked` | restic found the repository locked |
| `nothing_matched` | a restore selection matched nothing in the snapshot |
| `cannot_reclaim` | not enough space could be freed for a backup |
| `backup_too_large` | a backup can never fit in the partition |
| `timeout` | restic exceeded its configured timeout |
| `check_failed` | the repository check found problems |
| `cancelled` | the command was interrupted |
| `failed` | any other error |

New codes may be added; a reader treats unknown codes as `failed`.

### Values

- Sizes are bytes, in fields ending with `_bytes`.
- Times are RFC 3339 in UTC.
- `null` means unknown or never, never zero: a job that never ran has
  `"last_success": null`.

## version

```json
{"type":"result","command":"version","time":"2026-11-25T18:00:00Z","data":{"version":"0.2.0"}}
```

## status

`status --json [--no-snapshots]`. `--no-snapshots` skips reading the
repository, which takes a few seconds; the snapshot fields are then `null`.
Without administrator rights the snapshot fields are also `null` and
`problems` says why.

```json
{
  "repository": {"path": "E:\\Ouroboros", "size_bytes": 59055800320, "max_repo_size_bytes": null},
  "partition": {
    "total_bytes": 107374182400, "free_bytes": 42949672960,
    "min_free_bytes": 10737418240, "prune_headroom_bytes": 2147483648,
    "required_free_bytes": 12884901888
  },
  "snapshots": {"count": 2, "oldest": "2026-04-02T10:00:00Z"},
  "task": {"installed": true, "tick": "15m"},
  "running": false,
  "maintenance": {"last_prune": "2026-04-29T10:00:00Z", "last_check": null, "last_read_data_check": null},
  "jobs": [
    {
      "name": "docs",
      "last_success": "2026-05-02T08:00:00Z",
      "last_attempt": "2026-05-02T09:00:00Z",
      "failed": true,
      "last_error": "backup: source missing",
      "next_run": "2026-05-02T09:30:00Z",
      "due": true,
      "snapshots": 2,
      "predicted_growth_bytes": 157286400
    }
  ],
  "recent_problems": [
    {"time": "2026-05-02T09:00:00Z", "kind": "backup", "job": "docs", "ok": false, "error": "source missing"}
  ],
  "problems": []
}
```

The example shows the `data` field of the result line.

| Field | Meaning |
|---|---|
| `partition` | `null` if the partition could not be measured; `required_free_bytes` is `min_free` plus the prune headroom |
| `snapshots` | all Ouroboros snapshots of this host; `oldest` is the history depth |
| `task.installed` | `null` if the scheduled task could not be queried |
| `running` | whether a run holds the lock now; `null` if unknown |
| `jobs[].failed` | the last attempt came after the last success; `last_error` is then set |
| `jobs[].due` | the job runs at the next tick |
| `jobs[].predicted_growth_bytes` | `null` before the first backup |
| `recent_problems` | up to 50 history entries that failed or had warnings, newest first; `kind` is `backup`, `reclaim`, `retention`, `prune` or `check` |
| `problems` | what could not be determined; when not empty the exit code is `3` |

## snapshots

`snapshots --json [--job NAME]`, oldest first.

```json
{
  "snapshots": [
    {
      "id": "6d904825df73a50389c4267d49efba30955fdae7c8ae7617ebf88ddebaf3b7a0",
      "short_id": "6d904825",
      "time": "2026-11-24T18:29:48Z",
      "job": "docs",
      "pinned": false,
      "hostname": "pc",
      "paths": ["C:\Users\name\Documents"],
      "files": 3,
      "size_bytes": 300013
    }
  ]
}
```

`files` and `size_bytes` describe the backed-up data, as recorded by restic
0.17 and later; they are `null` for older snapshots. `job` is empty for a
snapshot made outside Ouroboros.

## check

`check --json [--read-data PERCENT]`. The result is
`{"read_data": "5%"}`, with an empty string when no data was read back. A
check that finds problems ends with an error line of code `check_failed`.

## doctor

`doctor --json`. The result lists every check that ran; the exit code is as
without `--json`.

```json
{
  "checks": [
    {"name": "config", "status": "ok", "detail": "C:\ProgramData\Ouroboros\ouroboros.toml"},
    {"name": "admin", "status": "warn", "detail": "not running as Administrator; VSS and the scheduled task need it"}
  ]
}
```

`status` is `ok`, `warn` or `fail`. `detail` may span several lines.
