# Data Folder Choice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** First-run setup where the user picks one folder that holds all app data, with safe move/adopt later from Settings and a blocking screen when the folder is missing.

**Architecture:** A pointer file in the OS config dir names the data root. `main` resolves it before logging; when it is not usable (or a move is pending) the same exe runs a small separate Wails app (setup window, route `#setup`) that keeps / moves / adopts / starts fresh, writes the pointer, relaunches itself and exits. The normal app takes the root through `app.Options.DataDir` and builds every path from a `paths.Layout`.

**Tech Stack:** Go 1.27, Wails v3.1.0, modernc.org/sqlite (`VACUUM INTO`), vanilla TypeScript + vitest, Lucide icons.

**Spec:** `docs/superpowers/specs/2026-11-28-data-folder-design.md` (read it first; this plan does not repeat its copy or its screen sketches).

## Global Constraints

- Pointer: `<os.UserConfigDir()>/awesome-reminder/location.json`, JSON `{"version":1,"dataDir":…,"previousDir":…,"pendingMove":…}`, written atomically (temp + rename). Never silently replaced when unreadable.
- Standard root = `<os.UserConfigDir()>/awesome-reminder`. When root is standard: voice stays `<os.UserCacheDir()>/awesome-reminder/voice`, WebView2 uses Wails' default (empty `WebviewUserDataPath`). Otherwise `<root>/voice` and `<root>/webview` (Windows only).
- A missing/unusable root never creates a database; only an explicit "Start with an empty list here" yes does.
- Old copy after a move is kept (DB files renamed to `*.moved`); only the voice folder the copy came from is deleted. **Remove old copy** deletes only the spec's list.
- `internal/settings` is not modified (Phase 4 owns it).
- No change to the startup/shutdown order inside `app.Run` beyond reading paths from `Layout`.
- UI follows `docs/superpowers/specs/2026-11-27-ui-design-guide.md`: tokens only (new tokens in their own block at the end of `tokens.css`), Lucide icons, no emoji, focus rings, 12px minimum text, pre-delivery checklist.
- Never run `bin/awesome-reminder.exe` from the repo path. Manual runs: copy the exe to a temp dir, set `APPDATA`/`LOCALAPPDATA` to temp dirs, stop it promptly (single-instance lock `dev.awesome-reminder.app`).
- `-race` is unavailable on this machine (no cgo); concurrency tests use `-count=5`.
- Commands: `go test ./...`, `npm --prefix frontend test`, `npm --prefix frontend run typecheck`, `wails3 task common:generate:bindings` after adding/changing bound methods, `wails3 build` for the exe.

## Review Focus

