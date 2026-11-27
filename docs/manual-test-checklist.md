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
