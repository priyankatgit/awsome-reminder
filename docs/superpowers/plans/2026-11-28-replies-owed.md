# Replies Owed Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A second global hotkey grabs the message the user is looking at and saves it as a "reply owed" that is listed under "Waiting on you", nudged until replied or dropped, and summarised in an end-of-day sweep.

**Architecture:** Additive fields on `model.Item` (store v4, synced through the Phase 2 Atlas mapping). A new `internal/reply` package guesses the person and source. A new `internal/grab` package does the per-OS selection grab with clipboard save and restore. `core` gains reply capture, drop and a `Waiting` view group. `scheduler` gains nudges and the sweep. `app` registers a second `hotkey.Manager` and opens the capture box in reply mode. Frontend changes are in `capture.ts`, `list.ts`/`item-row.ts`, `alert.ts`, the panel and `settings.ts`.

**Tech Stack:** Go 1.27 · Wails v3.1.0 · `golang.org/x/sys/windows` (SendInput, clipboard; no cgo on Windows) · cgo + ApplicationServices on macOS (read-reviewed, as Phase 1.6 ruling R2) · `xclip`/`xsel`/`wl-paste` via exec on Linux, only if present · vanilla TS + Vite · vitest + jsdom.

**Spec:** `docs/superpowers/specs/2026-11-28-replies-owed-design.md`

**Precondition:** Phase 2 (Atlas sync, branch `claude/phase2-atlas-sync`) is merged into the working branch. Task 1 takes schema v4 after Phase 2's v3. Task 2 edits Phase 2's `internal/cloudsync/atlas/docs.go` and `internal/cloudsync/memsync`. Task 3 extends Phase 2's `settings.Shared`. Take the merged files as the base.

## Global Constraints

- Default reply hotkey: `Ctrl+Alt+R` (Windows/Linux), `Ctrl+Option+R` (macOS). It must differ from the capture hotkey. Never default to `Ctrl+Shift+R`.
- Nudges for an item without a due time: 2 hours after `CreatedAt`, then the next working day at the default time (`Settings.DefaultTime`, `09:00`), then every working day at the default time until closed. With a due time: the normal lead and at-time alerts, then a daily working-day nudge once the due time has passed.
- Working days default Mon–Fri; end-of-day time default `17:30`; end-of-day sweep default on. Working days, end-of-day time and the sweep switch are in `settings.Shared` (they describe the person). The reply hotkey is per device, like `Hotkey`.
- The sweep fires on working days at the end-of-day time, only if at least one reply is owed. Slept through it, it shows once on wake the same local day, never on a later day.
- One alert card per item. Card buttons: **Replied**, **Snooze**, **Open**. The sweep card: **Open list**, **Tomorrow**.
- Clipboard: save every format that can be read into memory, inject, restore all. Don't inject when the clipboard holds `CF_HDROP` (15), `CF_BITMAP` (2), `CF_DIB` (8), `CF_DIBV5` (17), `CF_ENHMETAFILE` (14), `CF_METAFILEPICT` (3), or any format whose `GetClipboardData` returns null. Mark the restore write excluded from history and cloud (`ExcludeClipboardContentFromMonitorProcessing`, `CanIncludeInClipboardHistory`=0, `CanUploadToCloudClipboard`=0).
- Fallback quote: the current clipboard text when it is plain text of at most 2,000 characters; else none.
- Wait at most 150 ms for the clipboard sequence number to change after the injected copy. From key press to visible box: at most 200 ms on top of the capture box's normal show.
- The quote in the line is trimmed to 200 characters; the full grabbed text goes into the description as a Markdown blockquote.
- No polling of the clipboard or foreground window: work happens only on the key press. Idle CPU stays ~0.
- Everything stays local except Phase 2 sync of the new fields.
- UI: tokens only (`frontend/src/tokens.css`), contrast ≥ 4.5:1 in both themes, Lucide icons (`reply`), keyboard-complete, `prefers-reduced-motion`.
- Every new core mutation goes through the existing core write path (`c.write`/`c.mutate`), stamps `UpdatedAt`/`UpdatedBy`, and fires `OnChange`.
- Never run `bin/awesome-reminder.exe` from its repo path. Use a copy under `%TEMP%` with `APPDATA` pointed at a temp dir.

## Review Focus

