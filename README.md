# Awesome Reminder

Awesome Reminder is a small desktop app for capturing to-dos and reminders without leaving what you are doing. Press a global hotkey, type a line such as `remind me tomorrow 5pm check deploy #backend`, and press Enter. The app works out whether it is a to-do or a timed reminder, stores it locally, and alerts you before it is due. It runs from the system tray, keeps everything in a local SQLite file, and needs no account or network. If you want the same list on several computers, it can optionally sync through your own MongoDB Atlas cluster. It is built with Go and Wails v3 and runs on Windows 11, macOS 14+ and Linux.

## Screenshots

Screenshots will be added here (capture box, tray list, alert pop-up, settings).

## Install

Download the build for your OS from the project's GitHub Releases page and run it. There is no installer step beyond that; the app starts in the tray and registers itself to start at login (this can be turned off in Settings).

The builds are not code-signed, so the OS will warn you the first time:

- **Windows:** SmartScreen shows "Windows protected your PC". Click **More info**, then **Run anyway**.
- **macOS:** Gatekeeper blocks the app. Right-click the app, choose **Open**, then confirm. Alternatively remove the quarantine flag:
  `xattr -dr com.apple.quarantine /Applications/Awesome\ Reminder.app`
- **Linux:** make the file executable (`chmod +x awesome-reminder`) and run it. You need a system tray that supports the StatusNotifier protocol (GNOME needs the AppIndicator extension).

## Usage

Press the global hotkey (default `Ctrl+Shift+Space`, `Cmd+Shift+Space` on macOS) to open the capture box, type a line, and press Enter. Esc closes the box without saving. The box shows a live preview of how the line was understood (to-do or reminder, due time, tags) before you save.

- A line with a date or time becomes a **reminder**; a line without one becomes a **to-do**.
- **`#tags`** anywhere in the line become tags. To-dos without a tag go to `#inbox`.
- **Quotes keep text literal.** Text in double quotes is never parsed for times, tags or prefixes: `"fix 5pm cron job" tomorrow #ops` saves the text `fix 5pm cron job` due tomorrow at the default time.
- **Tab** opens a date/time picker. Choosing a time rewrites the line with an explicit date and time, which is handy when the phrase you want is awkward to type.
- Leading filler such as `remind me to`, `remind me`, `set me a reminder for`, `reminder:` and `todo:` is removed.

Date and time may come in either order, and a leading `at`, `on`, `for` or `by` next to a phrase is dropped. Matching is case-insensitive. The examples below assume it is Wednesday 25 Nov 2026, 10:00, with the default settings.

| You type | Saved as |
|---|---|
| `remind me tomorrow 5pm check deploy #backend` | reminder "check deploy", Thu 26 Nov 17:00, tag `backend` |
| `set me reminder for standup notes today at 4 pm` | reminder "standup notes", today 16:00 |
| `call bob today` | reminder "call bob", today 11:00 (later of 09:00 and the next full hour) |
| `tomorrow pay rent` | reminder "pay rent", tomorrow 09:00 (default time) |
| `next week plan sprint` | reminder "plan sprint", Mon 30 Nov 09:00 (configurable) |
| `in 2 hours check build` | reminder "check build", today 12:00 |
| `in 30 min stretch` | reminder "stretch", today 10:30 |
| `in an hour coffee` | reminder "coffee", today 11:00 |
| `in 3 days follow up` | reminder "follow up", 28 Nov 10:00 (keeps the current clock) |
| `on friday 3pm demo` | reminder "demo", Fri 27 Nov 15:00 |
| `wednesday 9am retro` | reminder "retro", Wed 2 Dec 09:00 (9am has passed today) |
| `next wednesday sync` | reminder "sync", Wed 2 Dec 09:00 (never today) |
| `28 nov 3pm review` | reminder "review", 28 Nov 15:00 |
| `nov 28 at 15:30 review` | reminder "review", 28 Nov 15:30 |
| `1 jan taxes` | reminder "taxes", 1 Jan 2027 09:00 (a past date rolls to next year) |
| `2026-12-01 17:00 release` | reminder "release", 1 Dec 17:00 |
| `at 5 deploy` | reminder "deploy", today 17:00 (bare 1-7 means pm) |
| `at 9 standup` | reminder "standup", tomorrow 09:00 (9 has passed) |
| `at 17:30 ship` | reminder "ship", today 17:30 |
| `lunch at noon` | reminder "lunch", today 12:00 |
| `eod push branch` | reminder "push branch", today 17:00 |
| `tonight backup` | reminder "backup", today 20:00 |
| `Tomorrow 5PM Call` | reminder "Call", tomorrow 17:00 |
| `every monday 10am standup #team` | repeating weekly on Monday 10:00, first on Mon 30 Nov |
| `every day at 9am vitamins` | repeating daily 09:00, first tomorrow |
| `every weekday 9:30 standup` | repeating Mon-Fri 09:30, first tomorrow |
| `refactor auth #backend` | to-do "refactor auth", tag `backend` |

