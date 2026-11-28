# Manual test checklist

Per-OS verification for the Phase 1 to Phase 2 builds, from the Verification section of the [design spec](superpowers/specs/2026-11-27-awesome-reminder-design.md). Run it on a release build (`wails3 build`) and record the result per OS.

Result values: **pass**, **fail** (with the number or a note), **needs human** (not verifiable by a script or needs a person at the keyboard or a reboot), **n/a**, or blank (not run yet). Only the Windows column has been filled in so far, and only with what was actually observed; the macOS and Linux columns are still to be run on those machines.

Safety when testing: do not run `bin/awesome-reminder` from the repo folder if you do not want it to register itself for autostart. Copy it elsewhere first, or point `APPDATA` / `XDG_CONFIG_HOME` / `HOME` at a scratch folder so the real data is untouched. (The app skips autostart registration when it runs from a temp directory.)

| # | Check | How | Windows 11 | macOS 14+ | Ubuntu X11 | Ubuntu Wayland |
|---|---|---|---|---|---|---|
| 1 | Hotkey latency < 150 ms | Press the hotkey while another app is focused; the capture box should be visible and typeable almost immediately. Time it with a phone slow-motion video or a stopwatch overlay. | needs human | | | n/a for the global hotkey (bind `awesome-reminder --capture` instead; time that) |
| 2 | Capture popup centered on the active monitor and focused | With two monitors, put the mouse and the focused app on the second one; press the hotkey; type immediately. The box is centered on that monitor and the first keystrokes land in the input. | needs human | | | |
| 3 | Tray menu | Tray icon is visible and legible on light and dark taskbars/menu bars; the menu shows Open List, Settings, Quit; the left click opens the list; Quit exits. On macOS the icon is a template image and follows the menu bar color. | needs human (the icon and menu were not inspected or clicked) | | | |
| 4 | Autostart after reboot | Enable "Start at login", reboot, log in: the app is running in the tray with no window. Disable the setting: the entry is removed (Windows: `HKCU\...\Run\AwesomeReminder`; macOS: `~/Library/LaunchAgents`; Linux: `~/.config/autostart`). | needs human (reboot) | | | |
| 5 | Catch-up after sleep | Create a reminder due in 3 minutes with the default lead times (1h, 10m, at due). Sleep the machine through the due time, wake it. Exactly one "missed" card per item appears, not one per lead time. | needs human (sleep) | | | |
| 6 | Idle RAM < 150 MB after 10 min idle, all windows closed | Task Manager (Details, add the app and its WebView2 children) / Activity Monitor / `ps -o rss`. Sum the app and its WebView2/WebKit helper processes. | **pass: 137 MB private (308 MB working set) summed over the tree after 10 min idle; 144 MB private after opening and closing every window and two alerts** (see measurements) | | | |
| 7 | ~0% idle CPU | Same tools, 60 s window with nothing open. | pass: 0.08% of one core for the whole tree, 0% in the Go process (no CPU-profile samples in 120 s) | | | |
| 8 | Second-instance flags | Start the app, then run `awesome-reminder --capture`, `--list`, `--settings`: each opens the matching window in the running copy and the second process exits. | pass (log: `second instance: --capture`/`open list`/`--settings`; windows `Awesome Reminder` and `Awesome Reminder Settings` became visible; second process exit code 0) | | | |
| 9 | Windows hide, app keeps running | Close the list and settings windows. The app and tray stay alive. | pass (closed via WM_CLOSE; log `settings: closed`, `list: closed`; process alive, 0 visible windows) | | | |
| 10 | `todo.md` export | Set an export path in Settings (or the database), change an item (toggle done). The file appears within a second or two with sections per tag and a Done section. | pass (seeded the export path, toggled an item in the list with the keyboard; the file was written with the expected content) | | | |
| 11 | Build metadata | Windows exe properties: product "Awesome Reminder", version 0.1.0, no company. macOS: bundle id `dev.awesome-reminder.app`, name "Awesome Reminder". | pass for the Windows exe (ProductName, FileDescription, 0.1.0, empty CompanyName); macOS not checked | | | |
| 12 | Unsigned-app prompts | First launch shows SmartScreen / Gatekeeper; the README steps get past it. | needs human | | | |
| 13 | Hotkey conflict | Register the hotkey in another app first. This app keeps running; the tray tooltip and Settings show the error; a new combo can be saved. | needs human (covered by `TestHotkeyManagerReportsRegisterError`) | | | |

## Windows 11 measurements

Machine: Windows 11 Pro build 26300, 16 logical cores, WebView2 runtime 158.0.4370.45. Build: `wails3 build` (production), run from a copy under `%TEMP%` with `APPDATA` pointed at a scratch folder; the database is seeded with one to-do and two reminders that fire after the idle measurement (no lead alerts).

