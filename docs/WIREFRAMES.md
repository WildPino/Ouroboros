# Window wireframes

Layout and content of every screen of `Ouroboros.exe` (DESIGN.md §12). They
fix what each screen shows and which calls of the bridge contract (§12.8,
question 7) it uses; colours, fonts and exact spacing are decided when the
screens are built. Text in quotes is the wording the user sees.

The window is 900 × 640 at start, resizable, with a minimum of 760 × 560.

## 1. First setup

Shown when `app.info` says setup is needed. A three-step guide, then the
first backup. "Back" keeps what was entered; closing the window before step 4
changes nothing on the machine.

### 1.1 Where to keep the backups

```
+------------------------------------------------------------------+
| Ouroboros                                        Step 1 of 3     |
|                                                                  |
| Where should the backups be kept?                                |
| Pick a drive that is used only for backups, if you have one.     |
|                                                                  |
|  ( ) D:  Backup        465 GB free of 500 GB                     |
|  (o) E:  Data          120 GB free of 1 TB                       |
|  ( ) C:  Windows        80 GB free of 256 GB                     |
|          ! Same drive as Windows: a disk failure loses both.     |
|                                                                  |
| Folder:  [ E:\Ouroboros                         ] [Change...]    |
|                                                                  |
|                                              [Cancel] [Next >]   |
+------------------------------------------------------------------+
```

- `setup.drives`; removable drives are listed with "Removable: backups run
  only while it is connected."
- "Change..." opens `dialog.pickFolder` on the chosen drive.
- An existing folder that is not empty is refused: "This folder is not
  empty. Choose an empty or new folder."

### 1.2 What to back up

```
+------------------------------------------------------------------+
| Ouroboros                                        Step 2 of 3     |
|                                                                  |
| What should be backed up?                                        |
|                                                                  |
|  [x] Documents   C:\Users\name\Documents                         |
|  [x] Desktop     C:\Users\name\Desktop                           |
|  [x] Pictures    C:\Users\name\Pictures                          |
|  [x] Projects    D:\Projects                         [Remove]    |
|                                                                  |
|  [+ Add a folder...]                                             |
|                                                                  |
| Backups run every 6 hours while the computer is on.              |
|                                              [< Back] [Next >]   |
+------------------------------------------------------------------+
```

- `setup.suggestedFolders`; "Add a folder..." uses `dialog.pickFolder`.
- A folder inside the backup folder, or the backup folder itself, is
  refused with the reason.
- "Next" needs at least one ticked folder.
- Setup creates one backup named "Documents" with the ticked folders, every
  6 hours, with open-file support (VSS) on.

### 1.3 Password

```
+------------------------------------------------------------------+
| Ouroboros                                        Step 3 of 3     |
|                                                                  |
| Choose a password for the backups.                               |
| The backups are encrypted with it. Without it nobody, including  |
| you, can read them: write it down and keep it somewhere safe.    |
|                                                                  |
| Password:          [****************        ]                    |
| Repeat password:   [****************        ]                    |
|                                                                  |
| [ ] I have written the password down.                            |
|                                                                  |
|                                     [< Back] [Start backing up]  |
+------------------------------------------------------------------+
```

- At least 8 characters; both fields must match; the box must be ticked.
- "Start backing up" calls `setup.run`; the page clears both fields right
  after the call.

### 1.4 Setting up and first backup

```
+------------------------------------------------------------------+
| Ouroboros                                                        |
|                                                                  |
| Setting up                                                       |
|  [done] Installing the program                                   |
|  [done] Creating the backup storage                              |
|  [done] Scheduling automatic backups                             |
|  [....] First backup                                             |
|         [##########--------------]  41%                          |
|         12,480 of 30,112 files, 18.2 of 44.6 GB                  |
|                                                                  |
| You can close this window: the backup goes on in the background. |
|                                                                  |
|                                                     [Close]      |
+------------------------------------------------------------------+
```

- Steps follow `setup` events; the bar follows `progress.json`.
- On an error the failed step shows the message and "Try again"; steps
  already done are not repeated.
- When the backup ends the screen becomes the main window (2).

## 2. Main window