If the resulting time is already in the past the preview warns "time is in the past". Bare-hour rule: 1-7 is pm, 8-12 is as written, 13-23 is 24-hour.

As soon as the line contains a due time, the icon at the left of the box turns into a light-red bell and a "Reminder · Thu 26 Nov, 17:00" badge appears in the preview. Both go away when you delete the time words, so you can tell before pressing Enter whether the box understood a reminder.

### Priority

An item can be High, Medium or Low priority, or have none (the default). In the capture box, click the **High**, **Med** or **Low** chip under the input, or press `Alt+1`, `Alt+2` or `Alt+3`; `Alt+0` clears it. You can also type a token anywhere in the line:

| Token | Priority |
|---|---|
| `!high`, `!h`, `!!!` | High |
| `!medium`, `!med`, `!m` | Medium |
| `!low`, `!l` | Low |

A token has to stand alone between spaces. `!important`, `wow!!`, a bare `!` or `!!`, and anything inside quotes stay as ordinary text. The chips and the tokens are the same thing: clicking a chip writes the token into the line, and typing a token lights up its chip.

Priority shows as a coloured dot before the item text (red High, amber Medium, blue Low). Inside each tag section, High items come first, then Medium, then Low, then the rest. Today/Upcoming keeps time order. The `todo.md` export adds `(!high)`, `(!med)` or `(!low)` after the text.

### Voice

Click the mic at the right of the capture box, or press `Ctrl+M`, and say the line: "remind me tomorrow at 5 pm to check the deploy, hashtag backend". Listening stops by itself after about 1.5 seconds of silence (or at 30 seconds). Press `Ctrl+M` or the stop button to stop early, and `Esc` to cancel. The text drops into the input with the normal preview so you can fix it. **Enter saves; dictation never saves on its own.**