1. **The hotkey's own modifiers are still down when the copy is injected:** with Shift or Alt held, the source app would see `Ctrl+Shift+C` or `Ctrl+Alt+C`. The grab must wait for the hotkey's modifiers to be released, up to 300 ms, then send key-ups for any still down, before sending `Ctrl+C`. → Task 8 `TestWaitForReleaseSendsKeyUps`.
2. **An image or files on the clipboard:** no copy is injected, and the clipboard is byte-for-byte what it was. → Task 8 `TestCanRestoreRejectsImagesAndFiles`, plus manual row R3.
3. **Nudges around weekends:** a reply saved Friday 16:00 nudges Friday 18:00, then Monday 09:00, not Saturday. One saved Saturday nudges 2 h later, then Monday 09:00. → Task 6 `TestNudgeSchedule` rows.
4. **Asleep through several nudges or a sweep:** one missed card per item (the latest nudge), not one per nudge; a sweep missed yesterday never shows today. → Task 6 `TestNudgesCollapseWhenMissed`, Task 7 `TestSweepSameDayOnly`.
5. **No person guessed, or the person cleared later:** the item stays a reply owed (listed under "Someone", still nudged). → Task 5 `TestReplyWithoutPersonStaysReply`.

---

### Task 1: Model fields + store v4

**Files:**
- Modify: `internal/model/item.go`, `internal/store/migrate.go`, `internal/store/store.go` (`Put`, `itemCols`, the scanner), `internal/store/store_test.go`

**Interfaces:**
- Produces:
  - On `model.Item`: `Reply bool json:"reply"`, `OwedTo string json:"owedTo"`, `Source string json:"source"`, `ReplyOutcome string json:"replyOutcome"`.
  - `model.ReplyReplied = "replied"`, `model.ReplyDropped = "dropped"`; `""` while open.
  - Migration v4: `ALTER TABLE items ADD COLUMN reply INTEGER NOT NULL DEFAULT 0; ... owed_to TEXT NOT NULL DEFAULT ''; ... source TEXT NOT NULL DEFAULT ''; ... reply_outcome TEXT NOT NULL DEFAULT ''`.

- [ ] **Step 1: Write failing tests** in `internal/store/store_test.go`:
  - `TestMigrateV3ToV4`: open a v3 fixture DB (build it with the v3 migrations, insert one item and the settings), migrate, and assert the item and settings are unchanged, `Reply == false`, `OwedTo == ""`, and `schema_version == "4"`.
  - `TestPutGetReplyFields`: `Put` an item with `Reply: true, OwedTo: "Priya", Source: "Teams", ReplyOutcome: ""`; `Get` returns the same fields. Then set `ReplyOutcome: model.ReplyDropped`, `Put`, `Get` and compare.
- [ ] **Step 2:** Run `go test ./internal/store/ -run 'TestMigrateV3ToV4|TestPutGetReplyFields' -v`. Expected: FAIL (unknown fields or columns).
- [ ] **Step 3:** Add the fields, the v4 migration entry, and the columns to `Put`'s INSERT/UPSERT, `itemCols` and the scan.
- [ ] **Step 4:** Run `go test ./internal/store/ ./internal/core/ -v`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(store): v4 reply fields on items`.

---

### Task 2: Sync the new fields (Phase 2 mapping)

**Files:**
- Modify: `internal/cloudsync/atlas/docs.go`, `internal/cloudsync/atlas/atlas_test.go`, `internal/cloudsync/cloudsynctest/conformance.go`

**Interfaces:**
- Consumes: Task 1 fields. Phase 2's item document mapping and conformance suite (use their existing function names).
- Produces: Atlas item documents carry `reply`, `owed_to`, `source` and `reply_outcome`, with the same last-writer-wins rule as every other item field.

- [ ] **Step 1:** Extend the existing item-document round-trip test in `atlas_test.go` with the four fields set (`true`, `"Priya"`, `"Teams"`, `"dropped"`), and add a conformance case "reply fields round-trip" that pushes such an item on device A and asserts device B receives identical fields.
- [ ] **Step 2:** Run `go test ./internal/cloudsync/... -v`. Expected: FAIL on the new assertions.
- [ ] **Step 3:** Map the four fields in `docs.go` (both directions); a document without them reads as the zero values.
- [ ] **Step 4:** Run `go test ./internal/cloudsync/... -v`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(sync): carry reply fields to Atlas`.

---

### Task 3: Settings

**Files:**
- Modify: `internal/settings/settings.go`, `internal/settings/settings_test.go`, `frontend/bindings/**` (regenerate with `wails3 generate bindings`), `frontend/src/api.ts` (the `Settings` interface)

**Interfaces:**
- Produces on `settings.Settings`:
  - `ReplyHotkey string json:"replyHotkey"`: per device; default from `Defaults(goos)`: `"Ctrl+Alt+R"`, or `"Ctrl+Option+R"` on darwin.
  - `WorkDays []string json:"workDays"`: lowercase weekday names; default `["monday","tuesday","wednesday","thursday","friday"]`.
  - `EndOfDay string json:"endOfDay"`: `"HH:MM"`, default `"17:30"`.
  - `EndOfDaySweep bool json:"endOfDaySweep"`: default `true`.
  - `WorkDays`, `EndOfDay` and `EndOfDaySweep` are added to `settings.Shared`, its `Equal`, `Shared()` and `WithShared`.
  - `func (s Settings) IsWorkDay(d time.Weekday) bool`.
