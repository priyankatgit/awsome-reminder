# Awesome Reminder — Design (v1: Quick Capture + To-do + Reminders + Sync)

## Context / why
Developers keep to-dos in Notepad and lose reminder-worthy thoughts mid-flow. Goal: a free, open-source, cross-platform (Windows / macOS / Linux) desktop app that replaces that notepad. A hotkey opens a Spotlight-style box, you type plain English ("remind me tomorrow 5pm check deploy #backend"), press Enter, and you're back to work. A tray icon opens a simple list grouped by hashtag. Reminders pop up on the desktop, optionally buzz your phone, and your list follows you across machines.

**User's hard constraints:** zero cost (no paid AI, servers, or services), CPU-only, light on RAM/CPU (dev machine — leave room for real work), radically simple (not Todoist/Asana).

## Goals (v1)
1. Global hotkey → centered capture box → Enter saves a **to-do** (no time) or a **reminder** (has a time). `#hashtags` group items; untagged → `#inbox`.
2. Tray icon → list window: **Today / Upcoming** reminders + one section per hashtag. Tick done, inline edit, snooze, delete, retag, search; done items fold into "Done".
3. Desktop reminder pop-ups at configurable lead times (default 1h + 10m before, plus at due time) with Done / Snooze (10m, 1h, tomorrow) / Open.
4. **Optional** phone buzz via **ntfy** (free open-source push app; no sign-in; works with laptop closed for alerts within 3 days).
5. Same list on 2+ machines and full restore on a new machine via the user's **own free MongoDB Atlas (M0)** cluster.

## Non-goals (v1) — deferred, each a separate design later
- **Google/Outlook calendar integration** (reading meetings or writing reminders). Decided for later: support *multiple accounts* (read all, one target calendar for writes); prefer no-sign-in private ICS links for reading; sign-in/OAuth only if writing to calendar.
- **Phone input** (add tasks from phone with no laptop on). Leading option recorded: Telegram bot + user's own free Cloudflare Worker (parses, confirms, stores inbox, schedules ntfy); laptops pull the inbox on sync. Alternatives: email-to-self (Gmail IMAP), ntfy inbox topic.
- Voice / "Hey Awesome" wake word; meeting transcription → to-dos (see Roadmap).
- Teams/sharing, projects/priorities/subtasks, non-English date parsing, a mobile app.

## Anti-goals (failure even if it "works")
- Capture slower than opening Notepad: popup visible < 150 ms; saving never waits on the network.
- Default hotkey steals keys devs need (Ctrl+Space = IDE autocomplete).
- Noticeable idle memory/CPU.
- Duplicate phone alerts, or lost items when offline.
- Anything that costs the user money.

## Approaches considered (user picked A)
| | Approach | Trade-off |
|---|---|---|
| **A ✅** | **Go + Wails v3** (Go core, small web UI in the OS webview) | Polished Spotlight-like popup, native tray, multi-window, ~15 MB binary. Risk: v3 is new (beta through 2026; recent Windows tray fixes). |
| B | Go + Fyne | Pure Go; plainer look, frameless centered popup awkward. |
| C | Headless Go daemon + separate UI process | Most modular; two processes to install — too heavy for v1. |
Electron (RAM) and Tauri (Rust; user prefers Go) ruled out.

**Storage (user picked local SQLite + Atlas):** Markdown file in a cloud-drive folder and Google Tasks / MS To Do were considered; user chose their own free Atlas cluster for multi-machine + restore.
**Phone buzz (user picked ntfy, optional):** calendar-as-alarm deferred with calendar integration; Telegram can't schedule; email too weak.

## Decisions (user-decided unless marked *[my call]*)
- **Stack:** Go + Wails v3. Frontend lightweight (Svelte or vanilla TS) *[builder's choice]*.
- **Hotkey:** configurable; default **Ctrl+Shift+Space** (⌘⇧Space on macOS) *[my call]*.
- **Date understanding:** rule-based Go parser, English only, no AI *[my call]*. Must handle: "today 4pm", "tomorrow", "tomorrow 5pm", "next week" (= Monday 09:00, configurable), "in 2 hours", "on friday", "28 nov 3pm", "every monday 10am". Date without time → 09:00 (configurable) *[my call]*. Live preview under the input shows the interpretation (kind, time, tags, channels) before Enter; Tab opens a quick time edit.
- **Storage & sync:** local **SQLite** per machine is the working store (instant, offline). Background sync to the user's **own Atlas M0** (connection string pasted once in Settings, stored in OS keychain). **No client-side encryption** — TLS + dedicated DB user + strong password (user's choice). Sync is behind a Go `Syncer` interface so other backends can be added later. Optional one-way `todo.md` export for grep/viewing.
- **Recurring reminders** *[my call]*: Done = complete this occurrence and advance `due_at` to the next; "Stop repeating" ends the series.
- **ntfy** *[my call on details]*: server URL (default ntfy.sh, self-host allowed) + random secret topic live in **synced settings** so any machine can schedule. High priority for a strong buzz.

