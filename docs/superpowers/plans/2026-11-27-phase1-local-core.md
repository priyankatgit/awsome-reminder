# Awesome Reminder — Phase 1 (Local Core) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A single-machine desktop app: global hotkey → Spotlight-style capture box → plain-English to-dos/reminders with #tags, a tray list window, and desktop reminder pop-ups. Usable daily on its own.

**Architecture:** One Go process using Wails v3. All logic lives in plain Go packages under `internal/` (parser, store, core, scheduler, export, hotkey, autostart) and is unit-tested without Wails. A thin Wails service (`internal/app`) exposes `core` to a vanilla-TypeScript frontend that renders four views (capture, list, alert, settings) chosen by URL hash.

**Tech Stack:** Go 1.27 · Wails v3.1.0 · vanilla TypeScript + Vite (Wails `vanilla-ts` template) · `modernc.org/sqlite` (pure Go) · `golang.design/x/hotkey` v0.6.4 · `github.com/teambition/rrule-go` v1.8.2 · `github.com/oklog/ulid/v2` v2.1.2

**Spec:** `docs/superpowers/specs/2026-11-27-awesome-reminder-design.md` (Phase 1 of "Delivery phases"). Phases 2 (Atlas sync) and 3 (ntfy) get their own plans after this ships.

## Global Constraints