- Validation (`Validate`): `ReplyHotkey` parses as a combo and is not equal to `Hotkey` → error `"Pick a different key from the capture hotkey"`; `EndOfDay` is `HH:MM`; `WorkDays` is non-empty, with valid names.

- [ ] **Step 1: Write failing tests:**
  - `TestDefaultsReplySettings` (windows and darwin values above).
  - `TestLoadSettingsMissingReplyFieldsGetDefaults`: JSON saved before this feature gets the four defaults.
  - `TestValidateReplyHotkeySameAsCapture`.
  - `TestValidateEndOfDayAndWorkDays`: `"25:00"`, `[]` and `["funday"]` are rejected.
  - `TestSharedIncludesReplySchedule`: changing `EndOfDay` changes `Shared()`; changing `ReplyHotkey` does not.
  - `TestIsWorkDay`.
- [ ] **Step 2:** Run `go test ./internal/settings/ -v`. Expected: FAIL.
- [ ] **Step 3:** Implement, using pointer wire fields for the defaults, the same pattern as `ShowPendingCount`.
- [ ] **Step 4:** Run `go test ./internal/settings/ ./internal/core/ -v`, then `npx tsc --noEmit -p frontend`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(settings): reply hotkey, working days, end of day`.

---

### Task 4: Person and source guessing

**Files:**
- Create: `internal/reply/guess.go`, `internal/reply/guess_test.go`

**Interfaces:**
- Produces:
  - `type Meta struct { OwedTo, Source, Quote string }`
  - `func Guess(text, process, title string) Meta`: `Quote` is `text` trimmed of surrounding whitespace.
  - `func SourceName(process string) string`: `OUTLOOK.EXE`→`Outlook`, `ms-teams.exe`/`Teams.exe`→`Teams`, `slack.exe`→`Slack`, `chrome.exe`→`Chrome`, `msedge.exe`→`Edge`, `firefox.exe`→`Firefox`, `thunderbird.exe`→`Thunderbird`; macOS bundle names `Microsoft Outlook`, `Microsoft Teams`, `Slack`, `Mail`, `Safari`; else the process name without its extension.
  - `func Line(m Meta) string`: `Reply to <person>: <first 200 chars of quote, one line>`, or `Reply: <quote…>` without a person, or `Reply to <person>` without a quote.
  - `func QuoteMarkdown(text string) string`: each line prefixed `> `.
- Person order: a `From: Name <addr>` / `From: Name` line in the text; a chat header line `Name  10:42 AM` / `Name, 10:42` / `Name — 10:42`; title patterns `Chat | <name> | Microsoft Teams`, `<name> | Microsoft Teams`, `<name> (DM) - <ws> - Slack`, `<name> - <ws> - Slack`, an Outlook message window `<subject> - Message (HTML)` (no person); else `""`.

- [ ] **Step 1: Write the failing table test** `TestGuess` with rows:

| text | process | title | want OwedTo | want Source |
|---|---|---|---|---|
| `"From: Priya Sharma <p@x.com>\nSent: …\ncan you check"` | `OUTLOOK.EXE` | `Inbox - me@x.com - Outlook` | `Priya Sharma` | `Outlook` |
| `"Sam Lee  10:42 AM\nship it?"` | `ms-teams.exe` | `Chat \| Ops \| Microsoft Teams` | `Sam Lee` | `Teams` |
| `"deploy?"` | `ms-teams.exe` | `Chat \| Priya Sharma \| Microsoft Teams` | `Priya Sharma` | `Teams` |
| `"deploy?"` | `slack.exe` | `Priya (DM) - acme - Slack` | `Priya` | `Slack` |
| `"hello"` | `chrome.exe` | `Inbox (3) - Gmail` | `""` | `Chrome` |
| `""` | `notepad.exe` | `x` | `""` | `notepad` |

  Also `TestLine` (person and quote; no person; no quote; a quote of 500 chars is cut to 200 with `…`; newlines become spaces) and `TestQuoteMarkdown`.
- [ ] **Step 2:** Run `go test ./internal/reply/ -v`. Expected: FAIL (package missing).
- [ ] **Step 3:** Implement with precompiled regexps.
- [ ] **Step 4:** Run `go test ./internal/reply/ -v`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(reply): guess person and source`.

---

### Task 5: Core — capture, drop, view

**Files:**
- Modify: `internal/core/core.go`, `internal/core/setters.go`, `internal/core/view.go`, `internal/core/core_test.go`, `internal/core/setters_test.go`

