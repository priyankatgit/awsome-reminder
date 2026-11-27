# Manual test checklist

Per-OS verification for the Phase 1 build, from the Verification section of the [design spec](superpowers/specs/2026-11-27-awesome-reminder-design.md). Run it on a release build (`wails3 build`) and record the result per OS.

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