- Say "hashtag" or "hash tag" before a word to tag it (`#backend`). Spoken times such as "five p.m." and "in two hours" become `5 pm` and `in 2 hours` so the parser understands them.
- **Everything stays on your computer.** Speech is turned into text locally by [whisper.cpp](https://github.com/ggml-org/whisper.cpp) with the English `base.en` model. No audio or text is sent anywhere.
- **First use asks before downloading.** The first time you press the mic, the box explains what is needed and offers a one-time download: about 9 MB for the speech engine and 148 MB for the model (157 MB on Windows). Nothing is downloaded until you click **Download**. A progress bar shows the download; you can close the box and it continues. Each file is checked against a pinned SHA-256 before it is used (whisper.cpp release `b5454` = v1.9.5).
- **No memory cost while idle.** The speech engine starts when you start listening (so the model loads while you talk), and exits 30 seconds after your last dictation. It uses at most half your CPU cores (8 at most) and no GPU. Measured on Windows 11 (16 cores): while you dictate the engine takes about 250 MB of working set (about 740 MB committed); once it exits, voice adds nothing but one small WebView2 audio process (about 16 MB).
- **Microphone access.** On Windows, if dictation says the microphone is blocked, turn on Settings › Privacy & security › Microphone › "Let desktop apps access your microphone". On macOS the system asks once. If the box loses focus while listening, the recording is dropped and the mic is released.
- **Removing voice:** delete the `voice` folder listed under "Where data lives".

Engine per OS:

| OS | Engine |
|---|---|
| Windows x64 | Official whisper.cpp release build, downloaded on consent. It needs the Microsoft Visual C++ Redistributable (x64), which most PCs already have; if it is missing the box links to [the installer](https://aka.ms/vs/17/release/vc_redist.x64.exe). |
| Linux x64 / arm64 | Official whisper.cpp Ubuntu build, downloaded on consent (needs glibc from Ubuntu 22.04 or newer). |
| macOS | whisper.cpp publishes no macOS binaries. Install the engine with Homebrew, `brew install whisper.cpp`; the app then downloads only the model. Each dictation runs `whisper-cli` once, so there is no warm start. A `whisper-server` you built yourself and put on `PATH` is used instead when present. |

### Alerts

A reminder alerts at each lead time before it is due (default 1 hour and 10 minutes) and again at the due time. The alert pop-up appears at the bottom right without taking keyboard focus. Each card offers **Done**, **Snooze** (10 min, 1 hour, tomorrow) and **Open**. If the computer was asleep or the app was not running through a due time, you get one "missed" card per item when it wakes, not a burst of every alert you skipped.

A repeating reminder keeps reminding on schedule even if you never mark it done: when its next occurrence arrives, it moves to that occurrence and alerts at the time (occurrences missed while the app was off collapse into the latest one).

### Tray list

Click the tray icon, use the tray menu (**Open List**, **Settings**, **Quit**), or on Windows click the app's taskbar button to open the list. It groups items into today/upcoming, one section per tag, and done.

Click a row to open its details panel (see below). Double-click the text, or press `F2`, to edit the row as a line of text instead.

| Key | Action |
|---|---|
| `/` | focus search |
| `Up` / `Down`, `Home` / `End` | move the selection (with the panel open, it follows) |
| `Space` | toggle done |
| `Enter` | open the details panel with the title focused |
| `F2` | edit the selected row as a line of text |
| `s` | snooze menu |
| `.` or the context-menu key | row menu (snooze, stop repeating, delete) |
| `Delete` | delete (a toast offers Undo for 5 seconds) |
| `Ctrl+Z` / `Cmd+Z` | undo the last delete |
| `Esc` | close the panel, then clear search, then close the window |

### Task details

The details panel slides in from the right and takes half of the list window (at least 420 px). If the window is narrower than 1100 px it widens to 1100 px (or the screen width) while the panel is open, and shrinks back when it closes. The panel lives inside the list window, so it opens no extra window.

It edits the title, priority, due time and repeat (none, daily, weekdays, or weekly on the due day; set a due time first), tags, and a Markdown description. There is no Save button. Text saves 600 ms after you stop typing and whenever you leave the field, switch items or close the panel, and `Ctrl+S` saves at once. Priority, due, repeat and tags save as soon as you change them. The footer shows "Saving…", "Saved", or "Couldn't save" with a Retry, plus **Mark done** / **Reopen**, **Snooze** (reminders only) and **Delete** (with the usual Undo toast). Changing the due time clears any snooze and gives the item fresh alerts. Clearing it turns the item back into a to-do.

The description has a toolbar and an **Edit | Preview** switch. Each toolbar button shows its shortcut and the Markdown it inserts:

| Key | Inserts |
|---|---|
| `Ctrl+B` | bold |
| `Ctrl+I` | italic |
| `Ctrl+K` | link |
| `Ctrl+Shift+C` | code (inline, or a block around selected lines) |
| `Ctrl+Shift+8` | bulleted list |
| `Ctrl+Shift+9` | checklist (`- [ ]`) |

Heading and Image are toolbar buttons only. In Preview, checklist boxes can be ticked (that edits the Markdown and saves), images open in a lightbox (Esc closes it), and links open in your browser. Only `http`, `https` and `mailto` links work. Raw HTML in the description is stripped, so nothing in it can run.

**Images:** paste a screenshot (`Ctrl+V`), drop an image file onto the description, or use the Image button. PNG, JPEG, WebP, GIF (first frame) and BMP are accepted, up to 20 MB. Before saving, each image is shrunk so its longest side is at most 1600 px and stored as WebP (quality 0.8, or 0.6 if the result would be over 600 KB). A 4K screenshot usually ends up a few hundred KB. The description refers to it as `![name](att:<id>)`.

### Settings

Open Settings from the tray menu or the gear in the list window.

- **Hotkey:** click the field and press the new combination. If another app already owns it, the error is shown inline and in the tray tooltip, and you can pick another.
- **Alert lead times:** chips such as `1h` and `10m`; add more as `30m`, `2h`, `1d`.
- **Default time** for dates without a time (`09:00`), and the weekday and time used for "next week".
- **Export path:** if set, the app writes a read-only `todo.md` there after changes (leave empty to turn off).
- **Start at login:** on by default.
- **Show pending count:** on by default. Turn it off to remove the count from the tray icon, the taskbar button and the Dock.

### Sync across machines (optional)

Sync is off by default and the app never touches the network for it until you turn it on. To turn it on, create a free MongoDB Atlas cluster and a database user limited to `readWrite` on `awesome_reminder`, then paste the cluster's `mongodb+srv://` connection string in **Settings › Sync across machines** and click **Connect**. The step-by-step guide is [docs/atlas-setup.md](docs/atlas-setup.md).

- **What syncs:** to-dos and reminders (text, tags, due time, repeat, priority, description, done and snooze state, deletions), the description images, and the reminder defaults (lead times, default time, "next week" day and time). The hotkey, export path, "Start at login", "Show pending count" and the record of shown alerts stay on each computer.
- **A new machine** connects with the same string and restores everything. The first sync merges both sides: the union of the two, and for an item edited on both the newest edit wins. Overdue items that come back do not flood you with "missed" cards.
- **Cadence:** a change is sent about 2 seconds after you make it, and the app checks for changes from other computers every 30 seconds. Your changes made offline are kept and sent when the network returns. Capture, the list and alerts never wait for the network.
- **Status:** the list header shows a pill (`All synced · 2 min ago`, `Syncing…`, `Offline · 3 changes waiting`, `Sync error`); click it for details, **Sync now** and the cluster host. Rows that Atlas has not received yet show a small crossed-out cloud marker. The tray icon carries a status dot and the tooltip ends with the same wording.
- **Safety:** the connection string is kept in the OS keychain (or an AES-256-GCM encrypted file where there is none) and never logged. If the database user can reach other databases, Settings warns you but sync still works. Synced data is not encrypted beyond Atlas's own TLS and storage, so it is readable by anyone with access to your cluster.
- **Disconnect** in Settings stops sync and forgets the string; your items stay here and Atlas keeps its copy.

### Pending count

The number of open items (to-dos and reminders not yet done) shows as a red badge. Above 9 it reads `9+`, and with nothing open there is no badge. It updates whenever an item changes.

- **Tray icon (all systems):** the badge is drawn on the icon, and the tooltip reads "Awesome Reminder — 3 pending", followed by the hotkey error if there is one.
- **Windows taskbar:** the app has a single taskbar button, a tiny native window rather than one of the app's windows, and the count appears as its overlay icon. Clicking the button opens the list. The list and Settings windows add no buttons of their own. You can pin the button so the count is always visible.
- **macOS:** the count is the Dock badge.
- **Linux:** the badge is on the tray icon only for now (no launcher count).

### Where data lives

Everything is under the per-user config directory, in an `awesome-reminder` folder:

| OS | Path |
|---|---|
| Windows | `%APPDATA%\awesome-reminder\` |
| macOS | `~/Library/Application Support/awesome-reminder/` |
| Linux | `~/.config/awesome-reminder/` |

It holds `awesome.db` (the SQLite database), `awesome.log`, and an `attachments` folder with the description images (`<id>.webp`). With sync on and no OS keychain it also holds `sync.key`. Settings are stored in the database. Delete the folder to reset the app.

At startup the app deletes images that no description has referred to for more than 7 days, and images of items that have been deleted for more than 30 days. Undoing a delete within those windows keeps the images.

The voice engine and model (Phase 1.5, about 160 MB) live in the local cache instead, so they never roam with your profile: `%LOCALAPPDATA%\awesome-reminder\voice\` on Windows, `~/Library/Caches/awesome-reminder/voice/` on macOS and `~/.cache/awesome-reminder/voice/` on Linux. Delete that folder to remove voice; the next mic press offers the download again.

### Command-line flags

Only one copy of the app runs. Starting it again with a flag asks the running copy to do something and exits:

- `awesome-reminder --capture` opens the capture box
- `awesome-reminder --list` opens the list
- `awesome-reminder --settings` opens Settings

### Linux notes

On X11 the global hotkey works as on other systems. **Wayland does not allow apps to register global hotkeys**, and the XDG GlobalShortcuts portal is not supported yet. Bind `awesome-reminder --capture` to a shortcut in your desktop's keyboard settings instead; the running app opens the capture box when it is invoked.

## Build from source

Requirements: Go, Node.js, and on Linux the WebKitGTK/GTK development packages plus gcc (Windows needs the WebView2 runtime, which ships with Windows 11; macOS needs the Xcode command-line tools).

```
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
wails3 doctor      # checks your toolchain
wails3 build       # output: bin/awesome-reminder (.exe on Windows)
go test ./...
npm --prefix frontend run typecheck   # type-checks the frontend (tsc, no output files)
```

Do not run the freshly built binary from the repo folder if you do not want it to register itself for autostart; copy it elsewhere first, or turn off "Start at login" in Settings.

The icon is drawn in `build/appicon.svg` and the tray glyphs in `build/tray/*.svg`; the PNG, ICO and ICNS files are generated from them (`wails3 generate icons`). Product name, identifier and version live in `build/config.yml`; run `wails3 task common:update:build-assets` after changing them.

## Status and roadmap

Phase 1 (local core) is complete: hotkey capture, natural-language parsing, local storage, tray list, alerts, settings, `todo.md` export and autostart. Phase 1.5 adds local voice dictation in the capture box. Phase 1.6 adds the details panel with a Markdown description and inline images, priority, the live reminder indicator in the capture box, and the pending-count badge. Phase 2 (optional sync through your own MongoDB Atlas cluster, with status indicators in the tray and list) is complete. Nothing leaves your machine unless you turn sync on (the one-time voice download is the only other network use, and only after you agree to it).

Planned, in order:

1. **Phase 3:** hands-free voice. Say "Hey Awesome" to start capturing without the hotkey, and dictate several tasks in one go: saying "create new task" saves the current one and starts the next.
2. **Phase 4:** optional phone push for reminders through ntfy.

The full design is in [docs/superpowers/specs/2026-11-27-awesome-reminder-design.md](docs/superpowers/specs/2026-11-27-awesome-reminder-design.md); the UI rules are in [docs/superpowers/specs/2026-11-27-ui-design-guide.md](docs/superpowers/specs/2026-11-27-ui-design-guide.md). Atlas setup is in [docs/atlas-setup.md](docs/atlas-setup.md). Per-OS verification is tracked in [docs/manual-test-checklist.md](docs/manual-test-checklist.md).