- Wails pinned to exactly `v3.1.0`; CLI installed with `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0`.
- No CGo on Windows (pure-Go SQLite). macOS/Linux builds may use CGo where Wails requires it.
- Capture popup visible < 150 ms after hotkey; saving never waits on the network (Phase 1 has no network at all).
- Idle RAM < 150 MB including webview; ~0% idle CPU. The capture window is pre-created hidden. The list and settings windows are destroyed on close. Only one alert window exists at a time.
- Default hotkey `Ctrl+Shift+Space` (`Cmd+Shift+Space` on macOS); configurable.
- Default lead times `1h`, `10m`, plus an alert at the due time itself (always).
- Default time for a date without a time: `09:00`. "next week" = next `Monday 09:00` (both configurable).
- English only. Untagged items get tag `inbox`.
- Data dir: `os.UserConfigDir()/awesome-reminder/` holding `awesome.db` and `awesome.log`.
- All times are stored in UTC, along with the IANA tz name the item was created in.
- Every item write sets `updated_at` (UTC) and `updated_by` (device ID), which Phase 2 sync needs. DB migrations are versioned so later phases add columns.
- Frontend is vanilla TS with no UI framework (planner's choice for minimal memory).

## Review Focus

1. **`#123` issue numbers and `C#` in text** must stay text. A tag starts with a letter and is preceded by start-of-input or whitespace. → Task 2 test `TestParseTags`.
2. **Time-looking words inside the task** ("fix 5pm cron job") get eaten by the parser. Double-quoted text is never parsed, and the live preview shows the interpretation. → Task 3 case `quoted text is literal`.
3. **Laptop asleep through a reminder** must produce one catch-up popup per item, not a burst of the 1h, 10m and due alerts. → Task 6 test `TestPlanMissedCollapsesPerItem`.
4. **A reminder due sooner than a lead time** (due in 5 min, lead 1h) must not instantly fire the 1h alert as "missed". → Task 6 test `TestPlanSkipsLeadsBeforeCreation`.
5. **Hotkey already taken by another app**: the app keeps running, the tray tooltip and Settings show the error, and a new combo can be saved. → Task 8 test `TestHotkeyManagerReportsRegisterError`.

---

### Task 1: Scaffold the Wails app with tray + Quit

**Files:**
- Create (via template): `main.go`, `go.mod`, `go.sum`, `Taskfile.yml`, `build/**`, `frontend/**`
- Create: `internal/paths/paths.go`, `internal/paths/paths_test.go`, `.gitignore`

**Interfaces:**
- Produces: `paths.DataDir() (string, error)` creates the dir if missing and returns `<UserConfigDir>/awesome-reminder`. `paths.DBPath() (string, error)` and `paths.LogPath() (string, error)` return the files inside it.

- [ ] **Step 1:** Install the CLI: `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0`, then run `wails3 doctor`. Expected: no blocking errors for Windows (WebView2 present).
- [ ] **Step 2:** In a temp dir run `wails3 init -n awesome-reminder -t vanilla-ts`. Copy its contents into the repo root, keeping `docs/`. Set the module path to `awesome-reminder` in `go.mod` and pin `github.com/wailsapp/wails/v3 v3.1.0`. Use this template's `main.go` as the reference for exact v3.1.0 API names in all later tasks.
- [ ] **Step 3:** Write `TestDataDirUnderUserConfig`. Set `APPDATA` / `XDG_CONFIG_HOME` / `HOME` to `t.TempDir()` per OS, and assert that `DataDir()` ends with `awesome-reminder`, exists, and that `DBPath()` ends with `awesome.db`.
- [ ] **Step 4:** Run `go test ./internal/paths/ -v`. Expected: FAIL (package missing).
- [ ] **Step 5:** Implement `internal/paths`.
- [ ] **Step 6:** Replace the template's demo service and window. The app should start with **no visible window**, show a system-tray icon (template icon for now) with the menu `Open List` (no-op for now), `Settings` (no-op), `Quit`, and should not exit when the last window closes. Log to `paths.LogPath()`. Enable Wails single-instance mode with a unique ID `dev.awesome-reminder.app`; the second-instance callback is a no-op for now.
- [ ] **Step 7:** Run `go vet ./... && go test ./... && wails3 build`. Expected: all pass, and `bin/awesome-reminder.exe` exists. Running it shows a tray icon, Quit exits, and launching it again doesn't start a second copy.
- [ ] **Step 8:** Add `.gitignore` entries for `bin/`, `frontend/node_modules/`, `frontend/dist/`. Commit with `chore: scaffold Wails v3 app with tray and single-instance`.

---

### Task 2: Item model + parser basics (prefixes, tags, quotes, kind)

**Files:**
- Create: `internal/model/item.go`, `internal/parser/parser.go`, `internal/parser/tags.go`, `internal/parser/parser_test.go`

**Interfaces:**
- Produces (`model`):
  ```go
  type Kind string   // KindTodo = "todo", KindReminder = "reminder"
  type Status string // StatusOpen = "open", StatusDone = "done", StatusDeleted = "deleted"
  type Item struct {
      ID          string          `json:"id"`        // ULID
      Kind        Kind            `json:"kind"`
      Text        string          `json:"text"`
      Tags        []string        `json:"tags"`      // lowercase, no '#', deduped, in input order
      DueAt       *time.Time      `json:"dueAt"`     // UTC; nil for todos
      TZ          string          `json:"tz"`        // IANA name
      RRule       string          `json:"rrule"`     // "" = not recurring
      LeadTimes   []time.Duration `json:"leadTimes"` // nil = settings default
      Status      Status          `json:"status"`
      DoneAt      *time.Time      `json:"doneAt"`
      SnoozeUntil *time.Time      `json:"snoozeUntil"`
      CreatedAt   time.Time       `json:"createdAt"`
      UpdatedAt   time.Time       `json:"updatedAt"`
      UpdatedBy   string          `json:"updatedBy"`
  }
  ```
- Produces (`parser`):
  ```go
  type Clock struct{ Hour, Minute int }
  type Config struct {
      Loc          *time.Location
      DefaultTime  Clock        // 09:00
      NextWeekDay  time.Weekday // time.Monday
      NextWeekTime Clock        // 09:00
  }
  type Result struct {
      Text     string
      Tags     []string
      DueAt    *time.Time // UTC
      RRule    string
      Warnings []string   // e.g. "time is in the past"
  }
  func Parse(input string, now time.Time, cfg Config) Result
  ```
  The kind is derived by callers: `DueAt != nil` means reminder.

- [ ] **Step 1: Write failing tests** `TestParsePrefixesAndTags`, `TestParseTags`, `TestParseQuotes` (table-driven; `now` = Wed 2026-11-25 10:00 America/New_York; no time phrases yet):

  | input | Text | Tags | DueAt |
  |---|---|---|---|
  | `buy milk` | `buy milk` | `[]` | nil |
  | `fix flaky test #backend #CI` | `fix flaky test` | `[backend ci]` | nil |
  | `fix bug #123 in C# parser #backend` | `fix bug #123 in C# parser` | `[backend]` | nil |
  | `#ops restart #ops worker` | `restart worker` | `[ops]` | nil |
  | `remind me to call mom` | `call mom` | `[]` | nil |
  | `set me a reminder for standup notes` | `standup notes` | `[]` | nil |
  | `Set me reminder for X` | `X` | `[]` | nil |
  | `"#notatag" literal` | `#notatag literal` | `[]` | nil |
  | `   ` | `` | `[]` | nil |

  Prefixes stripped case-insensitively, only at the start: `remind me to`, `remind me`, `set me a reminder for`, `set me reminder for`, `set a reminder for`, `set reminder for`, `reminder:`, `todo:`. Tag regex: `(^|\s)#([A-Za-z][A-Za-z0-9_-]*)`. Whitespace collapsed; the result is trimmed.
- [ ] **Step 2:** Run `go test ./internal/parser/ -v`. Expected: FAIL (undefined `Parse`).
- [ ] **Step 3:** Implement `Parse` with the tokenization this task needs: extract double-quoted segments first (their content becomes literal text with the quotes removed and is never parsed), strip prefixes, extract tags. Time phrases come in Task 3; leave a single `parseWhen(tokens, now, cfg)` hook returning `(due *time.Time, rrule string, rest []token)` that currently returns nil.
- [ ] **Step 4:** Run `go test ./internal/parser/ -v`. Expected: PASS.
- [ ] **Step 5:** Commit with `feat(parser): prefixes, tags, quoted literals`.

---

### Task 3: Parser time phrases, recurrence, and Render

**Files:**
- Create: `internal/parser/when.go`, `internal/parser/render.go`, `internal/parser/when_test.go`, `internal/parser/render_test.go`

**Interfaces:**
- Consumes: Task 2 `Parse`, `Config`, `Result`.
- Produces: `func Render(text string, tags []string, due *time.Time, rrule string, loc *time.Location) string`. It renders the editable one-line form `<text> #tag1 #tag2 <when>`, where `<when>` is `2026-11-28 17:00` (local) or `every monday 10:00` / `every day 09:00` / `every weekday 09:30`. `text` is wrapped in double quotes only when `Parse(text)` would detect a time phrase or tag in it.

- [ ] **Step 1: Write failing test** `TestParseWhen`. Table-driven; `now` = **Wed 2026-11-25 10:00 America/New_York**; default cfg. Expected `DueAt` is given in local time; the test converts it to UTC.

  | name | input | Text | Due (local) | RRule |
  |---|---|---|---|---|
  | tomorrow+time | `remind me tomorrow 5pm check deploy #backend` | `check deploy` | 2026-11-26 17:00 | |
  | today at | `set me reminder for standup notes today at 4 pm` | `standup notes` | 2026-11-25 16:00 | |
  | today no time → max(09:00, next full hour) | `call bob today` | `call bob` | 2026-11-25 11:00 | |
  | date only → default time | `tomorrow pay rent` | `pay rent` | 2026-11-26 09:00 | |
  | next week | `next week plan sprint` | `plan sprint` | 2026-11-30 09:00 | |
  | in hours | `in 2 hours check build` | `check build` | 2026-11-25 12:00 | |
  | in minutes | `in 30 min stretch` | `stretch` | 2026-11-25 10:30 | |
  | in an hour | `in an hour coffee` | `coffee` | 2026-11-25 11:00 | |
  | in days keeps clock | `in 3 days follow up` | `follow up` | 2026-11-28 10:00 | |
  | weekday | `on friday 3pm demo` | `demo` | 2026-11-27 15:00 | |
  | same weekday, time passed → next week | `wednesday 9am retro` | `retro` | 2026-12-02 09:00 | |
  | same weekday, time ahead → today | `wednesday 4pm retro` | `retro` | 2026-11-25 16:00 | |
  | next weekday never today | `next wednesday sync` | `sync` | 2026-12-02 09:00 | |
  | day month | `28 nov 3pm review` | `review` | 2026-11-28 15:00 | |
  | month day | `nov 28 at 15:30 review` | `review` | 2026-11-28 15:30 | |
  | past date rolls to next year | `1 jan taxes` | `taxes` | 2027-01-01 09:00 | |
  | ISO | `2026-12-01 17:00 release` | `release` | 2026-12-01 17:00 | |
  | bare hour 1–7 = pm | `at 5 deploy` | `deploy` | 2026-11-25 17:00 | |
  | bare time passed → tomorrow | `at 9 standup` | `standup` | 2026-11-26 09:00 | |
  | 24h | `at 17:30 ship` | `ship` | 2026-11-25 17:30 | |
  | noon | `lunch at noon` | `lunch` | 2026-11-25 12:00 | |
  | eod | `eod push branch` | `push branch` | 2026-11-25 17:00 | |
  | tonight | `tonight backup` | `backup` | 2026-11-25 20:00 | |
  | case-insensitive | `Tomorrow 5PM Call` | `Call` | 2026-11-26 17:00 | |
  | weekly | `every monday 10am standup #team` | `standup` | 2026-11-30 10:00 | `FREQ=WEEKLY;BYDAY=MO` |
  | daily | `every day at 9am vitamins` | `vitamins` | 2026-11-26 09:00 | `FREQ=DAILY` |
  | weekdays | `every weekday 9:30 standup` | `standup` | 2026-11-26 09:30 | `FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR` |
  | **quoted text is literal** | `"fix 5pm cron job" tomorrow #ops` | `fix 5pm cron job` | 2026-11-26 09:00 | |
  | no time → todo | `refactor auth #backend` | `refactor auth` | nil | |

  Also assert: `today 9am x` yields due 2026-11-25 09:00 and `Warnings` contains `"time is in the past"`. Bare-hour rule: 1–7 → PM, 8–12 → as written, 13–23 → 24h. Leftover connector words (`at`, `on`, `for`, `by`) next to a removed phrase are dropped.
- [ ] **Step 2: Add failing** `TestParseWhenDST`: `now` = Sat 2027-03-13 10:00 America/New_York; `tomorrow 9am x` → `DueAt` == `2027-03-14T13:00:00Z` (09:00 EDT).
- [ ] **Step 3: Add failing** `TestRenderRoundTrip`: for every non-recurring row above with a due, `Parse(Render(r.Text, r.Tags, r.DueAt, "", loc), now, cfg)` returns the same Text, Tags and DueAt. For the three recurring rows the RRule and DueAt are the same. `Render("fix 5pm cron job", ...)` contains `"fix 5pm cron job"` in quotes.
- [ ] **Step 4:** Run `go test ./internal/parser/ -v`. Expected: FAIL.
- [ ] **Step 5:** Implement `parseWhen` as an ordered set of token-pattern rules: recurrence, then relative (`in N unit`), then day words / weekdays / dates, then time-of-day. Each rule consumes the tokens it matches; at most one date part and one time part are taken. Compute the next recurrence occurrence with `rrule-go` (DTSTART = today at the parsed time, local tz, first occurrence > now). Implement `Render`.
- [ ] **Step 6:** Run `go test ./internal/parser/ -v`. Expected: PASS.
- [ ] **Step 7:** Commit with `feat(parser): natural-language dates, recurrence, render`.

---

### Task 4: SQLite store + settings

**Files:**
- Create: `internal/store/store.go`, `internal/store/migrate.go`, `internal/store/store_test.go`, `internal/settings/settings.go`, `internal/settings/settings_test.go`

**Interfaces:**
- Produces (`settings`):
  ```go
  type Settings struct {
      Hotkey       string          `json:"hotkey"`       // "Ctrl+Shift+Space" ("Cmd+Shift+Space" on darwin)
      LeadTimes    []time.Duration `json:"leadTimes"`    // [1h, 10m]
      DefaultTime  string          `json:"defaultTime"`  // "09:00"
      NextWeekDay  string          `json:"nextWeekDay"`  // "monday"
      NextWeekTime string          `json:"nextWeekTime"` // "09:00"
      ExportPath   string          `json:"exportPath"`   // "" = off
      Autostart    bool            `json:"autostart"`    // true
  }
  func Defaults(goos string) Settings
  func (s Settings) Validate() error                          // HH:MM times, weekday name, leads > 0 and ≤ 7 days, max 5 leads
  func (s Settings) ParserConfig(loc *time.Location) parser.Config
  ```
- Produces (`store`):
  ```go
  var ErrNotFound = errors.New("not found")
  func Open(path string) (*Store, error)          // ":memory:" allowed; runs migrations; creates the device ID on first open
  func (s *Store) Close() error
  func (s *Store) DeviceID() string                // ULID, stable across reopen
  func (s *Store) Put(it model.Item) error         // upsert by ID
  func (s *Store) Get(id string) (model.Item, error)
  func (s *Store) ListOpen() ([]model.Item, error) // status=open
  func (s *Store) ListDone(limit int) ([]model.Item, error) // newest done_at first
  func (s *Store) LoadSettings(goos string) (settings.Settings, error) // Defaults if none saved
  func (s *Store) SaveSettings(settings.Settings) error
  func (s *Store) WasNotified(key string) (bool, error)
  func (s *Store) MarkNotified(key string, at time.Time) error
  ```
  Schema v1: `meta(key PK, value)`, `items(id PK, kind, text, tags_json, due_at INTEGER NULL unix-ms, tz, rrule, lead_times_json NULL, status, done_at, snooze_until, created_at, updated_at, updated_by)`, `settings(id=1 PK, json)`, `notify_log(key PK, fired_at)`, and a `schema_version` row in `meta`. Use WAL mode and `busy_timeout=5000`.

- [ ] **Step 1:** Write failing tests:
  - `TestPutGetRoundTrip`: every `Item` field survives, including nil vs empty `LeadTimes` and nil pointers.
  - `TestListOpenExcludesDoneAndDeleted`.
  - `TestListDoneOrderAndLimit`.
  - `TestDeviceIDStableAcrossReopen` (file DB in `t.TempDir()`).
  - `TestSettingsDefaultsThenSave`.
  - `TestNotifyLog`.
  - `TestMigrateIdempotent`: opening twice keeps schema_version `1`.
  - `settings`: `TestDefaults` (exact values above; darwin hotkey `Cmd+Shift+Space`) and `TestValidateRejects` (`"25:00"`, `"funday"`, lead `0`, 6 leads).
- [ ] **Step 2:** Run `go test ./internal/store/ ./internal/settings/ -v`. Expected: FAIL.
- [ ] **Step 3:** Implement both packages with `modernc.org/sqlite` (`database/sql` driver name `sqlite`).
- [ ] **Step 4:** Run the same command. Expected: PASS.
- [ ] **Step 5:** Commit with `feat(store): sqlite store, settings, notify log`.

---

### Task 5: Core domain operations

**Files:**
- Create: `internal/core/core.go`, `internal/core/view.go`, `internal/core/core_test.go`

**Interfaces:**
- Consumes: `parser.Parse/Render`, `store.*`, `settings.*`, `model.Item`.
- Produces:
  ```go
  var ErrEmpty = errors.New("empty input")
  type Preview struct {
      Kind     model.Kind `json:"kind"`
      Text     string     `json:"text"`
      Tags     []string   `json:"tags"`      // "inbox" if none
      DueAt    *time.Time `json:"dueAt"`
      RRule    string     `json:"rrule"`
      Human    string     `json:"human"`     // e.g. "Thu 26 Nov, 17:00 · every Monday"
      Alerts   []string   `json:"alerts"`    // e.g. ["1h before", "10m before", "at time"]
      Warnings []string   `json:"warnings"`
  }
  type TagGroup struct { Tag string `json:"tag"`; Items []model.Item `json:"items"` }
  type View struct {
      Upcoming []model.Item `json:"upcoming"` // open reminders: overdue first, then due within 7 days, by due asc
      Tags     []TagGroup   `json:"tags"`     // all open items per tag, tags A–Z with "inbox" first; items by created asc
      Done     []model.Item `json:"done"`     // last 50
  }
  func New(st *store.Store, now func() time.Time, loc *time.Location) *Core
  func (c *Core) OnChange(fn func())            // called after every successful mutation
  func (c *Core) Preview(input string) Preview
  func (c *Core) WithDue(input string, due time.Time) string // re-renders input with its time replaced by `due` (for the Tab time editor)
  func (c *Core) Capture(input string) (model.Item, error)
  func (c *Core) EditText(id string) (string, error) // parser.Render of the item
  func (c *Core) Update(id, input string) (model.Item, error) // re-parse; keeps ID, CreatedAt, LeadTimes
  func (c *Core) Done(id string) (model.Item, error) // recurring: advance DueAt, stay open
  func (c *Core) Reopen(id string) error
  func (c *Core) StopRepeating(id string) error
  func (c *Core) Snooze(id, preset string) error     // "10m" | "1h" | "tomorrow" (= tomorrow at DefaultTime)
  func (c *Core) Delete(id string) error             // tombstone: status=deleted
  func (c *Core) View() (View, error)
  func (c *Core) Settings() (settings.Settings, error)
  func (c *Core) SaveSettings(s settings.Settings) error // Validate first
  ```
  Every mutation sets `UpdatedAt = now().UTC()` and `UpdatedBy = st.DeviceID()`. Done/Update/Reopen/Snooze clear the `SnoozeUntil` they no longer apply to.

- [ ] **Step 1:** Write failing tests (fixed clock Wed 2026-11-25 10:00 America/New_York, `:memory:` store):
  - `TestCaptureTodoGetsInbox`: `"refactor auth"` → Kind todo, Tags `[inbox]`.
  - `TestCaptureReminder`: `"tomorrow 5pm deploy #ops"` → Kind reminder, DueAt 2026-11-26 22:00Z, TZ `America/New_York`.
  - `TestCaptureEmpty`: `"  "` → `ErrEmpty`; `"#ops"` alone → `ErrEmpty`.
  - `TestDoneRecurringAdvances`: `"every monday 10am standup"`, then Done → status open, DueAt 2026-12-07 15:00Z (next Monday after 2026-11-30), and `DoneAt` set.
  - `TestDoneRecurringOverdueSkipsToFuture`: clock moved to 2026-12-16; Done on a weekly item due 2026-11-30 → next due 2026-12-21.
  - `TestStopRepeating`: RRule becomes "" and the status stays open.
  - `TestSnoozePresets`: `10m` → 10:10, `1h` → 11:00, `tomorrow` → 2026-11-26 09:00 local.
  - `TestUpdateReparsesKeepsIdentity`: Update `"deploy #ops"` (no time) turns a reminder into a todo; ID and CreatedAt unchanged.
  - `TestViewGrouping`: overdue item first in Upcoming; an item due in 8 days is not in Upcoming but is in its tag group; `inbox` is the first group; deleted items appear nowhere.
  - `TestPreviewAlerts`: due in 30 min with defaults → Alerts `["10m before","at time"]` (the 1h lead is already past).
  - `TestOnChangeCalled` once per mutation.
  - `TestWithDue`: `WithDue("tomorrow 5pm deploy #ops", 2026-11-27 08:30 local)` parses back to Text `deploy`, Tags `[ops]`, Due 2026-11-27 08:30; `WithDue("refactor auth", t)` turns the to-do into a reminder at `t`.
- [ ] **Step 2:** Run `go test ./internal/core/ -v`. Expected: FAIL.
- [ ] **Step 3:** Implement `core` (ULIDs via `oklog/ulid/v2`; next occurrence via `rrule-go` with DTSTART = the item's DueAt in its TZ, first occurrence > max(now, DueAt)).
- [ ] **Step 4:** Run `go test ./internal/core/ -v`. Expected: PASS.
- [ ] **Step 5:** Commit with `feat(core): capture, done/recur, snooze, view`.

---

### Task 6: Scheduler

**Files:**
- Create: `internal/scheduler/plan.go`, `internal/scheduler/scheduler.go`, `internal/scheduler/plan_test.go`, `internal/scheduler/scheduler_test.go`

**Interfaces:**
- Consumes: `model.Item`, `store.WasNotified/MarkNotified`, `store.ListOpen`.
- Produces:
  ```go
  type Alert struct {
      Key    string        `json:"key"`    // "<itemID>|<dueUnix>|<leadSeconds>" or "<itemID>|snooze|<unix>"
      ItemID string        `json:"itemId"`
      Text   string        `json:"text"`
      DueAt  time.Time     `json:"dueAt"`
      Lead   time.Duration `json:"lead"`   // 0 = at due time
      Missed bool          `json:"missed"` // fire time was > 2 min ago
  }
  // Plan is pure. fireNow = alerts to show now (at most one per item); covered = every due, un-notified key
  // (including ones collapsed away) to mark notified after firing; next = earliest future alert time (zero if none).
  func Plan(items []model.Item, defaultLeads []time.Duration, now time.Time, notified func(key string) bool) (fireNow []Alert, covered []string, next time.Time)
  type Source interface {
      ListOpen() ([]model.Item, error)
      WasNotified(string) (bool, error)
      MarkNotified(string, time.Time) error
  }
  func New(src Source, leads func() []time.Duration, fire func([]Alert), now func() time.Time) *Scheduler
  func (s *Scheduler) Start(ctx context.Context) // single time.Timer to `next` + 30s wall-clock check for sleep/wake
  func (s *Scheduler) Kick()                     // re-plan now (call from core.OnChange)
  ```
  Rules: alert times = `DueAt − lead` for each lead, plus `DueAt` itself. If `SnoozeUntil` is set, the only alert is `SnoozeUntil`. Leads whose fire time is before `CreatedAt` are skipped. Todos have no alerts. When several of an item's alerts are due at once, only the latest one is shown. After `fire`, every key in `covered` is marked notified.

- [ ] **Step 1:** Write failing tests (`plan_test.go`):
  - `TestPlanFutureOnly`: due in 3h, leads [1h,10m] → fireNow empty, next = due−1h.
  - `TestPlanFiresDueLead`: now = due−1h → one alert, Lead 1h.
  - **`TestPlanSkipsLeadsBeforeCreation`**: created now, due now+5m → next = due (not instant 1h/10m alerts).
  - **`TestPlanMissedCollapsesPerItem`**: now = due+3h, nothing notified → exactly 1 alert for the item (the at-time one, `Lead=0`), `Missed=true`, and `covered` has all 3 keys.
  - `TestPlanSnoozeOverrides`: SnoozeUntil set → only the snooze alert.
  - `TestPlanSkipsNotified` and `TestPlanTodoNoAlerts`.
- [ ] **Step 2:** Write failing `TestSchedulerKickFires` (`scheduler_test.go`): fake Source + fake clock; Kick at a due time calls `fire` once, and a second Kick doesn't fire again.
- [ ] **Step 3:** Run `go test ./internal/scheduler/ -v`. Expected: FAIL.
- [ ] **Step 4:** Implement `Plan` and `Scheduler`.
- [ ] **Step 5:** Run `go test ./internal/scheduler/ -v`. Expected: PASS.
- [ ] **Step 6:** Commit with `feat(scheduler): alert planning, catch-up, timer`.

---

### Task 7: Markdown export

**Files:**
- Create: `internal/export/markdown.go`, `internal/export/markdown_test.go`

**Interfaces:**
- Produces:
  ```go
  func Markdown(v core.View, loc *time.Location) []byte
  func WriteAtomic(path string, data []byte) error // temp file in the same dir + rename
  type Exporter struct{ ... }
  func NewExporter(path func() string, view func() (core.View, error), loc *time.Location, debounce time.Duration) *Exporter
  func (e *Exporter) Trigger() // debounced (2s in app); no-op when path() == ""
  ```
  Format: `# Awesome Reminder`, then `## #<tag>` sections in View order with lines `- [ ] <text> (due 2026-11-26 17:00, every monday)`, then `## Done` with `- [x] <text> #tags`.

- [ ] **Step 1:** Write failing tests: `TestMarkdownGolden` (fixed View → compare to `testdata/golden.md`), `TestWriteAtomicReplaces`, `TestExporterDebounces` (3 Triggers within the window → 1 write; path "" → no write).
- [ ] **Step 2:** Run `go test ./internal/export/ -v`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./internal/export/ -v`. Expected: PASS.
- [ ] **Step 5:** Commit with `feat(export): todo.md export`.

---

### Task 8: Hotkey manager + autostart

**Files:**
- Create: `internal/hotkey/combo.go`, `internal/hotkey/manager.go`, `internal/hotkey/keys_windows.go`, `internal/hotkey/keys_darwin.go`, `internal/hotkey/keys_linux.go`, `internal/hotkey/combo_test.go`, `internal/hotkey/manager_test.go`
- Create: `internal/autostart/autostart.go`, `autostart_windows.go` (HKCU `Software\Microsoft\Windows\CurrentVersion\Run`, value `AwesomeReminder`), `autostart_darwin.go` (`~/Library/LaunchAgents/dev.awesome-reminder.app.plist`), `autostart_linux.go` (`$XDG_CONFIG_HOME/autostart/awesome-reminder.desktop`), `autostart_test.go`

**Interfaces:**
- Produces (`hotkey`):
  ```go
  type Combo struct { Mods []string; Key string } // canonical: mods ⊂ {Ctrl,Shift,Alt,Cmd}, Key like "Space","K","F12"
  func ParseCombo(s string) (Combo, error)        // case-insensitive, '+' separated; ≥1 modifier required
  func (c Combo) String() string
  type Registrar func(c Combo, onPress func()) (unregister func(), err error) // real impl wraps golang.design/x/hotkey
  func NewManager(reg Registrar, onPress func()) *Manager
  func (m *Manager) Set(combo string) error // unregisters the old one; on failure keeps no hotkey and records the error
  func (m *Manager) Err() error             // last registration error or nil
  func Platform() Registrar
  func SetMainThreadRunner(run func(func())) // darwin: registration must run on the main thread; no-op elsewhere
  ```
- Produces (`autostart`): `func Set(enabled bool, exePath string) error`, `func Enabled() (bool, error)`. Internally a `baseDir` var is overridable in tests (darwin/linux); on Windows a registry key path var is overridable to `Software\AwesomeReminderTest\Run`.

- [ ] **Step 1:** Write failing tests:
  - `TestParseCombo`: `"ctrl+shift+space"` → `Ctrl+Shift+Space`; `"Cmd+Shift+K"` ok; `"space"` → error (no modifier); `"Ctrl+Foo"` → error.
  - **`TestHotkeyManagerReportsRegisterError`**: the fake Registrar returns an error → `Set` returns it, `Err()` is non-nil, and a later successful `Set` clears `Err()` and calls the old unregister.
  - `TestAutostartToggle`: Set(true) → Enabled() true and the entry contains exePath; Set(false) → false.
- [ ] **Step 2:** Run `go test ./internal/hotkey/ ./internal/autostart/ -v`. Expected: FAIL.
- [ ] **Step 3:** Implement. The macOS registrar must register on the main thread. Use Wails' main-thread dispatch from the app layer: `Platform()` on darwin takes the dispatch via `hotkey.SetMainThreadRunner(func(func()))`, which the app sets in Task 9. On Linux under Wayland (`XDG_SESSION_TYPE=wayland`), registration returns an error mentioning `awesome-reminder --capture`. Support for the XDG GlobalShortcuts portal is deferred past Phase 1; the README notes it as a known limitation.
- [ ] **Step 4:** Run the same command. Expected: PASS.
- [ ] **Step 5:** Commit with `feat: hotkey manager and autostart`.

---

### Task 9: Wails service + capture window

**Files:**
- Create: `internal/app/service.go`, `internal/app/windows.go`, `internal/app/service_test.go`
- Modify: `main.go`
- Create: `frontend/index.html`, `frontend/src/main.ts`, `frontend/src/api.ts`, `frontend/src/capture.ts`, `frontend/src/style.css` (replace template files)

**Interfaces:**
- Consumes: `core.*`, `scheduler.*`, `export.Exporter`, `hotkey.Manager`, `autostart.*`.
- Produces: the Wails-bound `type Service struct`, whose exported methods become frontend bindings: `Preview(input string) core.Preview`, `WithDue(input string, due time.Time) string`, `Capture(input string) (model.Item, error)`, `EditText(id string) (string, error)`, `Update(id, input string) (model.Item, error)`, `Done(id string) error`, `Reopen(id string) error`, `StopRepeating(id string) error`, `Snooze(id, preset string) error`, `Delete(id string) error`, `View() (core.View, error)`, `GetSettings() (settings.Settings, error)`, `SaveSettings(s settings.Settings) error` (also re-registers the hotkey, toggles autostart and triggers export), `Status() Status` (`Status{HotkeyErr string}`), `HideCapture()`, `OpenList(focusItemID string)`, `OpenSettings()`, `DismissAlerts()`.
  Events emitted to the frontend: `items:changed` (no payload), `alerts:show` (payload `[]scheduler.Alert`), `list:focus` (payload item ID).
  Windows (`windows.go`): `capture`, pre-created hidden, frameless, always-on-top, 640×120 (grows with preview), centered on the active screen on every show, URL `/#capture`.
- `main.go` wiring: open the store → core → scheduler (fire → alert window, Task 11) → exporter → hotkey manager (onPress → show+focus capture) → the single-instance callback shows capture when argv contains `--capture` → tray menu Open List / Settings / Quit (tooltip shows `Status().HotkeyErr` when set) → `core.OnChange(scheduler.Kick + exporter.Trigger + emit items:changed)`.

- [ ] **Step 1:** Write failing `TestServiceSaveSettingsReRegistersHotkey` and `TestServiceSaveSettingsInvalid`. Build Service with a fake Registrar and an in-memory store. Saving `Hotkey: "Alt+K"` calls the registrar with `Alt+K`; invalid settings return an error and leave the stored settings unchanged.
- [ ] **Step 2:** Run `go test ./internal/app/ -v`. Expected: FAIL.
- [ ] **Step 3:** Implement Service + main wiring. Run `wails3 generate bindings` into `frontend/bindings`.
- [ ] **Step 4:** Implement the capture view (`#capture`):
  - input autofocused;
  - layout, tokens, fonts, icons and motion exactly per `docs/superpowers/specs/2026-11-27-ui-design-guide.md` (create `frontend/src/tokens.css`; bundle fonts with `@fontsource/ibm-plex-sans` and `@fontsource/jetbrains-mono`; Lucide SVG icons, no emoji). Reserve the right-side 48×48 action slot for the future mic button and render nothing in it. On Windows use the Acrylic/translucent backdrop for this window, with a solid fallback;
  - `Preview` debounced 80 ms, rendering the preview row: leading icon (search / bell / check-square), `human` as a time chip, tag chips, `alerts`, and warnings in amber;
  - Enter → `Capture` → clear + `HideCapture`;
  - Esc or window blur → `HideCapture`;
  - empty Enter does nothing; a Capture error shows inline;
  - **Tab** opens an inline `datetime-local` picker prefilled with the preview's due time (or tomorrow at the default time). Confirming it replaces the input text with `WithDue(input, picked)` and refocuses the input.
- [ ] **Step 5:** Run `go test ./... && wails3 build`. Expected: PASS. Manual check: Ctrl+Shift+Space opens the box centered in under 150 ms; typing `tomorrow 5pm test #x` previews `Thu 26 Nov, 17:00 · #x`; Enter hides it; `awesome-reminder.exe --capture` from a terminal opens the box in the running instance.
- [ ] **Step 6:** Commit with `feat(app): Wails service and capture window`.

---

### Task 10: List window (tray)

**Files:**
- Create: `frontend/src/list.ts`, `frontend/src/item-row.ts`
- Modify: `internal/app/windows.go`, `frontend/src/main.ts`, `frontend/src/style.css`

**Interfaces:**
- Consumes: Service `View`, `Done`, `Reopen`, `Snooze`, `Delete`, `EditText`, `Update`, `StopRepeating`; event `items:changed`, `list:focus`.
- Produces: `list` window, 900×640, normal frame, created on Open List or a left tray click, **destroyed on close**, URL `/#list`.

- [ ] **Step 0:** Follow the "List window" section, keyboard map and pre-delivery checklist of `docs/superpowers/specs/2026-11-27-ui-design-guide.md`. Reuse `tokens.css` and the icon helper from Task 9.
- [ ] **Step 1:** Implement the layout:
  - a search box at the top (client-side, case-insensitive filter over text and tags);
  - a **Today / Upcoming** section (overdue rows in red, showing the relative time);
  - one section per tag (in View order);
  - a collapsed **Done** section.
- [ ] **Step 2:** Implement row actions:
  - checkbox → Done (→ Reopen in the Done section);
  - double-click the text → inline input prefilled with `EditText`; Enter → `Update`, Esc → cancel;
  - a snooze menu (10m / 1h / tomorrow) for reminders;
  - a "Stop repeating" item when `rrule` is set;
  - delete (×) with no confirmation, plus an "Undo" toast for 5 s that calls `Reopen`.
  The list re-fetches `View` on `items:changed`. Moving an item to another tag happens by editing its `#tags`.
- [ ] **Step 3:** Run `wails3 build` and check manually: capture 3 items with 2 tags → they appear grouped; tick done → moves to Done; edit `#a` → `#b` regroups; with Task Manager open, closing the window lowers memory (the window is destroyed).
- [ ] **Step 4:** Commit with `feat(ui): tray list window`.

---

### Task 11: Alert popup + Settings window

**Files:**
- Create: `frontend/src/alert.ts`, `frontend/src/settings.ts`
- Modify: `internal/app/windows.go`, `main.go`, `frontend/src/main.ts`, `frontend/src/style.css`

**Interfaces:**
- Consumes: scheduler `fire` callback, event `alerts:show`, Service `Done`, `Snooze`, `OpenList`, `DismissAlerts`, `GetSettings`, `SaveSettings`, `Status`.
- Produces:
  - `alert` window: 380×(auto, max 420), frameless, always-on-top, bottom-right of the primary screen, URL `/#alert`. **Only one alert window exists at a time**; new alerts are appended to its queue.
  - `settings` window: 520×560, URL `/#settings`, destroyed on close.

- [ ] **Step 1:** In `main.go`, the scheduler's fire callback creates or reuses the alert window and emits `alerts:show`. Never steal keyboard focus from the user's active app: show the window without activating it where the v3.1.0 window API allows.
- [ ] **Step 2:** Build the alert and settings views per the "Alert popup" and "Settings" sections of `docs/superpowers/specs/2026-11-27-ui-design-guide.md`, reusing `tokens.css`. Build the alert view: each alert card shows:
  - the text and due time (e.g. "in 10 min" / "now" / "missed · was due 14:00");
  - buttons **Done**, **Snooze ▾** (10m, 1h, tomorrow) and **Open**, which calls `OpenList(itemId)` so the list scrolls to and highlights that item.
  When the queue empties, `DismissAlerts` closes the window.
- [ ] **Step 3:** Build the settings view:
  - a hotkey field (capture the key combo on keydown) showing the `Status().HotkeyErr` message;
  - lead-time chips (add/remove, inputs like `30m`, `2h`, `1d`);
  - the default time, "next week" day + time, the export path (text field with a "Choose…" button using the Wails file dialog), and an Autostart toggle;
  - Save → `SaveSettings`, with the validation error shown inline.
- [ ] **Step 4:** Run `go test ./... && wails3 build` and check manually:
  - `in 2 min test` → popup at the due time with working Done / Snooze / Open;
  - set the hotkey to one already used by another app → error shown, app still running, tray tooltip shows it;
  - set an export path → `todo.md` appears and updates within ~2 s of a change.
- [ ] **Step 5:** Commit with `feat(ui): alert popup and settings`.

---

### Task 12: Packaging, README, and per-OS verification

**Files:**
- Create: `README.md`, `docs/manual-test-checklist.md`
- Modify: `build/**` (app name "Awesome Reminder", tray icon assets for light/dark, macOS template icon)

- [ ] **Step 1:** Replace the template icons with an app icon and tray icons (monochrome template icon on macOS). Set the Windows/macOS metadata to name `Awesome Reminder` and bundle ID `dev.awesome-reminder.app`.
- [ ] **Step 2:** Write `README.md` covering:
  - what it is;
  - install from GitHub Releases;
  - unsigned-app notes (Windows SmartScreen "More info → Run anyway"; macOS right-click → Open or `xattr -dr com.apple.quarantine`);
  - Wayland: bind `awesome-reminder --capture` in system keyboard settings;
  - a capture phrase cheat-sheet (the Task 3 table inputs);
  - where data lives;
  - building from source (`wails3 build`).
- [ ] **Step 3:** Write `docs/manual-test-checklist.md` from the spec's Verification section for Win 11, macOS 14+, Ubuntu X11 and Wayland, with these rows:
  - hotkey latency < 150 ms;
  - popup centered on the active monitor and focused;
  - tray menu;
  - autostart after reboot;
  - catch-up: sleep the machine through a due time → exactly one "missed" card per item on wake;
  - idle RAM < 150 MB (Task Manager / Activity Monitor / `ps`) after 10 min idle with all windows closed;
  - ~0% idle CPU.
- [ ] **Step 4:** Run `go test ./... && wails3 build`, then run the Windows column of the checklist. Expected: all rows pass. Record the measured numbers in the checklist.
- [ ] **Step 5:** Commit with `docs: README, packaging metadata, manual checklist`.