Method: PowerShell. Find the app process, walk Win32_Process by `ParentProcessId` (CIM) to collect all descendants (the msedgewebview2 processes of this app only; other apps' WebView2 processes on the machine are excluded), then read `WorkingSet64`, `PrivateMemorySize64` and `TotalProcessorTime` for each. CPU is the change in total processor seconds over a 60 s window divided by the wall time, as a percent of one core. "After" = press the hotkey, `--capture`, `--list`, `--settings`, close list and settings (WM_CLOSE), let two alerts fire 30 s apart and dismiss each (UI Automation), 60 s settle, 60 s window.

Current build (Task 12b), fresh start, no window opened, 10 minutes idle:

| Process | Working set | Private | CPU (% of one core) |
|---|---|---|---|
| awesome-reminder (Go app) | 35.0 MB | 52.4 MB | 0 |
| msedgewebview2 browser (main, GPU thread in-process) | 130.0 MB | 36.5 MB | 0.05 |
| msedgewebview2 renderer (capture window) | 67.6 MB | 25.0 MB | 0 |
| msedgewebview2 utility x2 (network, storage) | 41.4 + 21.0 MB | 12.2 + 7.8 MB | 0.03 |
| msedgewebview2 crashpad-handler | 13.2 MB | 3.1 MB | 0 |
| **Total (6 processes)** | **308 MB** | **137.0 MB** | **0.08** |

After the window round and two alerts: 143.6 MB private / 323 MB working set (6 processes; the browser process grows by about 3 MB, the Go process by about 3 MB). The alert window is destroyed when its last card goes, so no renderer stays behind for it.

How it got there (3 min idle unless noted; private / working set of the whole tree):

| Step | Fresh idle | After windows + alert | Go CPU |
|---|---|---|---|
| Task 12 build | 200.4 / 376 MB | 231.5 / 464 MB | 1.5% of a core |
| Native Windows hotkey loop (no 10 ms polling) | 200.5 / 382 MB | 235.1 / 475 MB | 0 |
| Alert window destroyed when empty | 198.3 / 380 MB | 210.2 / 411 MB | 0 |
| WebView2 `--disable-gpu --in-process-gpu` | 139.3 / 311 MB | 151.3 / 341 MB | 0 |
| Same, 10 min idle | 137.0 / 308 MB | 143.6 / 323 MB | 0 |

Reading the numbers: the working-set sum double counts pages shared between the WebView2 processes, so private is the fairer total. The Go runtime itself holds only about 12 MB (`runtime.MemStats.Sys`, heap in use about 3 MB); the rest of the app process's 52 MB is native WebView2 host code and data loaded into it. The remaining tree is the minimum for one live WebView2 (the pre-created capture window, which keeps hotkey latency low): browser, renderer, network and storage utilities, crashpad.

## Voice dictation (Phase 1.5)

| # | Check | How | Windows 11 | macOS 14+ | Ubuntu X11 | Ubuntu Wayland |
|---|---|---|---|---|---|---|
| V1 | Consent before download | Fresh voice folder; click the mic. A panel names the size ("157 MB" on Windows) and offers Download / Not now; nothing is downloaded until Download. | pass (2026-11-27, driven over DevTools) | | | |
| V2 | Download with progress, verified | Click Download: progress bar and "Downloading … MB of 157 MB"; the box can be closed meanwhile. Engine files and `models/ggml-base.en.bin` (147,964,211 bytes) appear in the voice folder; a wrong checksum discards the file and shows an inline error (covered by `TestInstallFailureReportsError`). | pass (download ~40 s) | | | |
| V3 | Dictation to the input, no auto-save | Mic or Ctrl+M, say "remind me tomorrow at 5 pm to check the deploy hashtag backend". Listening row (ring, waveform, timer), auto-stop after a pause, "Transcribing…", then the text in the input with the preview. Nothing is saved until Enter. | pass with a TTS phrase as a fake mic: input "Remind me tomorrow at 5pm to check the deploy #Back", preview "Reminder Sat 28 Nov, 17:00 #back"; 1.4 s from auto-stop to text | | | |
| V4 | Esc / blur / Ctrl+M | Esc while listening cancels; losing focus while listening drops the recording; Ctrl+M starts and stops. | pass (DevTools-dispatched keys and blur) | | | |
| V5 | Real microphone and permission | Speak into a real mic; then turn off Settings › Privacy & security › Microphone › desktop apps and try again: an inline message explains the fix. On macOS the OS asks once. | needs human (no mic used in the automated run) | | | |
| V6 | whisper exits after idle and with the app | ~30 s after the last dictation no `whisper-server` process is left; killing the app while it runs takes it down too (Windows job object). | pass | | | |

Windows run (same machine as above; temp copy, `APPDATA`/`LOCALAPPDATA` pointed at scratch folders). The automated run used a test-only build that also passes `--remote-debugging-port` and Chromium's fake-mic flags (`--use-fake-device-for-media-stream --use-file-for-fake-audio-capture=<wav>`) through `AdditionalBrowserArgs`; the shipped build has no such hook, and `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS` is ignored because Wails sets its own arguments.

| Moment | App (Go) WS / private | WebView2 tree WS / private | whisper-server WS / private |
|---|---|---|---|
| Before any voice use | 35 / 53 MB | 322 / 141 MB (6 processes) | none |
| Listening (engine warmed) | 47 / 61 MB | 387 / 164 MB (7 processes) | 207 / 736 MB |
| Right after transcription | 50 / 63 MB | 390 / 164 MB | 256 / 746 MB |
| 35 s after the last dictation | 50 / 63 MB | 387 / 161 MB (7 processes) | none |

Notes: whisper-server (8 threads on 16 cores, `base.en`) commits about 740 MB private while it runs (working set about 250 MB); it is gone 30 s after use. After the first dictation WebView2 keeps one extra utility process (the audio service), about +16 MB private, until the app restarts.

## Details panel, priority, pending badge (Phase 1.6)

| # | Check | How | Windows 11 | macOS 14+ | Ubuntu X11 | Ubuntu Wayland |
|---|---|---|---|---|---|---|
| D1 | Panel opens, switches, closes | Click a row: the panel slides in at half the window (min 420 px), and a window narrower than 1100 px widens and shrinks back on close. `Enter` opens it with the title focused; `F2` / double-click edits inline instead; `Up`/`Down` switch the panel's item; `Esc` closes the panel, then clears search, then closes the window. | needs human (keys covered by the list and panel vitest suites) | | | |
| D2 | Autosave, including a racing switch | Type in the title or description and wait: "Saving…" then "Saved". Type and press `Down` within 600 ms: the edit lands on the first item, not the second. `Ctrl+S` saves at once. | needs human (race covered by `flushes pending edit to the previous item on switch`) | | | |
| D3 | Pasted screenshot | Paste an 8 MB 4K PNG into the description: it shows "Uploading…", then the image; the file in `attachments\` is WebP and ≤ 600 KB. A 25 MB image shows "Image too large (max 20 MB)". | needs human (sizing covered by `compressTarget picks quality step` and `rejects >20MB`) | | | |
| D4 | Hostile Markdown is inert | Description `<img src=x onerror=alert(1)>`, `[x](javascript:alert(1))`, `<a href="file:///C:/">x</a>`: Preview shows no alert, and clicking does not navigate. An `https` link opens in the browser. | needs human (covered by `sanitizes hostile markdown`) | | | |
| D5 | Checklist tick persists | `- [ ] step` in the description, tick it in Preview, close and reopen the panel: it reads `- [x] step`. | needs human | | | |
| D6 | Capture priority and reminder indicator | `Alt+1/2/3/0` and the chips write and clear `!high` / `!med` / `!low`; `!important` and `"!h"` stay text. Typing `tomorrow 5pm` pops in the light-red bell and the "Reminder · …" badge; deleting the time fades them out. | needs human (tokens covered by the parser table) | | | |
| D7 | Idle RAM unchanged with the panel closed | As row 6: measure after 10 min idle, then open a panel with an image, close it, and measure again after 10 min. The difference should be small (Markdown libraries load only on first open). | | | | |
| B1 | Taskbar badge | With open items, the taskbar button shows a red count overlay (`9+` above 9, none at 0). | pass (3 open items seeded: the button showed a red "3" over the app icon; screenshot) | n/a (Dock badge: check the Dock tile instead) | n/a | n/a |
| B2 | One taskbar button, click opens the list | Only one "Awesome Reminder" button; the list and Settings add none. Clicking it opens the list, or Settings if that was used last, and the native window stays minimized. | pass (only the native window has `WS_EX_APPWINDOW`; the list window has `WS_EX_TOOLWINDOW`. A posted `SC_RESTORE` opened the list, and the native window stayed minimized. With Settings open and Notepad in front, a second click brought Settings to the foreground) | n/a | n/a | n/a |
| B3 | Live count and tray badge | Add and complete items: the taskbar, the tray icon and the tooltip ("Awesome Reminder — N pending") follow within about a second. | needs human (covered by `TestBadgeWiringUpdatesOnChange`; the tray icon was not inspected) | | | |
| B4 | Setting off | Turn off Settings › Show pending count: the badge leaves the tray, taskbar and Dock, and the tooltip is just "Awesome Reminder". Turn it on again: the badge comes back. | needs human (covered by `TestBadgeWiringUpdatesOnChange`) | | | |
| B5 | Pinned button | Pin the taskbar button, quit, start from the pin: the pin and the running button merge into one, with the badge. | needs human | n/a | n/a | n/a |

Windows run (2026-11-27, same machine; temp copy with `APPDATA`/`LOCALAPPDATA` pointed at a scratch folder): no `taskbar:` errors in `awesome.log`. The Go process was at 58 MB private / 40 MB working set a few seconds after start with the native window. That is no measurement of the window's own cost, which is a plain Win32 window with no WebView.

## Sync across machines (Phase 2)

The rows need an Atlas cluster set up as in [atlas-setup.md](atlas-setup.md); most need two machines. The Windows run below used a copy of the build under `%TEMP%` with `APPDATA` / `LOCALAPPDATA` pointed at a scratch folder, and the MongoDB service on `localhost:27017` as a stand-in for Atlas (an empty server with no authentication and no `awesome_reminder` database beforehand). That tests the app's own paths, not Atlas itself: no SRV lookup, no TLS, no IP access list, no pausing, no per-user roles.

| # | Check | How | Windows 11 | macOS 14+ | Ubuntu X11 | Ubuntu Wayland |
|---|---|---|---|---|---|---|
| S1 | Connect and status | Settings › Sync across machines: paste the string, Connect. The section shows "All synced · just now", the cluster host, where the string is stored, and "Images in Atlas: N MB of 512 MB". | pass (`mongodb://localhost:27017/`: "All synced · just now", "Cluster localhost", "Connection string stored in Windows Credential Manager", "Images in Atlas: 0.0 MB of 512 MB", read through UI Automation) | | | |
| S2 | Wrong password | Connect with a user whose password is wrong. | pass (a made-up user on the local server: "Atlas rejected the username or password."; nothing saved) | | | |
| S3 | Cluster unreachable wording | Use a string whose host resolves but does not answer, or pause the cluster. | pass for the wording only (`mongodb://localhost:1/` after about 10 s: "Can't reach your Atlas cluster. It may be paused (resume it in Atlas), or this network's IP isn't on the Atlas access list."). A really paused M0 cluster: needs human | | | |
| S4 | Broad user warning | Connect with a user that has `readWriteAnyDatabase`. Settings and the pill popover show "This Atlas user can reach other databases on your cluster. Limit it to readWrite on awesome_reminder (see the setup guide)."; sync still works. | needs human (the local server has no users; needs an Atlas cluster. Covered by the engine's scope-check tests) | | | |
| S5 | Two machines converge | Machines A and B on the same cluster. Add, edit and mark done on A; B shows each within 30 s, and the other way round. | needs human (two machines) | | | |
| S6 | Done on A removes the card on B | Both machines running. An alert card for the same item is showing on B; click Done on A. Within 30 s the card on B goes away. | needs human (two machines) | | | |
| S7 | Offline edit, marker, reconnect | Disconnect the network. Edit an item: its row shows the "not synced yet" marker and the pill reads "Offline · 1 change waiting". Reconnect (or press Sync now): the marker fades, the pill glows once and reads "All synced · just now". | needs human (network switch; the pill and marker wording is covered by the vitest suites) | | | |
| S8 | Restore on a fresh profile | New profile (or machine), same connection string: items, images and lead times come back, and no stack of "missed" cards appears for the overdue items. | needs human (an empty local server cannot show it; covered by `TestRestoreFreshDevice` and `TestApplyRemoteNewItemSuppressesPastAlerts`) | | | |
| S9 | Tray dot and tooltip | The tray icon shows the dot (green synced, alternating ring/dot syncing, grey ring offline, red error; macOS uses shapes instead); the tooltip ends with the matching text. | needs human (the tray icon and tooltip were not inspected; covered by `TestBadgeWiringSyncChanged`) | | | |
| S10 | Sleep and wake | Sleep for longer than 5 minutes with sync on; on wake the status returns to synced within a minute and no item is lost. | needs human (sleep) | | | |
| S11 | Idle RAM with sync on after 10 min < 150 MB | As row 6, with sync connected. | **Fresh start, no window opened: pass** (143.6 MB private, 10 min idle). **After the list and Settings windows have been opened and closed: fail, 151.7 MB** (just over 150; 60 s settle). The same round with sync off also reads 151.8 MB, so sync made no measurable difference in these runs (one run each, against an empty local MongoDB with no TLS or SRV, so real Atlas may cost more); see measurements | | | |
| S12 | ~0% idle CPU with sync on | As row 7 with sync connected (a poll every 30 s). | pass: 0.15% of one core for the whole tree over 60 s (Go process 0.03%), with a poll every 30 s | | | |

### Windows 11 measurements with sync on

Same machine and method as above (PowerShell, the app's process tree through `Win32_Process.ParentProcessId`, `WorkingSet64`, `PrivateMemorySize64`, CPU over a 60 s window after the idle time). Build: `wails3 build` of this branch, run from a copy under `%TEMP%` with `APPDATA` / `LOCALAPPDATA` in a scratch folder. Sync was connected to the local MongoDB service (`mongodb://localhost:27017`, stored in Windows Credential Manager); the database was empty, so the numbers do not include the cost of holding items, and a real Atlas cluster adds TLS and an SRV lookup that this run did not exercise. The scratch database and the credential were removed afterwards.

Fresh start (the app opened the saved string by itself), no window opened, 10 minutes idle:

| Process | Working set | Private | CPU (% of one core) |
|---|---|---|---|
| awesome-reminder (Go app) | 42.9 MB | 55.9 MB | 0.03 |
| msedgewebview2 browser | 144.5 MB | 38.8 MB | 0.03 |
| msedgewebview2 renderer (capture window) | 71.8 MB | 26.1 MB | 0.03 |
| msedgewebview2 utility x2 | 40.9 + 21.3 MB | 11.9 + 7.8 MB | 0.03 + 0 |
| msedgewebview2 crashpad-handler | 13.5 MB | 3.1 MB | 0.03 |
| **Total (6 processes)** | **334.9 MB** | **143.6 MB** | **0.15** |

Against Phase 1 (137.0 MB private, Go process 52.4 MB) sync adds about 6.6 MB private, nearly all in the Go process (55.9 MB) and the WebView2 browser process. That leaves 6.4 MB below the 150 MB limit for the fresh-start case only. After opening and closing windows the figure is 151.7 MB with sync on, which is over the limit, and equal to sync off (see below).

After a window round, sync off against sync on (same build, scratch profile each, 6 processes each; the round is `--capture`, `--list`, `--settings` against the running copy, then Settings and the list closed with WM_CLOSE through UI Automation, with no synthetic keyboard input; 15 s after start before the round, 60 s settle, 60 s window; no alerts fired, unlike Phase 1's "After" row, and no 10-minute idle first):

| Case | Working set | Private | CPU (% of one core) | Verdict (< 150 MB private) |
|---|---|---|---|---|
| Sync off | 335.8 MB | 151.8 MB | 0.54 | fail (by 1.8 MB) |
| Sync on (local server, empty database) | 340.4 MB | 151.7 MB | 0.67 | fail (by 1.7 MB) |

After the window round, both sync off and sync on read about 8 MB above the fresh-start 143.6 MB, and sync on and off are the same within 0.1 MB. The cause of that growth is not established; this run did not reproduce Phase 1's 143.6 MB "After" figure, which was taken with a different alert sequence and a 10-minute idle first. The earlier 188.9 MB reading, with a second renderer still alive after the list and Settings windows had been opened and closed in a different order, was not reproduced here (6 processes both times) and should not be relied on; why that run kept a second renderer is not established.

## Replies owed

Run on a release build; each row is for the person at the keyboard. The Windows column is filled in after the run.

| # | Check | How | Windows 11 | macOS 14+ | Ubuntu X11 | Ubuntu Wayland |
|---|---|---|---|---|---|---|
| R1 | Grab in real apps | Select text in Outlook, Teams, Slack and Chrome, then press the reply key: the box opens pre-filled with the line, the person and the source within about 200 ms. | | | | |
| R2 | Clipboard put back | Copy some formatted text (it has both plain and HTML content), grab a selection, then paste: the clipboard holds exactly its previous text and its previous HTML content. | | | | |
| R3 | Image on the clipboard | With an image on the clipboard, press the reply key: no copy is injected, and the image is still on the clipboard afterwards. | | | | |
| R4 | Outlook Reply All | With the reply key `Ctrl+Alt+R`, Outlook's Reply All (`Ctrl+Shift+R`) still works. | | | | |
| R5 | Nudges and sweep on time | Using the clock or seeded data: a reply nudges 2 hours after saving and at the default time on working days, and the end-of-day sweep card shows at the end-of-day time on a working day. | | | | |
| R6 | Idle CPU | With the feature on and nothing open, about 0% CPU over 60 s (as row 7). | | | | |

## Phase 3 — hands-free voice

Run in the built app (`wails3 build`) from a temp copy, with `APPDATA` / `LOCALAPPDATA` pointed at scratch folders as in Phase 1.5, voice installed. These need a person speaking into a real microphone; the automated runs cannot.

| # | Check | How | Windows 11 | macOS 14+ | Ubuntu X11 | Ubuntu Wayland |
|---|---|---|---|---|---|---|
| H0 | Wake audio without a user gesture (autoplay) | Quit the app, start a fresh copy (fresh temp profile), do not touch the capture box, wake word on, say "Hey Awesome". The chime is heard and the waveform moves. If not on Windows: add `--autoplay-policy=no-user-gesture-required` to `AdditionalBrowserArgs` in `app.go` and retest; on macOS / Linux use the per-window or WebKit setting in `windows.go`. Record the outcome in the spec's Measurements. | needs human (audible chime, waveform). Automated part done: on a fresh page with no input the chime's AudioContext was `running` at once (CDP, WebView2 158), so no flag was added | | | |
| H1 | One task, as in Phase 1.5 | `Ctrl+M`, say "buy milk", pause. The input shows "buy milk" with the preview (a screen reader hears "Ready to save"); nothing is saved until Enter. | needs human | | | |
| H2 | Several tasks in one session | `Ctrl+M`, say "call Sam tomorrow at 10, new task, buy milk, next task, refactor auth hashtag backend". Two review rows and the third task in the input; the input stays visible while listening and the listening row sits in the hint row. Enter: 3 items in the list. | needs human | | | |
| H3 | scratch that / save all / Undo | "… new task … scratch that" removes the current task. "save all" saves every task and shows "Saved N tasks" with Undo; Undo removes them again and puts them back in the review list. | needs human | | | |
| H4 | Esc and blur keep the text | Esc while listening stops and keeps the rows and the input (pending pieces still land). Switching to another window while listening releases the mic; the next open shows the draft. | needs human | | | |
| H5 | Reply mode stays one line | In reply mode (replies-owed), `Ctrl+M` and say "thanks new task save all": it stays one line of text in the input, no rows, nothing saved. | needs human | | | |
| H6 | Settings › Voice: consent and download | Fresh temp profile. Settings → Voice → turn on "Listen for "Hey Awesome"". The consent box shows the wake files plus the dictation files; Download → progress for dictation, then for the wake word; the switch turns on and the status reads Listening for "Hey Awesome". Not now closes the box with the switch off. Cancel in either phase leaves the switch off with no error. | needs human | | | |
| H7 | Settings › Voice: mic released on off | With the wake word on, the OS mic indicator is showing. Switch it off: the indicator goes within 1 s and the helper process is gone (Task Manager). Quit the app with it on: no helper is left. | needs human | | | |
| H8 | Settings › Voice: mic blocked | Turn the OS mic privacy setting off with the wake word on. After 3 restarts the status line shows the error with Try again; allow the mic and Try again recovers. "Play a sound when it hears you" is disabled while the wake word is off. | needs human | | | |
| H9 | False wake while typing (Review Focus 1) | Focus Notepad and keep typing, say "Hey Awesome": the box appears with the chime, "Listening — say your task" and "Click or press Ctrl+Shift+Space to type"; the typing keeps going into Notepad and nothing lands in the box. Say nothing: the box hides by itself after about 6 s with no error, nothing saved. | needs human | | | |
| H10 | Hands-free, several tasks | "Hey Awesome", then "buy milk, new task, call Sam, save all": 2 items saved, "Saved 2 tasks" with Undo, and the box hides when the note ends. | needs human | | | |
| H11 | Silence keeps the draft | "Hey Awesome", then "buy milk", then silence: the box hides after about 15 s. The next hotkey shows "1 unsaved task from voice" with "buy milk" in the input. | needs human | | | |
| H12 | Wake while open is ignored | Open the box with the hotkey, say "Hey Awesome": nothing changes (no chime, no new session). | needs human | | | |
| H13 | Engaging a wake-opened box | After a wake, click in the box: the "Click or press … to type" hint goes, typing works and dictation goes on under the focused rules (a 2 s pause ends it). Same with the hotkey, and with `Ctrl+M` (it does not stop listening). Where the box gets keyboard focus on a wake (Wayland), stray keys and Enter do nothing until then, and Esc hides the box keeping what was said. | needs human | | | |
| H14 | Wake to listening latency | Screen recording or log timestamps from `wake: detected` to the waveform moving: 500 ms or less. Record it in the spec's Measurements. | needs human | | | |
| H15 | A wake that cannot listen | Delete the dictation model (or block the mic in the OS privacy settings), say "Hey Awesome": the box shows why ("Voice isn't set up yet. Open Settings › Voice to set it up.", or the mic error) for about 4 s, then hides by itself; a draft from before is still there on the next hotkey. A click or the hotkey within the 4 s keeps the box open. The next "Hey Awesome" works again. | needs human | | | |
| H16 | Build and run on macOS | `wails3 build` on a Mac; the cgo file `internal/app/noactivate_darwin.go` has never been compiled. Then install the wake word and say "Hey Awesome". | not applicable (Windows build is done: `wails3 build`, `go vet`, `go test` pass) | needs human | | |
| H17 | Build and run on Linux | `wails3 build` on Ubuntu (X11 and Wayland); `internal/app/noactivate_linux.go` (cgo, GTK accept-focus) and the stock-spotter path have never been compiled or run. Then install the wake word and say "Hey Awesome". | not applicable | | needs human | needs human |
| H18 | macOS mic prompt and Gatekeeper | Fresh macOS user. Turn on the wake word: the microphone prompt names Awesome Reminder (usage string from `Info.plist`) and the downloaded `sherpa-onnx-keyword-spotter-microphone` is not blocked by Gatekeeper (no "cannot be opened" dialog; if it is, note the quarantine handling needed). | not applicable | needs human | not applicable | not applicable |
| H19 | Box appears without taking typing (macOS, Linux X11) | Type in a text editor and say "Hey Awesome": the box shows with the chime, the typing keeps going into the editor. On Wayland, where the box may get focus, stray keys and Enter do nothing until a click or the hotkey (H13). | not applicable (see H9) | needs human | needs human | needs human |
| H20 | Alert pop-up on macOS and Linux | The alert pop-up is now shown without activation (the same native calls): a due reminder shows at the bottom right without stealing typing; clicking **Done**, **Snooze** and **Open**, and dismissing it, all work. | needs human | needs human | needs human | needs human |
| H21 | Sleep and resume with the wake word on | Wake word on, put the computer to sleep, wake it. The log shows the helper restarted (`wake: helper started`) and "Hey Awesome" works again; the OS mic indicator is back. | needs human | needs human | needs human | needs human |
| H22 | Headset unplug and replug | Wake word on with a headset as the default microphone, unplug it, plug it back (or change the default device). Documented limitation: the listener may stay on the old device; switch the wake word off and on again and it works. | needs human | needs human | needs human | needs human |
| H23 | Real-voice accuracy | Normal voice from about 1 m, several speakers if possible: at least 9 of 10 "Hey Awesome" wake. Say near-misses ("hey, I'm awesome", "they're awesome") and note false wakes. Record the result in the spec's Measurements and, if it is poor, retune the keyword boost or threshold in `wake_keywords.txt`. | needs human | needs human | needs human | needs human |
| H24 | One hour false-wake test | Play 1 h of podcast or meeting audio through the speakers with the wake word on. At most 1 false wake. | needs human | needs human | needs human | needs human |
| H25 | Wake word CPU while speech is playing | Listener CPU with continuous speech or music nearby, averaged over 5 min: 10 % of one core or less (the quiet-room figure is measured in the spec). | needs human | needs human | needs human | needs human |
| H26 | Tray checkbox | Tray menu: **Listen for "Hey Awesome"** is checked when the wake word is on. Clicking it turns the wake word on or off like the Settings switch; with the files not downloaded it opens Settings › Voice for the consent instead of turning on. Changing the switch in Settings updates the tray check. | needs human | needs human | needs human | needs human |
| H27 | Idle with the wake word off | Fresh profile, wake word never turned on: no `--wake-helper` process, app process about 50 MB and 0 % CPU for a minute. | done (Task 13: 48 to 50 MB, 0 %, no helper) | | | |
| H28 | Quit leaves no helper | With the wake word on, quit from the tray and kill the app from Task Manager: no `--wake-helper` or `sherpa-onnx-keyword-spotter-microphone` process remains and the mic indicator goes. | needs human | needs human | needs human | needs human |
| H29 | False wake during a real call (speech continues) | During a real call or meeting (people keep talking near the mic), make the wake word fire (say "Hey Awesome" mid-conversation) and do not touch the box. The box ends within 60 s and hides; nothing is saved (sentences ending "that's it" stay text), and what it heard is only the draft on the next hotkey. | needs human | needs human | needs human | needs human |

## Phone push (Phase 4)

These need a phone with the ntfy app (and, for P8, a self-hosted server), so none can be run by a script. Use a scratch profile as above.

| # | Check | How | Windows 11 | macOS 14+ | Ubuntu X11 | Ubuntu Wayland |
|---|---|---|---|---|---|---|
| P1 | Phone buzzes with the laptop closed | Phone push on, subscribed in the ntfy app, grace 2 min. Create `in 5 min test push`, close the lid (or shut down) right away. The phone buzzes at the due time plus the grace. | needs human | needs human | needs human | needs human |
| P2 | Two computers, one buzz | Same topic on two computers (settings synced). Create a reminder due in 5 min on one. The phone buzzes once, not twice. | needs human | needs human | needs human | needs human |
| P3 | Done before due plus grace | Create `in 5 min test push`, mark it done on the computer before due plus grace: the phone does not buzz. | needs human | needs human | needs human | needs human |
| P4 | Dismiss the at-time card | Let the at-time card appear and dismiss it within the grace: no buzz. Dismissing an early "in 10 min" card does not cancel it. | needs human | needs human | needs human | needs human |
| P5 | Phone Done | Tap Done on the phone notification: the item shows done on a computer once one is online (within ntfy.sh's 12 h cache). | needs human | needs human | needs human | needs human |
| P6 | Phone Snooze 1h with the laptop off | Tap Snooze 1h, then turn every computer off: the phone buzzes again an hour later. | needs human | needs human | needs human | needs human |
| P7 | Hidden text | Turn on Hide task text on the phone: the buzz reads "Open Awesome Reminder on a computer." with no task text. | needs human | needs human | needs human | needs human |
| P8 | Token-protected self-hosted topic | On a self-hosted ntfy 2.16+ with an access-controlled topic, save a token that can post to the topic and `<topic>-ctl`: Send test arrives, and the phone's Done and Snooze buttons work. | needs human | needs human | needs human | needs human |
| P9 | Consent note and QR | Turning the switch on (or un-hiding text) shows "Reminder text will be sent to <host>."; scanning the QR code in the ntfy app subscribes to the topic. | needs human | needs human | needs human | needs human |
| P10 | Access token and the data folder | With a token saved, Settings › Data folder › Change… to a new folder: after the restart Send test still works (on Linux without a keychain, `push.key` was copied with the database). Then adopt a folder that already has data: the keychain token is kept and Send test still works. | needs human | needs human | needs human | needs human |

## Data folder

Run in the built app from a temp copy with `APPDATA` / `LOCALAPPDATA` pointed at scratch folders (see the safety note at the top). The macOS and Linux columns are still to be run. The design is in [data-folder-design](superpowers/specs/2026-11-28-data-folder-design.md).

| # | Check | How | Windows 11 | macOS 14+ | Ubuntu X11 | Ubuntu Wayland |
|---|---|---|---|---|---|---|
| D1 | Fresh install | Empty scratch config dir. The first start shows the data folder screen with Standard location, Next to the app, Another folder…. Pick Standard: the app starts, `awesome.db` and `location.json` exist. | | | | |
| D2 | Existing install, Keep | Scratch profile with an old `awesome.db`. The screen has "Keep it where it is" selected; Continue starts the app with the same items and nothing moved. Hey Awesome / dictation still work afterwards. | | | | |
| D3 | Existing install, move | Same profile, choose Another folder…. Progress shows, the app restarts on the new folder with the same items; the old folder has `awesome.db.moved`; the voice folder was moved. | | | | |
| D4 | Adopt a folder that has data | Choose a folder that already holds an `awesome.db`. Its items show, nothing is merged, and Atlas sync keeps its saved connection. | | | | |
| D5 | Folder unavailable | Rename or unplug the data folder and start. "Your data folder isn't available" shows; no empty database is created. Retry after restoring it starts normally; Choose another folder… and Quit work. | | | | |
| D6 | Settings: move | Settings › Data folder › Change…: pick a new folder; the copy is checked, the app switches and the items are all there. Cancel during the copy leaves the old folder in use. | | | | |
| D7 | Settings: Remove old copy | After D6, click Remove old copy: the old `awesome.db.moved` and attachments are deleted, the button goes away. | | | | |
| D8 | Network and cloud warnings | Pick a network share, then a folder under OneDrive / Dropbox / Google Drive / iCloud: each shows a warning before the move proceeds. | | | | |
| D9 | Older exe after a move | After D3, start the previous build against the same profile: it shows an empty list (the old database was renamed). The new build shows the items. | | | | |
| D10 | Wake word after Keep | With the wake word on, do D2 (Keep): after the restart "Hey Awesome" still wakes and the log resolves the voice folder to the local cache path. | | | | |

## Voice accuracy (speech model, "Words I use")

These need the user's own voice and microphone. The design is in [voice-accuracy-design](superpowers/specs/2026-11-28-voice-accuracy-design.md).

| # | Check | How | Windows 11 |
|---|---|---|---|
| VA1 | An existing Base install keeps working | Start the new build with the old voice folder. Settings › Voice › Speech model shows "Base (148 MB)" and the "much more accurate" tip; dictation works without a new download. | |
| VA2 | Switch to Small in the background | Choose "Small (264 MB) · Recommended". Progress and Cancel show; dictating during the download still works (on Base). Afterwards the select shows Small, `models/` holds only `ggml-small.en-q8_0.bin`, and `voice.json` says `"small"`. | |
| VA3 | Cancel a switch | Start a switch, click Cancel: the select returns to the old model, no `.part` file is left, dictation still works. | |
| VA4 | Accent accuracy | Dictate 10 everyday reminders with Indian names and terms on Base, then on Small. Count the misheard words in each. Note the time each phrase takes. | |
| VA5 | Words I use | Add 3 names that Small still gets wrong ("Words I use"); dictate them again; they are spelt as entered. Over 300 characters shows an error and keeps the old list. | |
| VA6 | Fresh install | With an empty voice folder, the capture box offers about 273 MB (engine plus Small). | |
| VA7 | Times | Say "remind me at nine thirty am": the line reads "9:30 am" (not "9.30"), and the parser schedules it. | |
