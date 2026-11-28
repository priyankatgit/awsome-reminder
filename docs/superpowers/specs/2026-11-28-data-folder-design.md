# Data folder choice (first-run setup) — design

Status: draft for approval · 2026-11-28 · base: master b953ea8

## Intent

The user's words: "very first time user choose folder their data folder where everything will be in that folder, application use it from that. So user can decide based on their choice & available space."

Today every file the app keeps sits in fixed OS folders on the system drive, whatever folder the exe runs from. The user copied the new build to `D:\program\awesome-reminder` and expected the database to live there. The voice engine alone is about 160 MB, attachments grow without bound, and the system drive is the one most likely to be short of space. The app should ask once for one folder, keep everything there, and let the user move it later.

## Goals

- On first start (no saved choice), a setup screen asks for the data folder and shows free space for each choice.
- Everything the app writes goes into that one folder: the database, attachments, the log, the sync key, the voice engine and models (dictation and wake word), and the WebView2 profile on Windows.
- Existing installs (data in `%APPDATA%\awesome-reminder\`) keep working. The default choice is to keep the data where it is. Moving it copies, checks the copy, switches, and keeps the old files as a backup until the user removes them from Settings.
- The folder can be changed later from Settings with the same copy, check and switch.
- A missing folder (an unplugged drive) or an unusable one never leads to a new, empty database. The app blocks and asks.
- The same behaviour on Windows, macOS and Linux, using each platform's own default folders.

## Non-goals

- A portable mode with no trace outside the chosen folder. A small pointer file always lives in the OS config folder. "Next to the app" is offered as a choice, but the pointer still lives in the config folder.
- Recovering from a drive unplugged while the app is running. Writes fail and show their existing errors, and the next start shows the "folder not available" screen.
- Moving the WebView2 profile on macOS and Linux. There the OS manages the web view data, and Wails exposes no path option.
- Several data folders or profiles at once.
- Changing what sync, export or settings store. The export path stays a user setting and is unaffected.

## Anti-goals (failure even if it "works")

- Any path where the user's existing tasks disappear, are overwritten, or get split across two databases.
- Deleting the old copy without the user pressing **Remove old copy**. The voice download is the one exception (agreed: it is only a download and can be fetched again).
- Silently creating a fresh database because the expected one wasn't found.
- Reworking the startup and shutdown order in `internal/app/app.go` that the sync and voice work depend on.

## Decisions made with the user

1. **Missing folder at startup: block and ask.** The screen offers **Retry**, **Choose another folder…** and **Quit**. No reminders fire until the user resolves it.
2. **After a move, the old copy stays as a backup.** Settings shows where it is, its size, and a **Remove old copy** button. Voice files are moved, not kept.
3. **Approach: a setup mode in the same exe, then a restart** (see Approaches).

## Approaches considered

| | Approach | Trade-off | Verdict |
|---|---|---|---|
| 1 | **Setup mode + restart.** `main` resolves the data folder before anything else. If there's no usable saved choice, or a move is pending, it runs a small separate Wails app (the setup window), saves the choice, starts a fresh process and exits. The normal startup code only changes where it reads paths from. | One visible restart (about a second) at setup and after each move. | **Chosen.** Gives the styled screen and leaves the tested startup order alone. |
| 2 | One process, no restart: windows first, the store and every service after the choice. | Smoother, but rewires the most delicate code and clashes with the Phase 4 (ntfy) work. | Rejected |
| 3 | Only the native folder picker before startup. | Simplest, but can't show the existing-data notice, free space or Retry, and ignores the UI guide. | Rejected |

## Design

### Folder layout

The chosen folder is called **root** below.

```
<root>/
  awesome.db (+ -wal, -shm)
  attachments/
  awesome.log
  sync.key
  voice/            (dictation engine + model, wake word files; was <UserCacheDir>/awesome-reminder/voice)
  webview/          (Windows only: WebView2 profile; was %APPDATA%\awesome-reminder.exe)
```

**Exception for the standard location (my call):** when the root is the standard location (`<UserConfigDir>/awesome-reminder`), `voice/` stays in `<UserCacheDir>/awesome-reminder/voice` and the WebView2 profile stays in Wails' default folder.
- On Windows the standard root is the *roaming* profile, and 160 MB of downloads don't belong there.
- This also means "Keep it where it is" changes nothing on disk: the installed wake word and dictation keep working with no copy and no re-download.
- For any other root, voice and webview live inside it. On a later move away from the standard location they are copied from the cache folder.

### The pointer

`<UserConfigDir>/awesome-reminder/location.json` (Windows `%APPDATA%\awesome-reminder\`, macOS `~/Library/Application Support/awesome-reminder/`, Linux `$XDG_CONFIG_HOME/awesome-reminder/`). It is written atomically (temp file plus rename).

```json
{ "version": 1, "dataDir": "D:\\Data\\Awesome Reminder", "previousDir": "C:\\Users\\…\\Roaming\\awesome-reminder", "pendingMove": "" }
```

- `dataDir`: the root in use. It must be an absolute path.
- `previousDir`: set after a move while the old copy still exists. It drives the **Remove old copy** row and is cleared when the copy is removed.
- `pendingMove`: the target of a move requested from Settings. Setup mode carries the move out at the next start.

The default root is `<UserConfigDir>/awesome-reminder`, the folder used today, so the pointer can sit inside the root. That is fine.

### Resolving at startup (`main`)

Order in `main.go`: the `--wake-helper` check (unchanged; it gets its paths from its parent) → **resolve root** → logging → `app.Run(root)`.

`paths` gains a resolver that returns one of these:

| State | Condition | What happens |
|---|---|---|
| **Ready** | Pointer exists, root exists, `awesome.db` exists, no `pendingMove` | Normal start with this root |
| **First run, existing data** | No pointer, `<UserConfigDir>/awesome-reminder/awesome.db` exists | Setup screen, existing-data variant |
| **First run, fresh** | No pointer, no old database | Setup screen, fresh variant |
| **Unavailable** | Pointer exists but the root is missing, unreadable, or has no `awesome.db` | Setup screen, unavailable variant |
| **Move pending** | `pendingMove` set | Setup screen runs the move with progress, then restarts |
| **Broken pointer** | Unreadable or invalid JSON | Treated as Unavailable, showing the error. The pointer is never silently replaced. |

The rest of the program stops calling `paths.Dir`, `DataDir`, `DBPath`, `LogPath` and `VoiceDir` with no arguments. `app.Options` gets a `DataDir string` field, and every path is built from it: DB, attachments (`core.SetAttachmentDir`), `secret.New`, `registerVoice`, `WebviewUserDataPath`, and `ReportFatal`'s folder line. The voice helpers already take `Dir` from their config. The wake helper's paths come from its parent, so its argument list must carry the new voice dir. The builder confirms this in `internal/voice/wake_proc.go`.

### Setup mode

- A separate small Wails app in the same exe: its own `application.New` with the same `SingleInstance` `UniqueID`, so a second launch during setup just focuses it. It shows one window at route `#setup`, a normal decorated window of about 560×520 px with a solid surface. It has no tray, store, scheduler, sync, voice or hotkeys.
- Before the screen exists it logs to `<UserConfigDir>/awesome-reminder/setup.log`, a small file replaced on each run.
- When it finishes, it starts the exe again with `--after-setup <pid>` and quits. The new process waits up to 10 s for that pid to exit before it creates its own single-instance lock. Otherwise it would count as a second instance and exit.
- Closing the window or choosing **Quit** exits without writing the pointer. On first run that means the app doesn't run, and the next launch shows setup again.

### The setup screen (follows `2026-11-27-ui-design-guide.md`)

Fresh and existing-data variants:

```
┌──────────────────────────────────────────────────────┐
│  Where should Awesome Reminder keep its data?         │
│  Tasks, images and voice files (about 230 MB with    │
│  voice) all live in this one folder.                  │
│                                                       │
│  ⓘ You already have data in C:\Users\…\awesome-reminder  (existing variant only)
│                                                       │
│  ◉ Keep it where it is            (Standard location) │
│    C:\Users\…\Roaming\awesome-reminder   41 GB free   │
│  ○ Next to the app                                    │
│    D:\program\awesome-reminder\Data      312 GB free  │
│  ○ Another folder…   [Browse]                         │
│    E:\Notes\Awesome Reminder             120 GB free  │
│                                                       │
│  (inline error / warning here, --danger-text)         │
│                                         [ Continue ]  │
└──────────────────────────────────────────────────────┘
```

- Radio rows with the path in JetBrains Mono, free space in `--fg-muted`, and a single accent **Continue** button. Keyboard-first: arrows move between options, Enter continues, Esc does nothing. Lucide icons only. Tokens only. The pre-delivery checklist applies.
- The first row reads "Standard location" in the fresh variant and "Keep it where it is" in the existing-data variant. It is selected by default in both.
- **Next to the app** appears only when the exe's folder is writable, isn't under `Program Files`/`Program Files (x86)`, and isn't inside a macOS `.app` bundle. Its path is `<exe dir>/Data`.
- **Browse** opens the OS folder picker (Wails dialog with directories only). The chosen folder is used as is when it's empty or already holds an `awesome.db`. Otherwise `Awesome Reminder` is appended, so picking `D:\` doesn't scatter files over the drive root. The resulting path is shown before Continue.
- Checks before Continue is accepted:
  - The folder can be created and a probe file written and deleted. Otherwise: "Can't write to this folder."
  - There is enough free space for the copy plus a margin (database + attachments + voice + 50 MB). Otherwise an inline error with the numbers.
  - A network path (UNC `\\server\…`, or a mapped network drive) gets a warning: "Network folders can damage the database if the connection drops." The user can still continue.
  - A folder that already holds an `awesome.db` while the user also has existing data elsewhere: "This folder already has Awesome Reminder data. Use that data? Your other data stays where it is." This needs an explicit confirm. The two databases are never merged. See **Adopting a folder** below.
  - A folder inside a cloud-synced tree gets a warning, the same as a network path, and the user can continue. Cloud-synced means under `%OneDrive%`, `%OneDriveConsumer%` or `%OneDriveCommercial%`, or a path containing a `Dropbox`, `Google Drive` or `iCloud Drive`/`Mobile Documents` segment. The warning: "Cloud-synced folders can damage the database or create conflicting copies. Sync across machines with Atlas instead." (my call)
- Continue in the existing-data variant with a different folder runs the copy (below) with a progress bar in the same window (`role="progressbar"`, the capture download panel's look). Continue with "Keep it where it is" just writes the pointer.

Unavailable variant:

```
  Your data folder isn't available
  E:\Notes\Awesome Reminder
  The drive may be disconnected, or the folder was moved or renamed.
  (the specific reason, e.g. "awesome.db is missing", --fg-muted)

  [ Retry ]   [ Choose another folder… ]   [ Quit ]
```

- **Retry** runs the resolver again. **Quit** exits.
- **Choose another folder…** opens the picker. A folder holding an `awesome.db` is adopted as the new root after a probe open of the database. A folder without one asks for confirmation: "This folder has no Awesome Reminder data. Start with an empty list here?" Only an explicit yes creates a new database.

### Adopting a folder (my call)

"Adopt" is a third flow next to "keep" and "move". It applies when the chosen target already holds an `awesome.db`, from setup, from Settings **Change…**, or from the Unavailable screen's **Choose another folder…**. Adopt never copies over the target's database.

1. Probe-open the target database (`store.Open` plus `integrity_check`).
2. Write the pointer with `dataDir` = target.
3. Leave `previousDir` unset (unchanged if it was already set). The other folder is a live dataset, not a backup, so **Remove old copy** must never offer to delete it.
4. From Settings, adopt writes the pointer and restarts. It needs no setup-mode run.

**Start with an empty list here** (Unavailable screen, empty folder) is handled the same way. The new database is created only on the explicit yes.

**Sync safety:** the Atlas connection string's main copy lives in the OS keychain under a fixed name (`internal/secret`), shared by every data folder. Adopt and start-empty must not connect a different database to the cluster without the user knowing. So both delete the keychain entry, and the adopted database's own fallback copy (database meta plus that folder's `sync.key`), if any, then governs. The confirm text says so: "Sync across machines will be off until you connect again" (or "…uses the connection saved with that data"). A plain move keeps the keychain entry: it is the same dataset.

### Copy, check, switch (shared by the first-run move and Settings)

The source is not open by any other process: setup mode holds the single-instance lock and the main app isn't running.

1. Create `<target>` and check that it's writable.
2. Database: open the source and run `VACUUM INTO '<target>/awesome.db.tmp'` (modernc sqlite). That gives a consistent snapshot with the WAL folded in. Rename it to `awesome.db` after the check in step 4.
3. Copy `attachments/` and `sync.key`, each file written to `name.tmp` then renamed. Copy `voice/` from its current location (old builds: `<UserCacheDir>/awesome-reminder/voice`). The log isn't copied: a new one starts.
4. Check: `store.Open` on the copy plus `PRAGMA integrity_check` returns `ok`. Every table's row count matches the source. Every attachment file exists with the same size. `sync.key` bytes are identical. Voice files match in size.
5. Switch: write the pointer (`dataDir` = target, `previousDir` = old root, `pendingMove` cleared).
6. Delete the voice folder the copy came from (the agreed exception: it is only a download).
7. Rename the old database files `awesome.db`, `-wal` and `-shm` to `awesome.db.moved`, `-wal.moved` and `-shm.moved` (my call). Older builds of the exe always open `<UserConfigDir>/awesome-reminder/awesome.db`. Without the rename, an old copy of the exe started by mistake would silently open the stale backup, and new tasks would split across two databases. With it, an old build shows an empty list: obvious, and nothing diverges. The backup is still complete and can be restored by renaming. The old root is otherwise untouched.
7. If any step fails, delete the half-written target files the app created (never pre-existing ones), leave the pointer as it was, show the error with **Try again** / **Cancel**, and the app keeps using the old root.

The WebView2 profile isn't copied. It only holds the list's "Done section open" flag (`localStorage`), which resets once. The old `%APPDATA%\awesome-reminder.exe` folder counts as part of the old copy.

### Settings: Data folder section

- A new section, "Data folder", outside the form's Save flow (like Sync and Voice): its buttons act at once.
- It shows the current path (monospace), the space the app uses, free space on that drive, and **Open folder** (opens it in the file manager) and **Change…**.
- **Change…** opens the picker, applies the same checks as setup, then shows an inline confirm: "Awesome Reminder will restart to copy your data to E:\…. Your current data stays where it is as a backup." **Restart and move** writes `pendingMove` and restarts. The restart is a quit through the normal shutdown path (scheduler, sync, voice, export all stopped) followed by a relaunch. Setup mode then runs the copy with progress and restarts again into the new root.
- When `previousDir` is set: "Old copy at C:\… (84 MB)" with **Remove old copy** and an inline confirm. It deletes only what the app owns there: `awesome.db*` (including the `.moved` files), `attachments/`, `sync.key`, `awesome.log`, `setup.log`, and on Windows `%APPDATA%\awesome-reminder.exe` (the old WebView2 profile). `location.json` stays when the old root is the config folder. Then it clears `previousDir`.

### Errors and permissions

- Unwritable folder, full drive, a network warning: handled on the setup screen and in Settings as above.
- The folder disappears between starts: the Unavailable screen.
- Whatever the resolver decided, `ReportFatal` names the real root, so "Data folder:" in the crash dialog is correct.
- Free space: `GetDiskFreeSpaceExW` on Windows, `statfs` on macOS and Linux, behind one `paths.FreeSpace(dir)` helper.

### Platforms

- Pointer and default root: the config dir on each OS, as today.
- Voice moves from the cache dir into `<root>/voice` on every OS.
- WebView2 profile: Windows only (`application.WindowsOptions.WebviewUserDataPath = <root>/webview`).
- The relaunch uses `os.Executable()` plus `exec.Command` on all three. On macOS this runs the bundle's binary directly, which is acceptable for a tray app.
- "Next to the app" is hidden inside a macOS `.app` bundle and in read-only locations.

### Compatibility with Phase 4 (ntfy)

Nothing here changes `internal/settings`. The data dir lives in the pointer, not in Settings, so the ntfy work can change Settings freely.

## Left to the builder

- Exact file split inside `internal/paths` (resolver, pointer, free space) and a new `internal/setup` (or `internal/app/setup*.go`) for setup mode and copy-check-switch.
- The Wails service shape for the setup window and the Settings section (bindings for options, browse, continue, progress events).
- How row counts are compared (`sqlite_master` table list plus `COUNT(*)` per table is fine).
- Tests: the resolver states table-driven against a temp config dir. Copy-check-switch against temp folders, including a failure halfway (injected) that must leave the source and pointer untouched. The pointer's atomic write. The frontend `#setup` route and Settings section with the existing vitest setup.
- Manual check: run temp copies of the exe with `APPDATA`/`LOCALAPPDATA` pointed at temp dirs, never `bin/awesome-reminder.exe` from the repo path, and stop them promptly (single-instance lock).