**Interfaces:**
- Consumes: Task 1 fields; `reply.Meta`, `reply.QuoteMarkdown` (Task 4).
- Produces:
  - `func (c *Core) CaptureReply(input string, m reply.Meta) (model.Item, error)`: parses `input` like `Capture` (tags, priority, due); sets `Reply=true`, `OwedTo=m.OwedTo`, `Source=m.Source`, `Description=reply.QuoteMarkdown(m.Quote)` when the quote is non-empty.
  - `func (c *Core) SetOwedTo(id, person string) (model.Item, error)`: trims; an empty value keeps `Reply=true`.
  - `func (c *Core) DropReply(id string) (model.Item, error)`: done with `ReplyOutcome=dropped`; error `"Not a reply"` for a non-reply.
  - `Done(id)` on a reply sets `ReplyOutcome=replied`; `Reopen` clears it.
  - `type PersonGroup struct { Person string json:"person"; Items []model.Item json:"items" }`
  - `View.Waiting []PersonGroup json:"waiting"`: open replies only, never also in `Upcoming` or `Tags`. Items oldest `CreatedAt` first within a group; groups ordered by their oldest item; `Person` is the trimmed `OwedTo` (matched case-insensitively, shown as first seen); `""` sorts last.
  - `PendingCount` still counts open replies (no change; assert it).

- [ ] **Step 1: Write failing tests:**
  - `TestCaptureReply`: `CaptureReply("Reply to Priya: deploy? !high", Meta{"Priya","Teams","can you check\nthe deploy"})` gives `Reply`, `OwedTo`, `Source`, `Priority=="high"`, `Description=="> can you check\n> the deploy"`, and `OnChange` fired.
  - `TestReplyWithoutPersonStaysReply` (Review Focus 5): capture with an empty `OwedTo` → in `View.Waiting` under `Person==""`; `SetOwedTo(id,"Sam")` then `SetOwedTo(id,"")` → still in `Waiting`.
  - `TestViewWaitingGroups`: three replies (Priya at 10:00, Sam at 09:00, priya at 11:00) plus a normal todo → groups `[Sam, Priya(2)]`; the todo only in `Tags`.
  - `TestDoneAndDropReply`: `Done` → outcome `replied`; `DropReply` → status done, outcome `dropped`; `Reopen` → outcome `""`; `DropReply` on a todo → error.
- [ ] **Step 2:** Run `go test ./internal/core/ -v`. Expected: FAIL.
- [ ] **Step 3:** Implement through `c.write`/`c.mutate`.
- [ ] **Step 4:** Run `go test ./internal/core/ ./internal/export/ -v`. Expected: PASS (export unchanged so far).
- [ ] **Step 5:** Commit `feat(core): reply capture, drop, Waiting view`.

---

### Task 6: Scheduler — nudges

**Files:**
- Modify: `internal/scheduler/plan.go`, `internal/scheduler/scheduler.go`, `internal/scheduler/plan_test.go`

**Interfaces:**
- Consumes: Task 1 fields; `settings.IsWorkDay` (Task 3).
- Produces:
  - `type Rules struct { Leads []time.Duration; WorkDays func(time.Weekday) bool; DefaultTime string; EndOfDay string; Sweep bool; Loc *time.Location }`
  - `Plan(items []model.Item, r Rules, now time.Time, notified func(key string) bool) (fireNow []Alert, covered []string, next time.Time)`: the `defaultLeads` parameter becomes `r.Leads`; update callers.
  - `scheduler.New` takes `func() Rules` in place of the leads func.
  - New `Alert` fields: `Kind string json:"kind"` (`""` normal, `"nudge"`, `"sweep"`), `WaitingSince time.Time json:"waitingSince"`, `People []string json:"people"` (sweep only).
  - Nudge keys: `"<itemID>|nudge|<unix>"`.
- Nudge times for an open `Reply` item (in `r.Loc`):
  - Without a due time: `CreatedAt+2h` (any day), then the first working day strictly after that instant's date at `DefaultTime`, then each following working day at `DefaultTime`.
  - With a due time: the normal alerts, then the first working day after the due date at `DefaultTime`, and daily after.
  - As with other alerts, only the latest due candidate per item fires, marked missed when more than 2 minutes late.

