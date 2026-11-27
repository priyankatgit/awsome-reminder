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

### Replies owed

For a message someone is waiting on. Select the message in Outlook, Teams, Slack, a browser or any other app, then press the reply key (default `Ctrl+Alt+R`, `Ctrl+Option+R` on macOS; `Ctrl+Alt+Shift+R` if your capture hotkey is already `Ctrl+Alt+R`). The capture box opens in reply mode with the line filled in, such as `Reply to Priya: can you check the deploy?`. Press Enter to save it, or edit the line first. If the capture box is already open, the key switches it to reply mode and keeps what you typed.

- **What it grabs.** On Windows and macOS the app copies the selection (it sends `Ctrl+C` / `Cmd+C` to the app you are in), reads it, and puts your clipboard back exactly as it was, every format included. Nothing watches the clipboard or the window in the background; it all happens when you press the key. If the copy is slow to arrive, the clipboard is still put back a moment later.
- **Clipboard history.** The copy is made by the other app, so it appears in Windows clipboard history (`Win+V`), cloud clipboard, Universal Clipboard and clipboard managers like any copy you make yourself. The app's own restore is marked so history and cloud sync skip it.
- **When it does not copy.** Nothing is sent to terminals and IDEs, where `Ctrl+C` with nothing selected would stop the running process: Windows Terminal, cmd, PowerShell, WSL, mintty, Alacritty, WezTerm, VS Code, Cursor, the JetBrains IDEs and Visual Studio on Windows; Terminal, iTerm2, Alacritty, WezTerm, kitty, Warp, Hyper, Ghostty, VS Code, Cursor and the JetBrains IDEs on macOS. It also sends nothing when the clipboard holds an image or files, or a format that cannot be put back unchanged, or while a key of the hotkey is still held. In these cases the box opens with whatever text the clipboard already holds (only if it is 2,000 characters or less), and your clipboard is not touched.
- **Who it is from.** The app guesses the person from the text (a `From:` line or a chat header such as `Priya  10:42`) or the window title (Teams, Slack), and the source from the app (Outlook, Teams, Slack, Chrome and so on). The person shows in the preview as a chip; press `Alt+P` to change it before saving. With no guess, the reply is owed to "Someone". The line is cut at 200 characters; the grabbed text is kept in full as a quote in the description.
- **Waiting on you.** Open replies have their own section at the top of the list, one subheading per person, oldest first. A row shows the source and how long it has waited (`Teams · 2 h`; the age turns amber after 24 hours). A reply is not listed under tags or Today/Upcoming. Mark it done when you have replied (the details panel says **Replied**), or choose **No reply needed** in the row menu or the panel to close it without replying; it moves to Done as "no reply needed". The panel has a **For** field for the person and shows where it came from.
- **Nudges.** A reply without a due time is nudged 2 hours after you save it, then at the default time (09:00) on each working day until you close it. A reply with a due time gets its normal alerts first, then the daily nudge. The nudge card reads "waiting 2 h · Priya" with **Replied**, **Snooze** and **Open**. Snoozing a reply (10 min, 1 hour or tomorrow, from the card, the row menu or the panel) holds its nudges until then, with or without a due time. The capture box shows the plan before you save, for example `nudge in 2h, then 9:00`.
- **End-of-day sweep.** At the end of the working day (default 17:30) one card lists who is still waiting: "3 people are still waiting on you - Priya, Sam, Ops channel". **Open list** shows the Waiting section; **Tomorrow** snoozes every open reply to the next working day at the default time. There is no sweep on non-working days, and none when every open reply was saved after the sweep time. A sweep you slept through is dropped, not shown late the next day.
- **Settings.** A Replies group has the reply key, the working days (default Monday to Friday), the end-of-day time and an end-of-day sweep switch. The reply key must differ from the capture hotkey; if another app owns it, the error shows inline and in the tray tooltip.
- **`todo.md`.** The export starts with a `## Waiting on you` section of lines like `- [ ] Reply to Priya: can you check the deploy? (from Teams)`.
- **macOS.** Reading the selection needs the Accessibility permission (System Settings › Privacy & Security › Accessibility). Without it the box says so and uses the clipboard's text instead.
- **Linux.** The app reads the selected text (the primary selection) with `wl-paste`, `xclip` or `xsel`, whichever is installed. It injects nothing and never writes the clipboard; with nothing selected it falls back to the clipboard's text. Wayland has no global hotkeys, so bind `awesome-reminder --reply` to a shortcut in your desktop settings.

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
- **Replies:** the reply key, working days, end-of-day time and the end-of-day sweep (see Replies owed above).
- **Start at login:** on by default.
- **Show pending count:** on by default. Turn it off to remove the count from the tray icon, the taskbar button and the Dock.

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

It holds `awesome.db` (the SQLite database), `awesome.log`, and an `attachments` folder with the description images (`<id>.webp`). Settings are stored in the database. Delete the folder to reset the app.

At startup the app deletes images that no description has referred to for more than 7 days, and images of items that have been deleted for more than 30 days. Undoing a delete within those windows keeps the images.

The voice engine and model (Phase 1.5, about 160 MB) live in the local cache instead, so they never roam with your profile: `%LOCALAPPDATA%\awesome-reminder\voice\` on Windows, `~/Library/Caches/awesome-reminder/voice/` on macOS and `~/.cache/awesome-reminder/voice/` on Linux. Delete that folder to remove voice; the next mic press offers the download again.

### Command-line flags

Only one copy of the app runs. Starting it again with a flag asks the running copy to do something and exits:

- `awesome-reminder --capture` opens the capture box
- `awesome-reminder --reply` opens the capture box in reply mode, as the reply key does
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

Phase 1 (local core) is complete: hotkey capture, natural-language parsing, local storage, tray list, alerts, settings, `todo.md` export and autostart. Phase 1.5 adds local voice dictation in the capture box. Phase 1.6 adds the details panel with a Markdown description and inline images, priority, the live reminder indicator in the capture box, and the pending-count badge. Replies owed (grab a message, track who is waiting, nudges and an end-of-day sweep) is done. Nothing leaves your machine (the one-time voice download is the only network use, and only after you agree to it).

Planned, in order:

1. **Phase 2:** optional sync across your machines through your own MongoDB Atlas cluster, with sync status indicators in the tray and list.
2. **Phase 3:** hands-free voice. Say "Hey Awesome" to start capturing without the hotkey, and dictate several tasks in one go: saying "create new task" saves the current one and starts the next.
3. **Phase 4:** optional phone push for reminders through ntfy.

The full design is in [docs/superpowers/specs/2026-11-27-awesome-reminder-design.md](docs/superpowers/specs/2026-11-27-awesome-reminder-design.md); the UI rules are in [docs/superpowers/specs/2026-11-27-ui-design-guide.md](docs/superpowers/specs/2026-11-27-ui-design-guide.md). Per-OS verification is tracked in [docs/manual-test-checklist.md](docs/manual-test-checklist.md).
