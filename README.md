# Awesome Reminder

Awesome Reminder is a small desktop app for capturing to-dos and reminders without leaving what you are doing. Press a global hotkey, type a line such as `remind me tomorrow 5pm check deploy #backend`, and press Enter. The app works out whether it is a to-do or a timed reminder, stores it locally, and alerts you before it is due. It runs from the system tray, keeps everything in a local SQLite file, and needs no account or network. It is built with Go and Wails v3 and runs on Windows 11, macOS 14+ and Linux.

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

### Alerts

A reminder alerts at each lead time before it is due (default 1 hour and 10 minutes) and again at the due time. The alert pop-up appears at the bottom right without taking keyboard focus. Each card offers **Done**, **Snooze** (10 min, 1 hour, tomorrow) and **Open**. If the computer was asleep or the app was not running through a due time, you get one "missed" card per item when it wakes, not a burst of every alert you skipped.

### Tray list

Click the tray icon, or use the tray menu (**Open List**, **Settings**, **Quit**), to open the list. It groups items into today/upcoming, one section per tag, and done.

| Key | Action |
|---|---|
| `/` | focus search |
| `Up` / `Down`, `Home` / `End` | move the selection |
| `Space` | toggle done |
| `Enter` | edit the selected row as a line of text |
| `s` | snooze menu |
| `.` or the context-menu key | row menu (snooze, stop repeating, delete) |
| `Delete` | delete (a toast offers Undo for 5 seconds) |
| `Ctrl+Z` / `Cmd+Z` | undo the last delete |
| `Esc` | clear search, then close |

### Settings

Open Settings from the tray menu or the gear in the list window.

- **Hotkey:** click the field and press the new combination. If another app already owns it, the error is shown inline and in the tray tooltip, and you can pick another.
- **Alert lead times:** chips such as `1h` and `10m`; add more as `30m`, `2h`, `1d`.
- **Default time** for dates without a time (`09:00`), and the weekday and time used for "next week".
- **Export path:** if set, the app writes a read-only `todo.md` there after changes (leave empty to turn off).
- **Start at login:** on by default.

### Where data lives

Everything is under the per-user config directory, in an `awesome-reminder` folder:

| OS | Path |
|---|---|
| Windows | `%APPDATA%\awesome-reminder\` |
| macOS | `~/Library/Application Support/awesome-reminder/` |
| Linux | `~/.config/awesome-reminder/` |

It holds `awesome.db` (the SQLite database) and `awesome.log`. Settings are stored in the database. Delete the folder to reset the app.

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
```

Do not run the freshly built binary from the repo folder if you do not want it to register itself for autostart; copy it elsewhere first, or turn off "Start at login" in Settings.

The icon is drawn in `build/appicon.svg` and the tray glyphs in `build/tray/*.svg`; the PNG, ICO and ICNS files are generated from them (`wails3 generate icons`). Product name, identifier and version live in `build/config.yml`; run `wails3 task common:update:build-assets` after changing them.

## Status and roadmap

Phase 1 (local core) is complete: hotkey capture, natural-language parsing, local storage, tray list, alerts, settings, `todo.md` export and autostart. Nothing leaves your machine.

Planned, in order:

1. **Phase 1.5:** voice dictation in the capture box (click to dictate, the text goes through the same parser; Enter always saves).
2. **Phase 2:** optional sync across your machines through your own MongoDB Atlas cluster, with sync status indicators in the tray and list.
3. **Phase 3:** optional phone push for reminders through ntfy.

The full design is in [docs/superpowers/specs/2026-11-27-awesome-reminder-design.md](docs/superpowers/specs/2026-11-27-awesome-reminder-design.md); the UI rules are in [docs/superpowers/specs/2026-11-27-ui-design-guide.md](docs/superpowers/specs/2026-11-27-ui-design-guide.md). Per-OS verification is tracked in [docs/manual-test-checklist.md](docs/manual-test-checklist.md).