- [ ] **Step 1: Write failing tests:**
  - `TestNudgeSchedule` (Review Focus 3), local zone `Asia/Kolkata`, rows of (created, want successive nudge times):
    - Wed 10:00 → Wed 12:00, Thu 09:00, Fri 09:00
    - Fri 16:00 → Fri 18:00, Mon 09:00
    - Sat 11:00 → Sat 13:00, Mon 09:00
    - Wed 23:30 → Thu 01:30, Fri 09:00
  - `TestNudgeAfterDue`: due Wed 17:00 with leads [1h] → Wed 16:00, Wed 17:00, Thu 09:00.
  - `TestNudgesCollapseWhenMissed` (Review Focus 4): created Mon 10:00, now Thu 15:00, nothing notified → exactly one alert, the Thu 09:00 nudge, `Missed==true`.
  - `TestNoNudgeWhenClosed`: done and dropped replies yield nothing.
- [ ] **Step 2:** Run `go test ./internal/scheduler/ -v`. Expected: FAIL.
- [ ] **Step 3:** Implement nudge candidates alongside the existing ones; `next` includes the next nudge time.
- [ ] **Step 4:** Run `go test ./internal/scheduler/ ./internal/app/ -v`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(scheduler): reply nudges on working days`.

---

### Task 7: Scheduler and app — end-of-day sweep, alert cards, service methods

**Files:**
- Modify: `internal/scheduler/plan.go`, `internal/scheduler/plan_test.go`, `internal/app/alerts.go`, `internal/app/alerts_test.go`, `internal/app/service.go`, `internal/app/service_test.go`, `frontend/bindings/**`

**Interfaces:**
- Consumes: Task 6 `Alert.Kind`/`People`/`WaitingSince`; Task 5 `DropReply`.
- Produces:
  - Sweep: when `r.Sweep` and today is a working day and `now ≥ today EndOfDay`, and some open reply exists, an `Alert{Kind:"sweep", Key:"sweep|YYYY-MM-DD", People: distinct OwedTo of open replies oldest first, "" shown as "Someone"}`. A sweep for an earlier date is never produced.
  - `AlertCard` gains `Kind`, `Person` (= item `OwedTo`), `Source`, `WaitingSince`, `People`.
  - `func (s *Service) DropReply(id string) error`
  - `func (s *Service) SnoozeReplies() error`: snoozes every open reply to the next working day at `DefaultTime` (the sweep's **Tomorrow**).
  - `func (s *Service) CaptureReply(input string, m reply.Meta) (model.Item, error)`

- [ ] **Step 1: Write failing tests:**
  - `TestSweepAtEndOfDay`: Wed 17:30 with two replies (Priya, "") → one sweep alert, `People==["Priya","Someone"]`; Wed 17:29 → none; Sat 17:30 → none; `Sweep:false` → none; no replies → none.
  - `TestSweepSameDayOnly` (Review Focus 4): asleep from Wed 17:00 to Thu 08:00 → no sweep on Thu morning; asleep from Wed 17:00 to Wed 22:00 → the sweep fires, missed.
  - `TestAlertCardCarriesReplyFields` in `alerts_test.go`.
  - `TestServiceSnoozeReplies`: two replies snoozed to Thu 09:00 when called on Wed; a normal reminder untouched.
- [ ] **Step 2:** Run `go test ./internal/scheduler/ ./internal/app/ -v`. Expected: FAIL.
- [ ] **Step 3:** Implement; regenerate bindings.
- [ ] **Step 4:** Run `go test ./... -v`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: end-of-day sweep and reply alert cards`.

---

### Task 8: Selection grab — Windows

**Files:**
- Create: `internal/grab/grab.go` (types, policy, fallback), `internal/grab/grab_test.go`, `internal/grab/grab_windows.go`, `internal/grab/grab_other.go` (`//go:build !windows && !darwin && !linux` returns the fallback only)

**Interfaces:**
- Produces:
  - `type Key uint16`: a modifier key as a Windows virtual-key code (`VK_SHIFT` 0x10, `VK_CONTROL` 0x11, `VK_MENU` 0x12, `VK_LWIN` 0x5B). macOS and Linux ignore it.
  - `type Result struct { Text, Process, Title string; Grabbed bool }`: `Grabbed` is false when `Text` came from the fallback or is empty.
  - `func Grab(ctx context.Context, mods []Key) Result`: `mods` are the reply hotkey's modifier keys, released before injecting.
  - `func canRestore(formats []uint32, readable func(uint32) bool) bool`: false if any format is in the Global Constraints list, or not readable.
  - `func fallbackQuote(text string) (string, bool)`: true for non-empty text of at most 2,000 characters.
  - `func waitForRelease(down func(Key) bool, keyUp func(Key), mods []Key, timeout time.Duration, sleep func(time.Duration))`: polls every 10 ms up to `timeout` (300 ms), then sends key-ups for any still down.
- Windows sequence: `GetForegroundWindow` → process image name (`QueryFullProcessImageNameW`, base name) and title; `waitForRelease`; open the clipboard, enumerate formats, `canRestore`; if false, skip to the fallback. Otherwise save each format's bytes (`GlobalLock`/`GlobalSize`), note `GetClipboardSequenceNumber`, `SendInput` Ctrl down, C down, C up, Ctrl up; poll the sequence number every 10 ms up to 150 ms. If it changed, read `CF_UNICODETEXT`, then restore all saved formats plus the three exclusion formats. If unchanged: `Grabbed=false`, use the fallback, nothing restored.

- [ ] **Step 1: Write failing tests** (pure functions, all OSes):
  - `TestCanRestoreRejectsImagesAndFiles` (Review Focus 2): `[13 /*CF_UNICODETEXT*/, 0xC0A1]` → true; adding each of 15, 2, 8, 17, 14, 3 → false; a format whose `readable` is false → false.
  - `TestFallbackQuote`: `""` → false; 2,000 chars → true; 2,001 → false.
  - `TestWaitForReleaseSendsKeyUps` (Review Focus 1): Shift stays down for the whole timeout → exactly one `keyUp(Shift)`; Ctrl released after 30 ms → no key-up for Ctrl; returns within the timeout given a fake sleep.
- [ ] **Step 2:** Run `go test ./internal/grab/ -v`. Expected: FAIL.
- [ ] **Step 3:** Implement `grab.go` and `grab_windows.go` with `golang.org/x/sys/windows` lazy procs (same style as `internal/app/taskbar_windows.go`, including `runtime.KeepAlive` after calls that pass pointers as `uintptr`).
- [ ] **Step 4:** Run `go test ./internal/grab/ -v` and `go vet ./...`. Expected: PASS. Manual check from a temp copy: select text in Notepad, call `Grab` through a tiny test binary, or wait for Task 10. The text comes back, and the clipboard has what it held before.
- [ ] **Step 5:** Commit `feat(grab): Windows selection grab with clipboard restore`.

---

### Task 9: Selection grab — macOS and Linux

**Files:**
- Create: `internal/grab/grab_darwin.go` (cgo), `internal/grab/grab_linux.go`

**Interfaces:**
- Consumes: Task 8 `Result`, `fallbackQuote`, `canRestore` (on macOS, apply it to pasteboard types: refuse `public.file-url`, `public.tiff`, `public.png`).
- Produces: `Grab` for darwin and linux, plus `func AccessibilityTrusted() bool` (darwin; elsewhere always true) for the inline hint.
- macOS: frontmost app name via `NSWorkspace.frontmostApplication`, title via `AXFocusedWindow` → `AXTitle`; selection via `AXFocusedUIElement` → `AXSelectedText` when `AXIsProcessTrusted()`. If that is empty: save pasteboard items, `changeCount`, post Cmd+C with `CGEventPost`, poll `changeCount` up to 150 ms, read, restore. Untrusted: fallback only.
- Linux: process and title from `_NET_ACTIVE_WINDOW` via `xprop` if present; selection from `xclip -o -selection primary` or `xsel -o -p` (X11), or `wl-paste --primary --no-newline` (Wayland), each with a 150 ms timeout; nothing injected and the clipboard never written; fallback `xclip -o -selection clipboard` / `wl-paste`.

- [ ] **Step 1:** Add `TestLinuxPrimaryCommandOrder` (build tag linux) for the pure helper `primaryCommands(env func(string) string, look func(string) (string, error)) [][]string`: on Wayland (`WAYLAND_DISPLAY` set) `wl-paste` comes first; on X11 `xclip`, then `xsel`; with neither installed, empty.
- [ ] **Step 2:** Run `GOOS=linux go vet ./internal/grab/` (cross-vet works because `internal/grab` has no cgo on linux). Expected: the test file compiles; on a Linux box the test fails before Step 3.
- [ ] **Step 3:** Implement both files. `grab_darwin.go` is reviewed by reading (no macOS machine), as for `dock_darwin.go`.
- [ ] **Step 4:** Run `go build ./...` and `GOOS=linux go vet ./internal/grab/`. Expected: clean.
- [ ] **Step 5:** Commit `feat(grab): macOS accessibility and Linux primary selection`.

---

### Task 10: App wiring — second hotkey, reply open, `--reply`

**Files:**
- Modify: `internal/app/app.go`, `internal/app/service.go` (`Status`, `SetHotkeyRecording`, `SaveSettings`), `internal/app/windows.go` (`ShowReply`), `internal/app/events.go`, `internal/app/launch.go`, `internal/app/badge_wiring.go` (tooltip), `internal/app/service_test.go`, `frontend/bindings/**`, `frontend/src/api.ts`

**Interfaces:**
- Consumes: `grab.Grab`, `reply.Guess`/`reply.Line`/`reply.SourceName`, `Service.CaptureReply` (Task 7), `settings.ReplyHotkey` (Task 3).
- Produces:
  - `const EventReplyOpen = "reply:open"`, payload `ReplyPrefill{ Line, OwedTo, Source, Quote string; Grabbed bool; NeedsAccess bool }`.
  - `func (w *wailsWindows) ShowReply(p ReplyPrefill)`: emits the event, then shows the capture box as `ShowCapture` does.
  - `Status.ReplyHotkeyErr string`; the tray tooltip appends `"reply hotkey error: …"` like the capture one.
  - `--reply` command-line flag (second instance and first launch), handled like `--capture`.
  - `func replyMods(c hotkey.Combo) []grab.Key` in `internal/app`: the combo's modifiers as virtual-key codes, for `grab.Grab`.
  - `SetHotkeyRecording(true)` suspends both managers; saving settings re-registers the reply hotkey and reports its error.
- Press handler: runs on its own goroutine, never the Wails main thread. `grab.Grab(ctx, mods)` → `reply.Guess` → `ShowReply`. The whole handler is skipped while the capture box is already visible in reply mode, so a double press doesn't grab twice.

- [ ] **Step 1: Write failing tests:**
  - `TestReplyPrefillFromGrab`, with a fake grab result (Teams title, text) → `Line=="Reply to Priya Sharma: deploy?"`, `Source=="Teams"`.
  - `TestSaveSettingsReportsReplyHotkeyError`, with a fake registrar failing for the reply combo → `Status().ReplyHotkeyErr` set, and capture hotkey unaffected.
  - `TestHotkeyRecordingSuspendsBoth`.
  - `TestLaunchReplyFlag` in `launch_test.go`.
- [ ] **Step 2:** Run `go test ./internal/app/ -v`. Expected: FAIL.
- [ ] **Step 3:** Implement; keep the press handler testable by passing `grab` as a function dependency.
- [ ] **Step 4:** Run `go test ./... -v` and `wails3 build`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(app): reply hotkey opens the capture box in reply mode`.

---

### Task 11: Capture box reply mode

**Files:**
- Modify: `frontend/src/capture.ts`, `frontend/src/icons.ts` (`reply`), `frontend/src/style.css`, `frontend/src/tokens.css` (only if a new token is needed), `frontend/src/list-demo.ts` (a demo trigger for reply mode)
- Create: `frontend/src/reply-mode.ts`, `frontend/src/reply-mode.test.ts`

**Interfaces:**
- Consumes: `EventReplyOpen` / `ReplyPrefill`, `api.captureReply(input, meta)` (Task 10 bindings).
- Produces, in `reply-mode.ts`:
  - `export interface ReplyState { owedTo: string; source: string; quote: string }`
  - `export function nudgeSummary(created: Date, settings: Pick<Settings,"defaultTime"|"workDays">, due: Date | null): string`: `"nudge in 2h, then 9:00"` / `"alerts before 17:00, then daily 9:00"`.
  - `export function personLabel(owedTo: string): string`: `""` → `"Someone"`.
- Behaviour: on `reply:open` the box enters reply mode. The input holds `Line`, the lead icon is `reply`, and the preview row shows a "Reply owed" chip, `for <person> · from <source> · <nudgeSummary>`. `Alt+P` or a click on the person chip opens an inline person input (Enter or Esc returns to the line). `Tab` still opens the time picker. Enter calls `captureReply`. Esc leaves reply mode and hides. The next normal hotkey opens a clean normal box. With `NeedsAccess`, one inline line explains the macOS Accessibility setting. The hint row reads `↵ save  Alt+P person  Tab time  Esc cancel`.

- [ ] **Step 1: Write failing vitest tests** in `reply-mode.test.ts`: the `nudgeSummary` rows (Wed 10:00 → "nudge in 2h, then 9:00"; with a due time → the second form); `personLabel("")`. Also a jsdom test that mounts the capture box, dispatches a fake `reply:open`, presses `Alt+P`, types "Sam", presses Enter, then Enter again, and asserts `captureReply` was called with `owedTo:"Sam"`.
- [ ] **Step 2:** Run `npx vitest run src/reply-mode.test.ts` in `frontend/`. Expected: FAIL.
- [ ] **Step 3:** Implement. The mic keeps working; the reply chip reuses the chip styles.
- [ ] **Step 4:** Run `npx tsc --noEmit -p .` and `npx vitest run`. Expected: PASS. Preview in the demo harness in light and dark themes.
- [ ] **Step 5:** Commit `feat(capture): reply mode`.

---

### Task 12: List, panel and alert cards

**Files:**
- Modify: `frontend/src/list.ts`, `frontend/src/item-row.ts`, `frontend/src/panel/panel.ts`, `frontend/src/panel/fields.ts`, `frontend/src/alert.ts`, `frontend/src/api.ts`, `frontend/src/style.css`, `frontend/src/list-demo.ts`
- Create: `frontend/src/waiting.ts`, `frontend/src/waiting.test.ts`

**Interfaces:**
- Consumes: `View.Waiting`, `PersonGroup`, `Item.reply/owedTo/source/replyOutcome`, `Service.DropReply`, `Service.SnoozeReplies`, `AlertCard.kind/person/waitingSince/people`.
- Produces, in `waiting.ts`:
  - `export function ageLabel(created: Date, now: Date): string`: `"5 min"`, `"2 h"`, `"yesterday"`, `"3 days"`.
  - `export function isStale(created: Date, now: Date): boolean`: older than 24 h, which renders in `--time-text`.
- List: a "Waiting on you (N)" section first, with person subheadings (`personLabel`) and rows showing source and age. Space = replied; the row menu gains "No reply needed". Done rows with `replyOutcome=="dropped"` show "no reply needed" in `--fg-muted`.
- Panel: for replies, "For" (editable, calls `SetOwedTo`) and "From" (read-only) fields above the description; footer adds "No reply needed".
- Alert cards: `kind=="nudge"` uses the time line `waiting <ageLabel> · <person>` and the button label **Replied**. `kind=="sweep"` shows "N people are still waiting on you — names" with **Open list** (`OpenList("")`) and **Tomorrow** (`SnoozeReplies`).

- [ ] **Step 1: Write failing tests:** `waiting.test.ts` for the `ageLabel` and `isStale` rows. Extend the list tests: a view with `waiting` renders the section first, and a reply never shows twice. Extend the alert tests: a sweep card's Tomorrow calls `snoozeReplies`.
- [ ] **Step 2:** Run `npx vitest run`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `npx tsc --noEmit -p .` and `npx vitest run`. Expected: PASS. Demo-harness screenshots in light and dark.
- [ ] **Step 5:** Commit `feat(ui): Waiting on you section, reply fields, nudge and sweep cards`.

---

### Task 13: Settings UI

**Files:**
- Modify: `frontend/src/settings.ts`, `frontend/src/style.css`, `frontend/src/list-demo.ts` (settings demo defaults)

**Interfaces:**
- Consumes: Task 3 fields; `SaveSettings` errors (`"Pick a different key from the capture hotkey"`).
- Produces: a "Replies" group after the hotkey field: a reply hotkey recorder (the same component as the capture hotkey; the recording state suspends both keys), working-day toggles (Mon–Sun chips, `aria-pressed`), an end-of-day time input, and an "End-of-day sweep" switch, with inline errors.

- [ ] **Step 1:** Extend the settings tests: draft round-trip of the four fields; clearing every work day shows "Pick at least one working day" and disables Save.
- [ ] **Step 2:** Run `npx vitest run`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `npx tsc --noEmit -p .` and `npx vitest run`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(settings): Replies section`.

---

### Task 14: Export, docs, checklist

**Files:**
- Modify: `internal/export/markdown.go`, `internal/export/markdown_test.go` (+ golden file in `testdata`), `README.md`, `docs/manual-test-checklist.md`, `docs/superpowers/specs/2026-11-27-ui-design-guide.md`

**Interfaces:**
- Consumes: `View.Waiting`.
- Produces: `todo.md` starts with `## Waiting on you` and lines `- [ ] Reply to Priya: … (from Teams)`, before the other sections. README: a "Replies owed" section covering the key, the grab, clipboard history (the selection is copied, so it appears in Win+V like a manual copy), nudges, the sweep, settings, and the macOS Accessibility note. Checklist rows R1–R6 below. Design guide: reply mode, the Waiting section, the nudge and sweep cards.

- [ ] **Step 1:** Update the golden test with a view containing two replies. Expected: FAIL.
- [ ] **Step 2:** Implement the export section; run `go test ./internal/export/ -v`. Expected: PASS.
- [ ] **Step 3:** Write the docs, checking every claim against the code.
- [ ] **Step 4:** Manual run from a temp copy on Windows, recording each row's result:
  - R1: select text in Outlook, Teams, Slack and Chrome, then press the key: the box opens pre-filled within ~200 ms.
  - R2: the clipboard holds exactly its previous text, and its previous HTML content, afterwards.
  - R3: with an image on the clipboard, no copy is injected and the image is still there.
  - R4: with the reply key `Ctrl+Alt+R`, Outlook's Reply All (`Ctrl+Shift+R`) still works.
  - R5: nudges and the sweep fire on time (clock or seeded data).
  - R6: idle CPU ~0 with the feature on.
- [ ] **Step 5:** Commit `docs: replies owed`.