1. **Same folder spelled differently** (`D:\Data\` vs `d:\data`): choosing the current root is a no-op "already here", never a copy onto itself → Task 1 `SameDir` test, Task 2 `TestCheckTargetSameAsCurrent`.
2. **Target inside the current root** (or the current root inside the target's `Awesome Reminder` subfolder chain): copying into itself must be refused with "Choose a folder outside the current data folder." → Task 2 `TestCheckTargetInsideCurrent`.
3. **Apostrophes, spaces and non-ASCII in paths** (`D:\Priyank's data\Données`): `VACUUM INTO` must quote the path (`'` → `''`) → Task 2 `TestMoveQuotedPath`.
4. **A move killed halfway** (power loss, Task Manager): the pointer still names the old root, `pendingMove` stays set, leftover `*.tmp` in the target are overwritten by the next attempt → Task 2 `TestMoveRetryAfterLeftovers`.
5. **Pointer naming a file, a relative path, or invalid JSON** → Unavailable with a reason, pointer untouched → Task 1 resolver table rows.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/paths/location.go` (new) | `Location`, pointer load/save, `ConfigDir`, `StandardRoot` |
| `internal/paths/resolve.go` (new) | `Resolve()` → `Resolution` state machine |
| `internal/paths/layout.go` (new) | `Layout`: every file/dir path from a root |
| `internal/paths/fs.go`, `fs_windows.go`, `fs_unix.go` (new) | `FreeSpace`, `IsNetwork`, `IsCloudSynced`, `SameDir`, `Within` |
| `internal/paths/paths.go`, `voice.go` (modify, Task 3) | drop zero-arg helpers except `LegacyVoiceDir` |
| `internal/datamove/` (new package) | target choice, checks, measure, move (copy-check-switch), adopt, start fresh, remove old copy |
| `internal/secret/secret.go` (modify, additive) | `ClearKeychain()` |
| `main.go` (modify) | wait for parent, resolve, choose setup vs run, relaunch |
| `internal/app/app.go`, `fatal.go`, `voice.go` (modify) | `Options.DataDir`, `Layout`, `WebviewUserDataPath`, restart flag |
| `internal/app/restart.go` (new) | `RequestRestart`, `RestartRequested`, `Relaunch`, `WaitForParent`, `openFolder` |
| `internal/app/setup.go`, `setup_service.go` (new) | setup-mode Wails app + `SetupService` |
| `internal/app/datafolder.go` (new) | `Service` methods for the Settings section |
| `frontend/src/setup.ts`, `setup.test.ts` (new) | `#setup` view |
| `frontend/src/data-folder-settings.ts`, `.test.ts` (new) | Settings "Data folder" section |
| `frontend/src/api.ts`, `main.ts`, `settings.ts`, `style.css`, `tokens.css` (modify) | wiring and styles |
| `docs/manual-test-checklist.md`, `README.md` (modify) | manual checks, user docs |

---

### Task 1: paths — pointer, resolver, layout, filesystem helpers

**Files:**
- Create: `internal/paths/location.go`, `resolve.go`, `layout.go`, `fs.go`, `fs_windows.go`, `fs_unix.go`
- Test: `internal/paths/location_test.go`, `resolve_test.go`, `layout_test.go`, `fs_test.go`

**Interfaces:**
- Produces:
  - `const DBFile = "awesome.db"`
  - `func ConfigDir() (string, error)` — `<UserConfigDir>/awesome-reminder`, not created.
  - `func StandardRoot() (string, error)` — same value as `ConfigDir`.
  - `type Location struct { Version int `json:"version"`; DataDir string `json:"dataDir"`; PreviousDir string `json:"previousDir,omitempty"`; PendingMove string `json:"pendingMove,omitempty"` }`
  - `func PointerPath() (string, error)`
  - `func LoadLocation() (loc Location, found bool, err error)` — `found=false, err=nil` when the file is absent; `err` for unreadable/invalid JSON/version≠1.
  - `func SaveLocation(loc Location) error` — sets Version=1, MkdirAll config dir, writes `location.json.tmp`, fsyncs, renames.
  - `type State string` with `StateReady="ready"`, `StateFirstRunExisting="existing"`, `StateFirstRunFresh="fresh"`, `StateUnavailable="unavailable"`, `StateMovePending="move"`.
  - `type Resolution struct { State State; Loc Location; Root string; Reason string }` — `Root` is the root in use (Ready/Unavailable/MovePending) or the standard root (first-run states).
  - `func Resolve() Resolution`
  - `type Layout struct { Root string; Standard bool }`
  - `func NewLayout(root string) (Layout, error)` — `Standard = SameDir(root, StandardRoot())`.
  - Methods: `DB()`, `Log()`, `Attachments()` (`<root>/attachments`), `SyncKey()` (`<root>/sync.key`), `Voice()` (standard → `LegacyVoiceDir` value without creating; else `<root>/voice`), `Webview()` (standard or non-Windows → `""`; else `<root>/webview`). All `string`.
  - `func LegacyVoiceDir() (string, error)` — `<UserCacheDir>/awesome-reminder/voice`, not created (rename of the current `VoiceDir` body minus MkdirAll; keep `VoiceDir` until Task 3).
  - `func SameDir(a, b string) bool` — `filepath.Clean` + `filepath.Abs`, case-insensitive on Windows and macOS.
  - `func Within(child, parent string) bool` — child equals or is below parent (same normalisation).
  - `func FreeSpace(dir string) (uint64, error)` — walks up to the nearest existing ancestor; `GetDiskFreeSpaceExW` (golang.org/x/sys/windows) / `unix.Statfs` (`Bavail*Bsize`).
  - `func IsNetwork(dir string) bool` — UNC `\\` prefix, or `GetDriveTypeW == DRIVE_REMOTE` on Windows; unix: false except paths under `/Volumes` with `statfs` fstype in {smbfs, nfs, afpfs, webdav} (macOS) or {nfs, cifs, smb3, fuse.sshfs} (Linux, read from `/proc/mounts` longest-prefix match).
  - `func IsCloudSynced(dir string) bool` — under any of env `OneDrive`, `OneDriveConsumer`, `OneDriveCommercial`, or a path segment equal (case-insensitive) to `Dropbox`, `Google Drive`, `iCloud Drive`, `Mobile Documents`, or starting with `OneDrive`.

- [ ] **Step 1: Write the failing tests.** Use a helper that points `APPDATA`/`LOCALAPPDATA` (Windows), `HOME` (darwin), `XDG_CONFIG_HOME`/`XDG_CACHE_HOME` (linux) at `t.TempDir()` like the existing `TestDataDirUnderUserConfig`.
  - `TestSaveLoadLocationRoundTrip`: save `{DataDir: X, PreviousDir: Y}` → load returns found, Version 1, same fields; no `location.json.tmp` left.
  - `TestLoadLocationMissing`: found=false, err=nil.
  - `TestLoadLocationInvalid`: file `{` → err≠nil; file `{"version":2,…}` → err≠nil.
  - `TestResolve` table (each row builds the temp tree, then asserts `State` and that `Reason` is non-empty exactly for Unavailable):
    | pointer | disk | want |
    |---|---|---|
    | none | none | fresh, Root = standard |
    | none | standard/awesome.db | existing, Root = standard |
    | dataDir=R | R/awesome.db | ready, Root=R |
    | dataDir=R | R missing | unavailable, Reason contains "not found" |
    | dataDir=R | R exists, no db | unavailable, Reason contains "awesome.db is missing" |
    | dataDir=R (a file) | — | unavailable |
    | dataDir="rel/path" | — | unavailable, Reason contains "absolute" |
    | `{` | — | unavailable, Reason contains "location.json" |
    | dataDir=R, pendingMove=T | R/awesome.db | move, Root=R |
    | dataDir=R, pendingMove=T | R missing | unavailable (a move needs its source) |
    Also assert the pointer file bytes are unchanged after `Resolve()` in every row.
  - `TestLayoutStandard`: `NewLayout(standard)` → `Standard` true, `Voice()` = `<cache>/awesome-reminder/voice`, `Webview()` = "".
  - `TestLayoutCustom`: root `T` → `DB()`=`T/awesome.db`, `Attachments()`=`T/attachments`, `SyncKey()`=`T/sync.key`, `Log()`=`T/awesome.log`, `Voice()`=`T/voice`, `Webview()`=`T/webview` on windows, "" elsewhere.
  - `TestSameDirAndWithin`: `SameDir("C:\\A\\B\\", "c:\\a\\b")` true on windows; `Within(T+"/x", T)` true; `Within(T+"x", T)` false (prefix but not child).
  - `TestFreeSpace`: `FreeSpace(t.TempDir()+"/not/yet")` > 0, err nil.
  - `TestIsCloudSynced`: `t.Setenv("OneDrive", T)`; `IsCloudSynced(T+"/Docs")` true; `IsCloudSynced("D:\\Dropbox\\x")` true; `IsCloudSynced(t.TempDir())` false.
  - `TestIsNetworkUNC` (windows only): `IsNetwork(`\\server\share\x`)` true; `IsNetwork(t.TempDir())` false.

- [ ] **Step 2: Run** `go test ./internal/paths/` — expect compile failures (undefined names).
- [ ] **Step 3: Implement** the files above. `Resolve` order: load pointer (error → Unavailable "Can't read location.json: …"); not found → check `<standard>/awesome.db`; found → validate `filepath.IsAbs`, stat root (missing → "Folder not found", not dir → "Not a folder"), stat `<root>/awesome.db` (missing → "awesome.db is missing"); then PendingMove → MovePending; else Ready.
- [ ] **Step 4: Run** `go test ./internal/paths/` — PASS (existing tests too).
- [ ] **Step 5: Commit** `feat(paths): data folder pointer, resolver and layout`.

---

### Task 2: datamove — checks, move, adopt, start fresh, remove old copy

**Files:**
- Create: `internal/datamove/target.go`, `check.go`, `move.go`, `adopt.go`, `cleanup.go`
- Modify: `internal/secret/secret.go` (add `ClearKeychain`)
- Test: `internal/datamove/*_test.go`, `internal/secret/secret_test.go` (one test)

**Interfaces:**
- Consumes: Task 1 (`Layout`, `Location`, `LoadLocation`, `SaveLocation`, `SameDir`, `Within`, `FreeSpace`, `IsNetwork`, `IsCloudSynced`, `DBFile`); `store.Open(path) (*store.Store, error)`.
- Produces:
  - `const SubfolderName = "Awesome Reminder"`; `const SpaceMargin = 50 << 20`
  - `func Target(picked string) (string, error)` — `picked` if missing, empty, or holds `awesome.db`; else `picked/Awesome Reminder` with the same rule applied once more; still non-empty without a db → error "That folder isn't empty. Choose an empty folder or one with Awesome Reminder data."
  - `type Sizes struct { DB, Attachments, Voice int64 }` with `func (s Sizes) Total() int64`
  - `func Measure(from paths.Layout) (Sizes, error)` — DB counts `awesome.db` + `-wal`; missing dirs count 0.
  - `type Check struct { Path string; Free uint64; Need int64; HasData bool; Network bool; Cloud bool; Same bool; Err string }` (JSON tags lowerCamel; bound to the frontend).
  - `func CheckTarget(picked string, from *paths.Layout) Check` — runs `Target`; `Same` when `SameDir(target, from.Root)`; `Err` for: inside current root (`Within(target, from.Root)` and not Same → "Choose a folder outside the current data folder."), current root inside target (`Within(from.Root, target)` → same message), not writable (MkdirAll + create/delete `.awesome-probe` → "Can't write to this folder."), free < Need when not HasData (Need = `Measure(from).Total()+SpaceMargin`; message "Not enough space: needs X, Y free." with human sizes). `from` nil (fresh install) → Need = SpaceMargin, no inside checks. Probe must remove a folder it created when `Err` is set.
  - `type Progress struct { Step string; Done, Total int64 }` (Step ∈ "database","attachments","voice","checking"; JSON tags)
  - `func Move(ctx context.Context, from, to paths.Layout, onProgress func(Progress)) error` — the spec's steps 1–7 in order. DB: open source with `database/sql` + modernc driver name `sqlite`, `VACUUM INTO '<to>/awesome.db.tmp'` with `'` doubled; files copied via `name.tmp` + rename, removing an existing `name.tmp` first; progress emitted at most every 64 KiB copied and at each step change. Check: `store.Open` copy (then Close), `PRAGMA integrity_check` = `ok`, per-table `COUNT(*)` equal for every name in `sqlite_master WHERE type='table'`, attachment/voice files same size, `sync.key` bytes equal. Then rename `awesome.db.tmp` → `awesome.db`; `SaveLocation{DataDir: to.Root, PreviousDir: from.Root}` (clears PendingMove); remove `from.Voice()` (only when it differs from `to.Voice()`); rename `from` DB files to `*.moved`. On any error before SaveLocation: remove only files/dirs this call created in `to` (track them), return the error wrapped with the step name; pointer untouched. `ctx` cancellation checked between files → same rollback, returns `context.Canceled`.
  - `func Adopt(target string, prev *paths.Location) error` — probe `store.Open` + `integrity_check`, then `secret.ClearKeychain()` (error → returned, pointer unchanged), then `SaveLocation{DataDir: target, PreviousDir: prev.PreviousDir}` (prev nil → empty).
  - `func StartFresh(target string, prev *paths.Location, clearKeychain bool) error` — MkdirAll, `store.Open` (creates schema) + Close, `ClearKeychain` only when `clearKeychain`, `SaveLocation` as Adopt. (A brand-new install passes false; the Unavailable screen's "Start with an empty list here" passes true.)
  - `func KeepStandard() error` — `SaveLocation{DataDir: StandardRoot()}`.
  - `func RequestMove(target string) error` — load pointer, set `PendingMove`, save.
  - `func RemoveOldCopy(loc paths.Location) error` — refuses when `SameDir(loc.PreviousDir, loc.DataDir)` or `PreviousDir` holds a live `awesome.db` that the pointer names; removes `awesome.db`, `awesome.db-wal`, `awesome.db-shm` and their `.moved`, `attachments/`, `sync.key`, `awesome.log`, `setup.log` inside PreviousDir; on Windows also `filepath.Join(os.Getenv("APPDATA"), "awesome-reminder.exe")`; removes PreviousDir itself only if then empty and not `ConfigDir()`; then saves the pointer with `PreviousDir=""`.
  - `func OldCopySize(dir string) int64`
  - In `internal/secret`: `func ClearKeychain() error` — `keyring.Delete(service, account)`, `ErrNotFound` → nil.

- [ ] **Step 1: Write the failing tests** (temp config dirs via the Task 1 env helper; build a source root with `store.Open` + a few `Put` items, `attachments/a.webp`, `sync.key` 32 bytes, `voice/models/m.bin`):
  - `TestTarget`: empty dir → itself; missing → itself; dir with `awesome.db` → itself; dir with `x.txt` → `…/Awesome Reminder`; dir with `x.txt` and non-empty `Awesome Reminder/` without db → error.
  - `TestCheckTargetSameAsCurrent`: `CheckTarget(strings.ToUpper(src.Root)+"\\", &src)` on windows (plain path elsewhere) → `Same` true, `Err` "".
  - `TestCheckTargetInsideCurrent`: `CheckTarget(src.Root+"/sub", &src)` → Err "Choose a folder outside the current data folder.", and `src.Root/sub` was not left behind.
  - `TestCheckTargetHasData`: target with an `awesome.db` → `HasData` true.
  - `TestMoveCopiesAndSwitches`: `Move(ctx, src, dst, rec)` → nil; dst has db with same item count, attachment, identical `sync.key`, voice file; pointer `{DataDir: dst, PreviousDir: src}`; src has `awesome.db.moved` and no `awesome.db`; src voice dir gone; progress saw steps database→attachments→voice→checking with final Done==Total.
  - `TestMoveQuotedPath`: dst root named `Priyank's data/Données` → succeeds.
  - `TestMoveFailureLeavesSourceAndPointer`: inject failure via package var `var afterCopy = func(step string) error { return nil }` set to fail at "voice" → error mentions "voice"; pointer bytes unchanged; src untouched (db present, not renamed); dst contains no files created by Move (a pre-existing `keep.txt` in dst survives).
  - `TestMoveRetryAfterLeftovers`: pre-create `dst/awesome.db.tmp` (garbage) and `dst/attachments/a.webp.tmp` → Move succeeds.
  - `TestMoveCancelled`: cancelled ctx → `errors.Is(err, context.Canceled)`, same rollback assertions.
  - `TestAdoptKeepsOtherDataLive`: target with its own db; prev `{PreviousDir: P}` → pointer `{DataDir: target, PreviousDir: P}`; target db unchanged (mtime/bytes).
  - `TestStartFreshCreatesDB`: empty target → `awesome.db` exists and opens; with `clearKeychain=false` the injected keychain func is not called, with `true` it is.
  - `TestRemoveOldCopyOnlyOwnedFiles`: PreviousDir holds the owned list plus `notes.txt` → only `notes.txt` remains, dir kept; pointer `PreviousDir` cleared.
  - `TestRemoveOldCopyRefusesLive`: PreviousDir == DataDir → error, nothing removed.
  - Adopt/StartFresh tests swap the keychain call through a package var `var clearKeychain = secret.ClearKeychain` so tests don't touch the real keychain; one test asserts Adopt returns the injected keychain error and leaves the pointer unchanged.
- [ ] **Step 2: Run** `go test ./internal/datamove/ ./internal/secret/` — expect compile failures.
- [ ] **Step 3: Implement.** `Within`/`SameDir` from Task 1; human sizes "84 MB"/"1.2 GB" via a small `formatBytes(int64) string` (exported as `FormatBytes`, Task 6 reuses it).
- [ ] **Step 4: Run** `go test ./internal/datamove/ ./internal/secret/ -count=5` — PASS.
- [ ] **Step 5: Commit** `feat(datamove): copy-check-switch, adopt, start fresh and old-copy removal`.

---

### Task 3: wire the root through main and app.Run; restart plumbing

**Files:**
- Create: `internal/app/restart.go`, `internal/app/restart_test.go`
- Modify: `main.go`, `main_test.go`, `internal/app/app.go:45-80,120-135,170`, `internal/app/fatal.go:23-29`, `internal/app/voice.go:66-72`, `internal/paths/paths.go`, `internal/paths/voice.go`, `internal/paths/paths_test.go`

**Interfaces:**
- Consumes: Task 1 `Resolve`, `NewLayout`, `Layout`.
- Produces:
  - `app.Options.DataDir string` (required; `Run` returns an error when empty).
  - `func RequestRestart()`, `func RestartRequested() bool` (atomic flag).
  - `func Relaunch() error` — `exec.Command(os.Executable(), "--after-setup", strconv.Itoa(os.Getpid())).Start()`, detached (Windows `CREATE_NEW_PROCESS_GROUP|DETACHED_PROCESS` via `SysProcAttr`), cwd = exe dir.
  - `func WaitForParent(args []string) []string` — if args contain `--after-setup <pid>`, poll every 100 ms up to 10 s until that process is gone (Windows `OpenProcess`+`WaitForSingleObject`; unix `syscall.Kill(pid,0)`), and return args with the two tokens removed.
  - `func ReportFatal(err error, dataDir string)` (signature change; `main` passes the resolved root, or the standard root when unknown).
  - `func openFolder(dir string) error` — `explorer.exe dir` / `open dir` / `xdg-open dir`.
  - `paths`: delete `Dir`, `DataDir`, `DBPath`, `LogPath`, `VoiceDir` and their tests; `LegacyVoiceDir` stays.
- `app.Run` changes: `lay, err := paths.NewLayout(opts.DataDir)`; MkdirAll root; `store.Open(lay.DB())`; `c.SetAttachmentDir(lay.Attachments())`; `secret.New(lay.Root, st)`; `WindowsOptions.WebviewUserDataPath: lay.Webview()`; `registerVoice(app, win, lay.Voice())` (signature gains `dir string`; MkdirAll it there, keep the TempDir fallback on error).

- [ ] **Step 1: Write the failing tests:**
  - `TestWaitForParentStripsFlag` (restart_test.go): `WaitForParent([]string{"--after-setup","999999","--capture"})` with a pid that doesn't exist returns `[]string{"--capture"}` in < 1 s.
  - `TestWaitForParentNoFlag`: args returned unchanged.
  - `TestRestartFlag`: `RestartRequested()` false, after `RequestRestart()` true (reset via a test-only `restartRequested.Store(false)`).
  - `TestRunRequiresDataDir` (app package): `Run(Options{})` returns an error containing "data folder" without creating a window (check before `application.New`).
  - Update `fatal_test.go` to the new `ReportFatal` usage if it calls it (it tests `reportFatal`; keep that).
- [ ] **Step 2: Run** `go test ./internal/app/ ./internal/paths/ .` — expect failures.
- [ ] **Step 3: Implement.** `main.go` order:
  1. `--wake-helper` check (unchanged).
  2. `args := app.WaitForParent(os.Args[1:])` (launch parsing in `app.Run` must use these args: add `Options.Args []string`, used where `os.Args[1:]` is read today in `app.go`'s ApplicationStarted handler).
  3. `res := paths.Resolve()`.
  4. If `res.State != paths.StateReady`: `logger := setupLogging(<ConfigDir>/setup.log, truncate)`; `err := app.RunSetup(app.SetupOptions{Assets: assets, Logger: logger, Resolution: res})` (Task 4 provides; until then, a stub in `internal/app/setup.go` returning `fmt.Errorf("setup required: %s", res.State)`); on err → `app.ReportFatal(err, res.Root)`, exit 1; if `app.RestartRequested()` → `app.Relaunch()`; exit 0.
  5. Else `logger := setupLogging(lay.Log(), append)` and `app.Run(...DataDir: res.Root, Args: args)`; on error `ReportFatal(err, res.Root)`; after a nil return, `if app.RestartRequested() { app.Relaunch() }`.
  `setupLogging(path string, truncate bool)`. Keep `TestLoggerLevelAboveDebug` passing.
- [ ] **Step 4: Run** `go test ./...` — PASS; `go vet ./...` clean.
- [ ] **Step 5: Manual smoke** (temp copy, `APPDATA`/`LOCALAPPDATA` → temp): with a pre-written `location.json` naming a temp root containing an `awesome.db` (copy one made by `store.Open`), `wails3 build`, start the copy, confirm `awesome.log` appears in that root and the tray icon shows; quit from the tray.
- [ ] **Step 6: Commit** `feat(app): run from the resolved data folder`.

---

### Task 4: setup mode backend (SetupService + window)

**Files:**
- Create/replace: `internal/app/setup.go`, `internal/app/setup_service.go`, `internal/app/setup_service_test.go`
- Modify: `internal/app/events.go` (register `EventSetupProgress`)

**Interfaces:**
- Consumes: Task 1, Task 2 (`CheckTarget`, `Move`, `Adopt`, `StartFresh`, `KeepStandard`, `Measure`, `Target`, `FormatBytes`), Task 3 (`RequestRestart`).
- Produces:
  - `type SetupOptions struct { Assets fs.FS; Logger *slog.Logger; Resolution paths.Resolution }`
  - `func RunSetup(o SetupOptions) error` — `application.New` with `Name: appName`, same `SingleInstance.UniqueID` (second launch → focus the setup window), `Windows.DisableQuitOnLastWindowClosed: false`, assets handler only (no attachment middleware), `RegisterService(NewService(svc))`, one window: name `setup`, URL `/#setup`, title `Awesome Reminder`, 560×560 DIP, min 480×480, background `listBgDark`/`listBgLight` per `isDarkMode`, centred. Closing the window quits.
  - `const EventSetupProgress = "setup:progress"` carrying `datamove.Progress`.
  - `type FolderOption struct { Kind string; Label string; Path string; Free uint64; FreeText string }` (`Kind` ∈ "standard","beside","other").
  - `type SetupInfo struct { State string; Root string; Reason string; ExistingDir string; Options []FolderOption; MoveFrom string; MoveTo string; NeedText string }`
  - `type SetupService struct` (deps injected for tests: `resolve func() paths.Resolution`, `exe func() (string, error)`, `move`, `adopt`, `startFresh`, `keep`, `quit func()`, `emit func(string, any)`).
  - Methods (bound):
    - `Info() SetupInfo` — options: standard (label "Keep it where it is" when State=="existing" else "Standard location"); beside = `<exeDir>/Data` only if exe dir is writable (probe), not under `%ProgramFiles%`/`%ProgramFiles(x86)%`, not containing `.app/Contents/`; Free via `FreeSpace`.
    - `Check(picked string) datamove.Check` — `CheckTarget(picked, fromLayout)` where from = standard layout when State=="existing", the current root's layout for "unavailable"/"move" only if it exists, nil for "fresh".
    - `Choose(kind, path string, confirm bool) error` — rules:
      - fresh: `standard` → `StartFresh(standard, nil, false)`; `beside`/`other`: a `Check.HasData` target with `!confirm` → error `"confirm-adopt"`, with confirm → `Adopt`; an empty target → `StartFresh(target, nil, false)`.
      - existing: `standard` → `KeepStandard`; `beside`/`other`: HasData → needs confirm → `Adopt`; else → `Move` in a goroutine emitting `EventSetupProgress`, returns immediately; `MoveDone`/failure reported by event `setup:done` `{ok bool, error string}`.
      - unavailable: HasData → needs confirm (text differs, frontend side) → `Adopt`; empty → needs confirm "confirm-fresh" → `StartFresh(…, true)`.
      - On success (sync paths) → `RequestRestart()`, `quit()`.
    - `RunPendingMove() error` — State "move": `Move(ctx, layout(root), layout(PendingMove))` in a goroutine with the same events; on success restart+quit.
    - `CancelMove()` — cancels the ctx.
    - `Retry() SetupInfo` — re-resolve; Ready → `RequestRestart()`, `quit()`.
    - `Quit()` — quit without restart.
  - Events registered in `init()` like `wake.go`: `EventSetupProgress` (`datamove.Progress`), `EventSetupDone = "setup:done"` (`SetupDone{OK bool; Error string}`).

- [ ] **Step 1: Write the failing tests** (service with fakes, no Wails app):
  - `TestSetupInfoExistingLabelsKeep`: existing → first option Kind "standard", Label "Keep it where it is", `ExistingDir` = standard root.
  - `TestSetupInfoBesideHiddenUnderProgramFiles`: exe `C:\Program Files\AR\ar.exe` (with `ProgramFiles` env set) → no "beside" option; exe in a writable temp dir → beside Path `<tmp>/Data`.
  - `TestChooseExistingKeep`: calls keep once, restart requested, quit called.
  - `TestChooseAdoptNeedsConfirm`: target with data, confirm=false → error "confirm-adopt", adopt not called; confirm=true → adopt called.
  - `TestChooseUnavailableEmptyNeedsConfirm`: empty target → "confirm-fresh"; with confirm → startFresh called with clearKeychain=true.
  - `TestChooseMoveEmitsProgressThenDone`: fake move emits two progress values → `emit` saw `setup:progress` ×2 then `setup:done{OK:true}`; restart requested.
  - `TestChooseMoveFailureNoRestart`: fake move error → `setup:done{OK:false, Error:…}`, restart not requested, quit not called.
  - `TestRetryReadyRestarts`: resolve returns Ready → restart + quit.
- [ ] **Step 2: Run** `go test ./internal/app/ -run Setup` — fail.
- [ ] **Step 3: Implement**; then `wails3 task common:generate:bindings` and confirm `frontend/bindings/awesome-reminder/internal/app/setupservice.js` exists.
- [ ] **Step 4: Run** `go test ./internal/app/ -count=5` — PASS.
- [ ] **Step 5: Commit** `feat(app): setup mode service and window`.

---

### Task 5: setup screen frontend (`#setup`)

**Files:**
- Create: `frontend/src/setup.ts`, `frontend/src/setup.test.ts`
- Modify: `frontend/src/main.ts` (route `setup: mountSetup`), `frontend/src/api.ts` (`setup` namespace + `chooseFolder`), `frontend/src/style.css` (`.setup-*`), `frontend/src/tokens.css` (own "Setup" block, only if a new value is needed), `frontend/src/icons.ts` (Lucide `folder`, `hard-drive`, `info`, `triangle-alert`, `folder-open` if absent)

**Interfaces:**
- Consumes: Task 4 bindings (`SetupService.Info/Check/Choose/RunPendingMove/CancelMove/Retry/Quit`, events `setup:progress`, `setup:done`).
- Produces: `export function mountSetup(root: HTMLElement): void`; `api.setup = { info, check, choose, runPendingMove, cancelMove, retry, quit, onProgress(cb), onDone(cb) }`; `api.chooseFolder(title: string): Promise<string>` — `Dialogs.OpenFile({ Title, CanChooseDirectories: true, CanChooseFiles: false, CanCreateDirectories: true })`, "" when cancelled (also used by Task 6).

Behaviour (copy is fixed by the spec's sketches; use it verbatim):
- States "fresh"/"existing": heading, sub-copy with `NeedText`, existing notice (`info` icon) only for "existing", a `role="radiogroup"` of option rows (path in JetBrains Mono, free space `--fg-muted`), "Another folder…" row with **Browse** (calls `chooseFolder`, then `check`; shows the resulting path and free space; `Err` inline in `--danger-text`; `Network`/`Cloud` warnings in `--time-text` with `triangle-alert`, copy from the spec). Continue disabled while the selected option has `Err`. Arrow keys move selection (native radios), Enter submits, Esc does nothing.
- Continue → `choose(kind, path, false)`; error "confirm-adopt" → inline confirm panel with the spec's adopt copy plus the sync line "Sync across machines will be off until you connect again." and **Use that data** (primary) / **Back**; "confirm-fresh" → the spec's empty-folder copy with **Start with an empty list** / **Back**.
- During a move: options replaced by a `role="progressbar"` (`aria-valuenow` = Done/Total %, label = Step: "Copying your tasks…", "Copying images…", "Copying voice files…", "Checking the copy…") using the capture download panel classes (`cap-voice-*`, `cap-progress`) and **Cancel**; `setup:done` error → error text + **Try again** (re-runs the same choose) / **Cancel** (back to options).
- State "unavailable": the spec's sketch; **Retry** (primary, focused), **Choose another folder…**, **Quit**; reason in `--fg-muted`.
- State "move": calls `runPendingMove()` on mount and shows the progress view with "Moving your data to <MoveTo>".

- [ ] **Step 1: Write the failing tests** (`setup.test.ts`, vitest + jsdom, mock `./api` like `voice-settings.test.ts` does):
  - renders existing state with "Keep it where it is" checked and the existing notice;
  - fresh state has no existing notice and label "Standard location";
  - Browse → `check` result with `err` shows the message and disables Continue;
  - `cloud: true` shows the cloud warning and Continue stays enabled;
  - Continue on a HasData target → `choose` rejects "confirm-adopt" → confirm panel; **Use that data** calls `choose(kind, path, true)`;
  - progress events update `aria-valuenow`; `setup:done {ok:false}` shows Try again;
  - unavailable state focuses Retry and Quit calls `api.setup.quit`;
  - move state calls `runPendingMove` once on mount.
- [ ] **Step 2: Run** `npm --prefix frontend test -- setup` — fail.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `npm --prefix frontend test` and `npm --prefix frontend run typecheck` — PASS.
- [ ] **Step 5: Manual check:** `wails3 build`; temp copy with empty temp `APPDATA`/`LOCALAPPDATA` → setup window shows "fresh"; pick "Another folder…" in a temp dir → Continue → app restarts into the tray with `awesome.db` in that folder. Then seed a temp `APPDATA\awesome-reminder\awesome.db`, delete `location.json` → "existing" screen → move to a temp folder; check `awesome.db.moved` in the old dir. Unplug simulation: rename the root → "unavailable" screen; rename back → Retry starts the app. Screenshot dark and light. Run the guide's pre-delivery checklist.
- [ ] **Step 6: Commit** `feat(frontend): first-run data folder screen`.

---

### Task 6: Settings "Data folder" section

**Files:**
- Create: `internal/app/datafolder.go`, `internal/app/datafolder_test.go`, `frontend/src/data-folder-settings.ts`, `frontend/src/data-folder-settings.test.ts`
- Modify: `internal/app/service.go` (Deps gains `DataDir string`, `Restart func()`; no other change), `internal/app/app.go` (pass `DataDir: lay.Root`, `Restart: func(){ RequestRestart(); app.Quit() }`), `frontend/src/settings.ts` (a `<section id="data-folder-section">` after the Voice section and `mountDataFolderSection(...)`), `frontend/src/api.ts` (`dataFolder` namespace), `frontend/src/style.css`

**Interfaces:**
- Consumes: Task 2 (`CheckTarget`, `RequestMove`, `Adopt`, `RemoveOldCopy`, `OldCopySize`, `Measure`, `FormatBytes`), Task 1 (`LoadLocation`, `FreeSpace`, `NewLayout`), Task 3 (`openFolder`), Task 5 (`api.chooseFolder`).
- Produces (bound `Service` methods):
  - `type DataFolderInfo struct { Path string; UsedText string; FreeText string; PreviousDir string; PreviousText string }`
  - `DataFolder() (DataFolderInfo, error)`
  - `CheckDataFolder(picked string) datamove.Check`
  - `ChangeDataFolder(picked string, confirm bool) error` — Check Err → error; Same → error "This is already your data folder."; HasData && !confirm → "confirm-adopt"; HasData → `Adopt(target, &loc)` then `Restart()`; else `RequestMove(target)` then `Restart()`.
  - `RemoveOldCopy() error` — `datamove.RemoveOldCopy(loc)`.
  - `OpenDataFolder() error` — `openFolder(DataDir)`.
- Frontend `export function mountDataFolderSection(root: HTMLElement): void`: section title "Data folder", note "Changes here restart Awesome Reminder.", path (mono), "Uses 84 MB · 41 GB free", **Open folder**, **Change…** → `chooseFolder` → `checkDataFolder` → inline confirm "Awesome Reminder will restart to copy your data to <path>. Your current data stays where it is as a backup." with **Restart and move** / **Cancel** (HasData: the adopt copy + sync line, **Restart and use that data**). When `previousDir`: "Old copy at <path> (<size>)" + **Remove old copy** → inline confirm "Delete the old copy? This can't be undone." **Delete** (danger) / **Cancel**. Outside the form and its Save button (like `voice-settings.ts`).

- [ ] **Step 1: Write the failing tests:**
  - Go: `TestChangeDataFolderMoveRequestsRestart` (temp env, pointer exists) → pointer `PendingMove` = target, Restart called once; `TestChangeDataFolderSame` → error, no restart; `TestChangeDataFolderAdoptNeedsConfirm`; `TestRemoveOldCopyViaService` clears `PreviousDir`; `TestDataFolderInfo` returns Path and a non-empty FreeText.
  - Frontend: renders path and usage; Change → confirm → **Restart and move** calls `changeDataFolder(path,false)`; HasData path shows adopt copy and calls with `true`; old-copy row hidden without `previousDir`, Delete confirm calls `removeOldCopy` then re-renders without the row; `err` from check shows inline and no confirm.
- [ ] **Step 2: Run** `go test ./internal/app/ -run DataFolder` and `npm --prefix frontend test -- data-folder` — fail.
- [ ] **Step 3: Implement**; regenerate bindings.
- [ ] **Step 4: Run** `go test ./...`, `npm --prefix frontend test`, `npm --prefix frontend run typecheck` — PASS.
- [ ] **Step 5: Manual check** (temp copy as in Task 5): Settings → Change… → temp folder → Restart and move → setup progress → app back with the new path; Remove old copy deletes the owned files only. Screenshot the section dark and light.
- [ ] **Step 6: Commit** `feat(settings): change the data folder and remove the old copy`.

---

### Task 7: docs and the whole-flow check

**Files:**
- Modify: `README.md` (where data lives; first-run setup; moving; missing drive; macOS/Linux folders), `docs/manual-test-checklist.md` (a "Data folder" section: fresh, existing keep, existing move, adopt, unavailable/Retry, Settings move, remove old copy, network/cloud warnings, old-exe-after-move shows empty list, wake word still works after "Keep")

- [ ] **Step 1:** Write the README and checklist sections.
- [ ] **Step 2:** Run everything: `go test ./...`, `go vet ./...`, `npm --prefix frontend test`, `npm --prefix frontend run typecheck`, `wails3 build` — all green.
- [ ] **Step 3:** End-to-end on a temp copy with temp `APPDATA`/`LOCALAPPDATA`, seeded with a copy of a Phase-1-era database plus a fake `%LOCALAPPDATA%\awesome-reminder\voice` folder: "existing" → Keep → wake/voice dir still resolved to the cache path (check `awesome.log`); then Settings → move to a temp `D:`-like folder → voice folder moved, `*.moved` left, Remove old copy works. Stop the app after each run.
- [ ] **Step 4: Commit** `docs: data folder setup`.