```
+------------------------------------------------------------------+
| Ouroboros                     [Backups] [Restore] [Settings]     |
|------------------------------------------------------------------|
| Automatic backups: On                      [Pause]               |
|                                                                  |
| Backups                                         [+ Add backup]   |
| +--------------------------------------------------------------+ |
| | Documents          OK, today 14:02     Next: today 20:02     | |
| | 4 folders          212 versions                              | |
| |                                     [Back up now] [Edit]     | |
| +--------------------------------------------------------------+ |
| | Projects           Running  [######------]  52%              | |
| | D:\Projects        88 versions                               | |
| |                                                      [Edit]  | |
| +--------------------------------------------------------------+ |
|                                                                  |
| Backup drive E:                                                  |
| [#################################-----------]  used 74%         |
| Backups 412 GB   Free 260 GB   Kept free at least 50 GB          |
| Oldest version: 14 March 2025                                    |
|                                                                  |
| Recent problems                                    [Show all]    |
|  ! 23 Nov 03:10  Documents: 2 files could not be read            |
+------------------------------------------------------------------+
```

- `status.get`, refreshed as in §12.8 question 8; progress from
  `progress.json`.
- "Back up now" calls `job.backupNow`; disabled with "A backup is running"
  while the run lock is held.
- "Pause" calls `task.pause` and becomes "Resume"; when paused the line reads
  "Automatic backups: Paused" with a warning colour.
- The result of each job is one of: "OK", "OK with warnings", "Failed",
  "Not yet run", "Running".
- "Show all" lists the last 50 warnings and errors from the history.
- If removed jobs still have versions in the storage, a line under the job
  list reads "Versions of removed backups: 2 [Manage]" (4).

## 3. Backup editor

Opened by "Add backup" or "Edit".

```
+------------------------------------------------------------------+
| Edit backup                                                      |
|                                                                  |
| Name:        [ Documents              ]                          |
|                                                                  |
| Folders                                                          |
|   C:\Users\name\Documents                            [Remove]    |
|   C:\Users\name\Desktop                              [Remove]    |
|   [+ Add a folder...]                                            |
|                                                                  |
| Skip files matching                                              |
|   *.tmp                                              [Remove]    |
|   [ node_modules        ] [Add]                                  |
|                                                                  |
| Back up every:  [ 6 hours  v]                                    |
| [x] Back up files that are open in other programs (recommended)  |
|                                                                  |
| [Remove this backup]                         [Cancel] [Save]     |
+------------------------------------------------------------------+
```

- `config.get`, `config.saveJob`; field errors appear under their field.
- Names use letters, digits, `.`, `_` and `-`, starting with a letter or a
  digit, as `config validate` requires.
- The name is fixed after the first save, because it tags the versions in
  the storage; the field becomes read-only text.
- Interval choices: 1, 3, 6, 12 hours, 1 day, 1 week.
- The "open files" box is the job's VSS setting.
- "Remove this backup" asks: "Stop backing up Documents? Its versions stay
  in the storage until space is needed or you delete them." Then
  `config.removeJob`.
- When the configuration was edited by hand, the first save shows: "The
  configuration file was edited outside Ouroboros. Saving rewrites it and
  removes its comments." with "Save anyway" and "Cancel".

## 4. Versions of removed backups

```
+------------------------------------------------------------------+
| Versions of removed backups                                      |
|                                                                  |
|  Photos-old      42 versions, last 3 May 2026    [Delete...]     |
|  Work            7 versions,  last 1 Feb 2026    [Delete...]     |
+------------------------------------------------------------------+

+------------------------------------------------------------------+
| Delete the versions of "Photos-old"?                             |
|                                                                  |
| 41 versions will be deleted, about 38 GB will be freed.          |
| 1 pinned version is kept.                                        |
| This cannot be undone.                                           |
|                                                                  |
| Type the name to confirm:  [               ]                     |
|                                                                  |
|                                         [Cancel] [Delete]        |
+------------------------------------------------------------------+
```

- `jobs.removed`, `jobs.deletePreview`, then `jobs.delete` with the typed
  name; "Delete" stays disabled until the name matches exactly.
- During the deletion the dialog shows its progress and cannot be closed.

## 5. Settings