## Architecture (single process, Wails v3)
```
Go core
├─ hotkey     global hotkey → show capture window; single-instance; `awesome --capture` CLI signals the running app
├─ parser     text → {kind, text, tags, due_at, tz, rrule}
├─ store      SQLite: items, settings, outbox, notify_log, sync_state
├─ sync       Syncer interface → AtlasSyncer (push outbox, pull by updated_at watermark)
├─ scheduler  single timer to next alert; fires desktop popup; catches up missed alerts on startup/wake
├─ push       NtfyPusher (optional): schedule ≤3 days ahead, cancel/replace on edit/done/delete
├─ secrets    OS keychain (Atlas URI)
├─ export     todo.md writer (optional)
└─ autostart  launch at login
UI windows: capture (frameless, centered, always-on-top, hides on Esc/blur) · list (from tray) · alert popup · settings
```

### Data model
**items (synced):** `id (ULID) · kind (todo|reminder) · text · tags[] · due_at (UTC) · tz · rrule · lead_times[] (null = settings default) · status (open|done|deleted tombstone) · done_at · snooze_until · created_at · updated_at · updated_by (device_id) · ntfy {msg_id, scheduled_for, claimed_by, claimed_at}`
**settings (synced doc):** hotkey, default lead times, default times, "next week" rule, ntfy server/topic/enabled, export path.
**Local only:** device_id, Atlas URI (keychain), sync watermark, outbox, notify_log.

### Sync rules
- Every write goes to SQLite first, then into the outbox. Capture never blocks on the network.
- Push the outbox when online; pull `updated_at > watermark − skew window`. Conflicts: per-record last-writer-wins on `updated_at`, tie broken by device_id. Deletes are tombstones.
- Desktop pop-ups appear on every running machine; Done/Snooze on one syncs to the others.
- With no Atlas configured, the app works fully as single-machine.

### ntfy without duplicates
- A machine schedules an ntfy alert only after **atomically claiming** it in Atlas (`findOneAndUpdate` where `ntfy.scheduled_for` ≠ target and claim is empty/stale) → two laptops never both schedule the same alert. Without Atlas, the local machine just schedules.
- Only alerts due within 3 days are scheduled (ntfy.sh limit); each online machine checks periodically, so far-future and recurring occurrences get scheduled once they enter the window. Known limit: if no machine is online during an alert's 3-day window, the phone won't buzz for it — shown in Settings help.
- Edit/done/delete → cancel/replace the scheduled ntfy message.

## Constraints & known risks
- **Resources:** idle RAM target < 150 MB incl. webview, ~0% CPU; destroy the list window when closed; one timer, no busy loops; sync poll ~30 s with backoff when idle/offline.
- **Linux Wayland:** no X11-style global hotkey → try the XDG GlobalShortcuts portal, else guide users to bind `awesome --capture` in desktop settings.
- **Atlas M0:** 512 MB (ample). Roaming laptops need network access from anywhere → setup guide mandates a dedicated DB user with a strong password. Free clusters can pause after long inactivity → app warns clearly. MongoDB retired Device Sync/Data API in 2025, hence our own sync.
- **Code signing costs money** (Apple $99/yr, Windows certs) → v1 ships unsigned via GitHub Releases; README explains Gatekeeper/SmartScreen; Homebrew cask / winget / scoop later.
- **Time zones/DST:** store UTC + original tz; recurrences expand in their tz.

## Delivery phases
1. **Local core:** hotkey + capture box + parser + SQLite + tray list + desktop alerts + autostart + md export. Usable daily on one machine.
2. **Atlas sync:** Settings → paste URI, outbox/pull sync, restore on a fresh machine.
3. **ntfy push:** settings toggle, claim-then-schedule, cancel/replace, test-notification button.

## Roadmap after v1 (direction only)
- **Calendar** (see Non-goals). **Phone input** (see Non-goals).
- **Voice ("Hey Awesome"):** `sherpa-onnx` (Apache-2.0, Go bindings, CPU) for keyword spotting + Silero VAD + ASR, or `whisper.cpp` (base.en ~150 MB / small ~500 MB) → same parser.
- **Meeting notes:** WASAPI loopback (Windows), ScreenCaptureKit (macOS 13+), PipeWire/Pulse monitor (Linux) + mic → local transcript → small local LLM via llama.cpp (Qwen-class 1.5–3B Q4, ~1–2 GB) for action items with #tags. Models downloaded on demand; LLM runs only when needed, threads capped (≤ half the cores), unloaded afterwards. Consent notice for recording others.

## Left to the builder
Frontend framework; exact libraries (candidates: `golang.design/x/hotkey`, `markusmobius/go-dateparser` or `olebedev/when` + custom rules, `teambition/rrule-go`, `modernc.org/sqlite`, `go.mongodb.org/mongo-driver/v2`, `zalando/go-keyring`); UI styling; packaging/installers; exact polling intervals within the stated budget.

## Verification
- **Parser:** table-driven tests (~50 phrases: relative/absolute times, "next week", recurrence, tags, no-time to-dos, DST edge) with a fixed clock.
- **Sync:** unit tests for LWW merge, tombstones, offline-queue replay; integration test with two simulated devices against a local `mongod` (and once against a real Atlas M0).
- **ntfy:** fake HTTP server asserting schedule / cancel / replace and claim-once across two devices; manual check that the phone buzzes with the laptop closed.
- **Manual per-OS checklist** (Win 11, macOS 14+, Ubuntu X11 + Wayland): hotkey latency, popup focus/centering, tray, autostart, missed-alert catch-up after sleep, measured idle RAM/CPU.