```
+------------------------------------------------------------------+
| Ouroboros                     [Backups] [Restore] [Settings]     |
|------------------------------------------------------------------|
| Space on the backup drive                                        |
|   Always keep free:          [ 10 %   ]   default 10 %           |
|   Largest size of backups:   [        ]   default: no limit      |
|                                                                  |
| Keeping versions                                                 |
|   Keep at least, per backup:     [ 3   ]  default 3              |
|   Never delete versions newer than: [ 24 hours ] default 24 hours|
|                                                                  |
| Performance                                                      |
|   Memory use:  [ Automatic v]   Run backups at: [ Low priority v]|
|                                                                  |
| [ ] Run missed backups when on battery                           |
|                                                                  |
| Program                                                          |
|   Version 0.2.0                                                  |
|   [Uninstall Ouroboros...]                                       |
|                                                                  |
|                                    [Restore defaults] [Save]     |
+------------------------------------------------------------------+
```

- `config.get`, `config.saveSettings`; each field shows its default and its
  error. Settings the page does not show (retention, integrity, timeouts,
  logging, tuning factors) keep their values when the file is saved; the
  hand-edit warning of 3 applies.
- Priority choices: "Lowest", "Low priority", "Normal" (`idle`,
  `below_normal`, `normal`).

## 6. Restore

### 6.1 Choose a version

```
+------------------------------------------------------------------+
| Ouroboros                     [Backups] [Restore] [Settings]     |
|------------------------------------------------------------------|
| Restore from:  [ Documents  v]                                   |
|                                                                  |
|  Today 14:02                                      2.1 GB         |
|  Today 08:02                                      2.1 GB         |
|  Yesterday 20:02                                  2.0 GB         |
|  22 Nov 2026 20:02                    pinned      2.0 GB         |
|  ...                                                             |
|                                                     [Open >]     |
+------------------------------------------------------------------+
```

- `snapshots.list`; newest first.

### 6.2 Choose files

```
+------------------------------------------------------------------+
| Documents, today 14:02                        [< Other version]  |
| C:\ > Users > name > Documents > Invoices                        |
|                                                                  |
|  [ ] ..                                                          |
|  [x] 2025                       folder                           |
|  [ ] 2026                       folder                           |
|  [x] summary.xlsx               48 KB    20 Nov 2026 18:11       |
|                                                                  |
| 1 folder and 1 file selected                                     |
|                                                                  |
| Restore to:  (o) A new folder: [ Desktop\Restored 2026-11-25 ]   |
|              ( ) The original place, replacing current files     |
|                                                    [Restore]     |
+------------------------------------------------------------------+
```

- `snapshots.ls` one folder at a time; the selection survives moving
  between folders.
- "A new folder" can be changed with `dialog.pickFolder`.
- "The original place" asks again: "Files in the original folders with the
  same names will be replaced by the versions from today 14:02. Continue?"
- "Restore" calls `restore.run`.

### 6.3 Restoring

```
+------------------------------------------------------------------+
| Restoring                                                        |
| [###############-----------]  58%   1,204 of 2,077 files         |
|                                                     [Stop]       |
+------------------------------------------------------------------+
```

- Events `restore`; "Stop" calls `restore.cancel` and the result says how
  many files were restored before stopping.
- At the end: "1,204 files restored to Desktop\Restored 2026-11-25" with
  "Open folder" and "Done". A restore that matched nothing says so.

## 7. Uninstall

```
+------------------------------------------------------------------+
| Uninstall Ouroboros                                              |
|                                                                  |
| Automatic backups stop and the program is removed.               |
| Your backups in E:\Ouroboros are kept: you can still read them   |
| with restic and your password.                                   |
|                                                                  |
| [ ] Also remove the settings and the saved password              |
|                                                                  |
|                                   [Cancel] [Uninstall]           |
+------------------------------------------------------------------+
```

- `app.uninstall`. At the end: "Ouroboros was removed. One file is removed
  at the next restart." with "Close".
- The same screen opens when uninstalling from Windows "Installed apps".

## 8. Messages outside the window

- WebView2 runtime missing: a native message box, "Ouroboros needs the
  Microsoft Edge WebView2 Runtime, which is missing on this computer.
  Download it from Microsoft, install it, then open Ouroboros again." with
  "Open download page" and "Close".
- A second window: the existing window comes to the front and the new
  process exits.
- An older `Ouroboros.exe` than the installed one: "A newer version of
  Ouroboros (0.2.1) is installed. This file is version 0.2.0 and was not
  used." with "Open the installed version" and "Close".
