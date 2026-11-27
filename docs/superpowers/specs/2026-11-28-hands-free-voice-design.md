# Hands-free voice — Design (Phase 3)

Builds on `2026-11-27-awesome-reminder-design.md`, the Phase 1.5 voice work (`docs/superpowers/plans/2026-11-27-phase1.5-voice.md`, `internal/voice/`, `frontend/src/voice.ts`) and `2026-11-27-ui-design-guide.md`. Roadmap position: after Phase 2 (Atlas sync), before Phase 4 (ntfy). It is designed while Phase 2 is being built, so it stays out of the files Phase 2 changes (see "Files and Phase 2 isolation").

The user handed the product and technical calls to the designer ("take your best call"), so every decision below is *[my call]* unless it repeats something the user said earlier (the roadmap line in the README: "Say 'Hey Awesome' to start capturing without the hotkey, and dictate several tasks in one go: saying 'create new task' saves the current one and starts the next").

## Intent / why

The user is a developer who wants less typing. Phase 1.5 removed typing a task. It still needs the hotkey, and it handles one task per dictation. Phase 3 removes the rest of the keyboard from capture:

1. **Wake word.** Saying "Hey Awesome" opens the capture box and starts listening, with no hotkey.
2. **Several tasks in one go.** The user keeps talking. Saying "new task" (or "create new task", "next task") ends the current task and starts the next, so they can get a whole list out of their head in one go.

Success: the user says "Hey Awesome, … new task … new task … save all" with their hands on something else, and three correct tasks are in the list. The app's idle cost is unchanged when the feature is off and small when it is on.

## Goals

1. An opt-in, offline "Hey Awesome" wake word on Windows, macOS and Linux.
2. Continuous dictation in the capture box (from the wake word, the mic button or `Ctrl+M`), split into tasks by a spoken boundary phrase.
3. A review list in the capture box that holds the finished tasks until they are saved together.
4. A few spoken commands so the whole flow can be done by voice: next task, scratch that, save all, cancel.

## Non-goals

- Languages other than English (the whisper model is `base.en`).
- A custom or user-chosen wake phrase (the keyword file makes this cheap later, but it is not in this phase).
- Voice control of anything outside the capture box (opening the list, marking done, snoozing).
- Keeping the review list across an app restart.
- Typing several tasks with a "next task" key (only voice creates list rows in this phase).
- Pausing the wake word automatically while the screen is locked, during calls, or on battery.
- The tray menu toggle until Phase 2 has merged (the last plan task adds it, gated on that).

## Anti-goals (failure even if it works)

- **Anything is saved without an explicit save.** Enter, the "save all" phrase, or nothing. A boundary phrase never saves.
- **A false wake-up steals keystrokes.** If the user is typing in another app when the box opens by mistake, their typing must keep going to that app.
- **The idle cost changes when the feature is off.** No new process, thread, timer or memory unless the wake word is on.
- **Audio or text leaves the computer**, or wake-word audio is written to disk.
- **The mic stays on after the user turns the feature off**, or after the app quits or crashes.
- Single-task dictation (the common case) gets slower or more awkward than in Phase 1.5.

## Constraints

- Today the app idles at about 137 MB and 0 % CPU. With the wake word off, Phase 3 adds nothing to that. With it on, the budget is in "Budgets and the spike gate".
- Windows builds use `CGO_ENABLED=0` (`build/windows/Taskfile.yml`). No cgo on Windows.
- Phase 1.5 rules still hold: nothing is downloaded without an explicit click; every download is checked against a pinned SHA-256 and size; voice files live under `<os.UserCacheDir()>/awesome-reminder/voice/`; whisper runs only while dictating and exits after 30 s idle.
- The capture window hides when it loses focus (`internal/app/windows.go`, `WindowLostFocus` → `HideCapture`), and the page drops any recording on `blur` (`capture.ts`).
- whisper transcribes at most 30 s of audio per request.
- The UI follows the design guide: tokens only in `tokens.css`, Lucide icons, no emoji, motion ≤ 180 ms except the listening pulse, reduced-motion respected, ≥ 4.5:1 contrast.

## Approaches considered

### Wake-word engine

| | Option | Trade-off |
|---|---|---|
| **1 ✅** | **sherpa-onnx open-vocabulary keyword spotting, run as a helper process** (the prebuilt microphone keyword spotter from the official sherpa-onnx release, with the 3.3M-parameter GigaSpeech KWS model) | Apache-2.0, offline, no account. The keyword is given as text, so no training. Prebuilt for Windows x64, Linux x64/arm64 and macOS, so macOS gets the wake word without Homebrew. It runs in a separate process (like `whisper-server`), so the app's own memory is unchanged and it can be killed at once. The stock binary runs the model on all audio, with no loudness check in front, so the CPU it uses in a quiet room has to be measured (spike gate). |
| 2 | Our own helper (the same executable started with a helper flag) that loads the sherpa-onnx C library through `purego` and does its own mic capture | Full control: a loudness check in front would mean truly near-zero CPU in a quiet room, and audio from just before the wake word could be kept. But native mic capture without cgo on three OSes (WASAPI, CoreAudio, ALSA/Pulse) is a large job. **Fallback if option 1 fails the spike gate.** |
| 3 | Listen in the hidden capture webview (`getUserMedia` always on) and feed audio to a spotter | WebKit (macOS WKWebView, Linux WebKitGTK) mutes or refuses capture in hidden windows, and WebView2 behaviour while hidden is not guaranteed. Rejected. |
| — | Porcupine (Picovoice) | Lightest engine, but needs an AccessKey checked online, with paid tiers. Rejected (no accounts, zero cost). |
| — | openWakeWord | Needs a custom model trained for "hey awesome" (Python, synthetic speech) and ships with Python. Rejected. |
| — | OS speech APIs (SAPI / Windows.Media, macOS Speech, nothing usable on Linux) | Three implementations, and Linux has no engine at all. Rejected. |

### Finding the "new task" boundary

| | Option | Trade-off |
|---|---|---|
| **A ✅** | **Cut the audio at pauses, transcribe each piece with whisper while recording continues, and find the phrases in the text** | Simple, testable as plain string functions, and text appears live as the user speaks. Depends on whisper writing the phrase correctly; a fixed list of variants covers that. |
| B | Have the keyword spotter also listen for "new task" and cut the audio there | Accurate cut points, but the helper and the webview record separately, so their timelines would have to be lined up. Much more machinery. Rejected. |

### Saving several tasks

| | Option | Trade-off |
|---|---|---|
| **i ✅** | **Review list**: finished tasks wait in the box; Enter or "save all" saves them all | Keeps "nothing saves without an explicit save". Mistakes are fixed before they reach the database (and Atlas once Phase 2 lands). |
| ii | Save each task as soon as its boundary is heard, with an Undo toast | Faster, but every mishearing is written and then synced. It breaks the Phase 1.5 rule. Rejected. |

This changes the README roadmap line ("saying 'create new task' saves the current one"). That line conflicts with the Phase 1.5 rule "dictation never saves on its own", which was set at the user's request. The user was asked to choose between i and ii and left the call to the designer. In this design, "new task" moves the current task into the review list, and a single spoken "save all" saves the whole list, so the flow stays hands-free. The README roadmap line is reworded to match when this phase ships.

## Decisions

### 1. Wake listener (Go, `internal/voice`)

**Engine.** The sherpa-onnx release's microphone keyword-spotter program (expected name `sherpa-onnx-keyword-spotter-microphone[.exe]`; the spike confirms the name, its arguments and its output format). It is started with the KWS model's encoder, decoder, joiner and tokens files, a keywords file, `--num-threads=1` and the CPU provider. It opens the default microphone through PortAudio (built into the binary) and prints a line to stdout for each detection.

**Model.** `sherpa-onnx-kws-zipformer-gigaspeech-3.3M-2024-01-01` (English, 17.6 MB archive in the `kws-models` release). Use its int8 ONNX files if they meet the accuracy gate, otherwise the fp32 ones; the spike decides and records which.

**Keywords.** One phrase, "hey awesome". The keywords file holds its BPE tokens for the GigaSpeech model, a boost score, a trigger threshold and a label (`@HEY_AWESOME`). The spike generates the tokens once with sherpa-onnx's `text2token` tool and tunes the threshold. The file is committed and embedded in the binary (`internal/voice/wake_keywords.txt`), so users never need Python.

**Assets and consent.** Same pattern as Phase 1.5 (`assets.go`, `download.go`, `extract.go`):
- Pinned per platform: the sherpa-onnx release's `*-shared` archive for windows/amd64, linux/amd64, linux/arm64, darwin/arm64 and darwin/amd64 (the release current when the plan is executed; v2.0.3 at the time of writing). Plus the KWS model archive. URL, size and SHA-256 for each go in a table in `wake_assets.go`.
- The archives are `.tar.bz2`; Go's `compress/bzip2` reads them. `Extract` gets a per-asset file filter (today its filter is hard-coded to whisper files). Only the spotter program, its shared libraries (onnxruntime, sherpa-onnx, and on Windows whatever DLLs it imports) and the model files are kept.
- Layout: `voice/wake-<release>/` (program and libraries, with a `.ok` marker holding the release name) and `voice/models/kws-gigaspeech-3.3M/`.
- Download size is shown before consent (roughly 22–30 MB for the program archive plus 17.6 MB for the model; the exact figures come from the pinned table). If whisper is not installed yet, the same consent covers both downloads, because a wake word without dictation is useless.
- Windows: the same Visual C++ runtime check as whisper (`RuntimeMissing`), with the same hint.

**Process lifecycle.** A `voice.Wake` type owns the helper:
- It runs only while the wake word is **enabled** and **installed**. Turning it off kills the process at once, so the OS mic indicator goes out.
- It is started with the existing platform hooks (`hideAndContain`, `afterStart`: hidden window, Windows job object with kill-on-close, Linux `Pdeathsig`), so it dies with the app. `Close()` on app shutdown kills it.
- It is restarted if it exits unexpectedly: after 1 s, 5 s, then 30 s. After 3 unexpected exits within 10 minutes it stays stopped, in an `error` state that carries the last stderr line, shown in Settings. Toggling it off and on resets the count.
- After the computer resumes from sleep, it is restarted (a PortAudio stream often dies across sleep). The resume signal: a wall-clock jump of more than 5 s between 1 s ticks, the same heuristic as `scheduler.wallJumped`, run in `Wake`'s own goroutine only while enabled.
- A detection is reported as an event (`voice:wake`) unless one of these holds: the capture box is visible, a wake was reported less than 3 s ago, or a dictation session ended less than 2 s ago (so the box's own chime and the user's last words cannot retrigger it).
- `Wake` reports its state through `voice:wake-status`: `off | absent | installing | starting | listening | error | unsupported`, with download progress, an error text and a hint.

**Enabled flag storage.** In `voice/wake.json` (`{"enabled": bool, "chime": bool}`), next to the voice files, **not** in `settings.Settings`. Reasons: the setting belongs to one device (which mic, which room) and must not sync through Atlas in Phase 2; it keeps the store, core and settings packages untouched; and deleting the voice folder, which removes the engine, also turns the feature off, which is consistent. Default: off, chime on.

### 2. From wake word to capture box

- On `voice:wake`, Go shows the capture box **without activating it** (new `ShowCaptureForWake`, next to `ShowCapture` in `windows.go`, using the existing `showNoActivate` on Windows), centred on the screen under the cursor like the hotkey, and emits a new event `capture:wake` (registered in the new Go file, so `events.go` is untouched).
- The page runs the same `open()` as for the hotkey (keeps any draft), plays the chime if enabled, and starts a dictation session in **wake mode**.
- **Chime:** a short soft two-note blip (about 150 ms) made with WebAudio, so there is no asset. Played before the mic opens, so it is not recorded.
- **Focus:** in wake mode the box does not have keyboard focus, so keys keep going to the user's app (anti-goal). The box is driven by voice. A click on the box, or the hotkey, gives it focus as usual (`ShowCapture` already focuses an open box). From then on the session follows focused-mode rules.
- **Showing without focus, per OS.** Wails v3.1.0 can't do this outside Windows (`noactivate_other.go`).
  - **macOS:** `ShowCaptureForWake` calls `orderFrontRegardless` on the NSWindow through cgo. darwin builds already use cgo (`dock_darwin.go`), so this shows the window without activating the app.
  - **Linux (X11):** the window is set to not accept focus (`gtk_window_set_accept_focus(FALSE)`) before showing, and set back on a click or the hotkey. Linux builds already use cgo.
  - **Wayland:** the compositor decides.
- **Keyboard guard (all OSes, safety net).** In case the box does get focus on a wake (Wayland, or a native call that fails), the page tracks whether the user has clicked in the box, pressed the hotkey or pressed `Ctrl+M` since the wake. Until one of those happens, a wake-mode page ignores printable keys, Enter, Backspace and Tab, so stray typing can't reach the input or save anything. Esc still cancels the session (and hides the box). The hint row shows "Click or press Ctrl+Shift+Space to type" (with the configured hotkey). Swallowed keystrokes are the accepted cost on those systems; corrupting the draft or saving is not acceptable.
- **Audio without a user gesture.** Every Phase 1.5 recording starts from a key or click. A wake starts one with no gesture, and Chromium (WebView2) and WebKit may create the `AudioContext` suspended, which silences both the chime and the recorder. The page calls `ctx.resume()` and checks `ctx.state`. The spike tests this on each OS from a freshly started app, before the capture page has had any user input.
  - If it is blocked on Windows, add the WebView2 browser argument `--autoplay-policy=no-user-gesture-required` to the app's Windows options. This is the one allowed change to `app.go` beyond the existing line. It is a single option line, kept small to avoid conflicts with Phase 2.
  - If it is blocked on macOS, use the per-window autoplay option in `windows.go`.
  - If it is blocked on Linux (WebKitGTK), use the equivalent WebKit setting on the capture window.
  - If none of these exist on an OS, the wake word is marked `unsupported` there, with a hint.
- **Known limitation:** the capture page needs roughly 200–500 ms to open the mic after the wake word. Words spoken in one breath with "Hey Awesome" may be cut. The listening row says "Listening — say your task" and the chime marks the moment. The spike measures the delay (gate below). Fallback approach 2 would remove the limitation by keeping audio from before the wake word.
- **False wake-ups:** the threshold is tuned for few false accepts (gate below). A false wake costs a chime and a brief box: if no speech starts within 6 s, the session ends and the box hides quietly, with no "Didn't catch that" error. Nothing is ever saved without an explicit save.

### 3. Continuous dictation (all sessions)

Every dictation becomes a session that can hold several tasks: wake word, mic button or `Ctrl+M`. This replaces the Phase 1.5 one-shot recording. For one task it feels the same: speak, pause, review, Enter.

**Segments.** The `Recorder` in `voice.ts` gains a segment mode:
- It keeps a 300 ms pre-roll so a segment does not start mid-word.
- After speech, 700 ms of silence ends a segment: the audio from the pre-roll to now goes to `onSegment(pcm)` and recording continues.
- A segment is force-cut at 20 s of continuous speech, at the quietest 100 ms window of its last 2 s, so no request exceeds whisper's 30 s.
- A segment with less than 250 ms of speech frames is dropped (clicks, coughs).
- The existing RMS speech test and noise floor are reused unchanged.

**Transcription.** Segments go through an ordered queue, one whisper request at a time, and results are applied in order.
- `VoiceService` gains `TranscribeSegment(ctx, pcm, prompt) (string, error)`. It is like `Transcribe`, but passes whisper-server's `prompt` field: the last ~200 characters of the session's text plus the phrase "new task", to bias spelling and continuity. On the `whisper-cli` engine (macOS) it passes `--prompt`.
- It also drops whisper's known silence hallucinations when the segment has under 1.5 s of speech: "thank you", "thanks for watching", "thank you for watching", "you", "bye", compared after normalising.
- While a segment is being transcribed, the input shows a small shimmer at its end.
- A failed segment shows its error inline ("Couldn't transcribe the last bit: …") and the session goes on.
- `api.voice.warm()` still runs when the session starts.

**When a session ends:**

| Mode | Ends after | Also ends on |
|---|---|---|
| Focused (hotkey box, mic button, `Ctrl+M`) | 2 s of silence after speech (Phase 1.5 used 1.5 s; segments are already transcribed by then, so the result arrives sooner overall). After a boundary phrase it waits up to 8 s for the next task. No speech at all for 8 s: "Didn't catch that", as today. | `Ctrl+M` or the stop button; Enter; Esc (cancel); "save all" / "cancel"; window blur (cancel, as today); 5 min total (stops and keeps the tasks, note "Stopped after 5 minutes") |
| Wake (box not focused) | 15 s of silence after speech. No speech for 6 s after the wake: hides quietly. | "save all" / "cancel"; 5 min total; the user focuses the box (switches to focused rules) |

**Esc and blur never silently discard finished tasks:**
- **Esc while listening:** stops listening. Only the audio not yet cut into a segment is dropped. Queued segments are still transcribed, and the rows and input stay for review.
- **Esc when not listening, with no rows:** hides the box and clears the input, as today.
- **Esc when not listening, with one or more rows:** a first Esc shows an inline confirm, "Discard 3 tasks? Esc again to discard · ↵ to save". A second Esc within 3 s discards and hides. Anything else dismisses the confirm.
- **Window blur (any state):** releases the mic and drops the audio not yet cut into a segment, as today. Queued segments finish transcribing in the background into the draft. The rows and input are kept as the draft for the next open, as the input is today.

When a session ends without a save, all pending segments are transcribed first. Then the box shows the review list and the input for editing. In focused mode, Enter saves. In wake mode, the box hides after the session ends. The list and the input stay as the draft, the same way the input text survives a blur today, and the next open shows them with a note: "3 unsaved tasks from voice".

**Single-task compatibility.** Saying one task and pausing gives exactly the Phase 1.5 result: the text in the input, the preview, "Ready to save", Enter saves.

### 4. Spoken commands

A pure TS module, `frontend/src/voice-commands.ts`, unit-tested with Vitest. It turns each segment's text into operations: `text(s) | boundary | scratch | save | cancel`.

Matching: case-insensitive, ignoring `.,!?;:"'` and repeated spaces, whole words only.

| Operation | Phrases | Where it may appear |
|---|---|---|
| boundary | "new task", "create new task", "create a new task", "next task", "add another task" | Anywhere in a segment. Text before it goes to the current task and text after it starts the next one. Several per segment are fine. Leftover punctuation or "and"/"okay" at the edges of a piece is trimmed. |
| scratch | "scratch that", "delete that" | The whole segment, or its last sentence (after whisper's `.`, `?` or `!`) |
| save | "save all", "save them", "save them all", "save tasks", "that's all", "that's it" | The whole segment, or its last sentence |
| cancel | "cancel", "cancel all", "never mind" | The whole segment, or its last sentence |

Restricting the last three to a whole segment or a final sentence means a task such as "remind me to save all the logs" or "cancel the dentist appointment" stays text. A boundary phrase inside a task ("new task template") is the accepted cost. The README tells the user to type such a task.

Effects:
- **text:** appended to the input (space-separated), with the existing dictation fade-in. The preview refreshes.
- **boundary:** if the input has text, it becomes a new row at the end of the review list and the input clears. An empty input means no change. Announced as "Task 2 added".
- **scratch:** clears the input. If the input is already empty, it removes the last list row. Announced as "Removed".
- **save:** ends listening, waits for pending segments, then saves every row plus a non-empty input (see Saving). After a spoken save the box shows "Saved 3 tasks" with an **Undo** button (`Ctrl+Z` when focused) for 5 s, then hides. Undo deletes those items (`Service.Delete`, the same soft delete as the list) and puts them back in the review list.
- **cancel:** ends listening, clears the list and the input, and shows "Discarded 3 tasks" with **Undo** for 5 s (Undo restores them), then hides.

### 5. Review list (capture box UI)

```
┌────────────────────────────────────────────────────────────┐
│  1  ☐  refactor auth                 To-do · #backend    × │  ← review rows, mono 14px
│  2  🔔 check deploy        Reminder · Thu 26 Nov 17:00   × │     (bell / check-square icons, not emoji)
├────────────────────────────────────────────────────────────┤
│  ◉  call Sam about the invoice tomorrow at 10▍        [■]  │  ← input = current task; mic is the stop button
│     Reminder · Fri 27 Nov 10:00                            │  ← live preview of the current task
└────────────────────────────────────────────────────────────┘
   ◉ Listening ▁▃▅▇▅▃▁ 0:42 · "new task" next · "save all" done · Esc cancel
```

- The list sits above the input row, newest at the bottom (next to the input). Each row has a number, a kind icon (`bell` in `--remind` for a reminder, `check-square` for a to-do), the text, a compact preview (reminder chip or "To-do", tag chips, priority dot) and a × button (`aria-label="Remove task 2"`). Each row's preview comes from `api.preview(text)` when the row is added.
- While listening, the input stays visible (Phase 1.5 hid it behind the listening row) because it shows the current task as it grows. The listening indicators (pulse dot, "Listening…", waveform, timer) move into the hint row, which is replaced while listening. The mic button stays a stop button with its pulse ring. The design guide's "Listening state" is updated to match.
- Keys (focused): `Up` from the input moves into the list (last row); `Up`/`Down` move; `Delete` removes the row; `Enter` on a row moves it into the input for editing (any input text becomes a row in its place); `Esc` in the list returns to the input. Clicking a row's text does the same as `Enter`.
- The hint row shows `↵ save 3` when there is more than one task.
- At most 6 rows show; more scroll inside the list. The box still grows to fit up to `captureMaxHeight`, through the existing `ResizeObserver`.
- `role="status"` announces "Task 2 added", "Removed", "Saved 3 tasks", "Discarded".

**Saving.** Enter (or "save all") saves the rows in order and then the input, one `api.capture` call each. Enter with rows but an empty input is allowed. Enter while listening stops listening, waits for pending segments, then saves. If a save fails, saving stops there: saved rows leave the list, the failing row shows the error inline, and the rest stay. A save by Enter flashes and hides as today, with no Undo note. Only a spoken save shows the Undo note. No batch API is added; the frontend loop calls the existing `Capture`.

The draft (rows plus input) survives hide/show like the input does today. It is cleared only by a save, the confirmed double Esc, or "cancel" (which has Undo).

### 6. Settings: a "Voice" section

An additive section in `settings.ts`, wired to new `VoiceService` methods (`WakeStatus`, `SetWakeEnabled`, `SetWakeChime`, `InstallWake`, `CancelWakeInstall`), **not** to `SaveSettings`. Its switches apply at once, outside the form's Save button, and the section says so ("Voice settings apply right away").

- **Listen for "Hey Awesome"** (switch, off by default). Help text: "Opens the capture box when you say "Hey Awesome". While this is on, the microphone stays on, so your system shows that Awesome Reminder is using it. Audio is processed on this computer and never stored or sent anywhere. Uses about N MB of memory and a little CPU." (N comes from the spike measurement.)
- Turning it on when the files are absent shows an inline consent box with the download size (wake files, plus whisper if needed), **Download** and **Not now**, then a progress bar with **Cancel**. When the download finishes, the switch turns on.
- **Play a sound when it hears you** (switch, on by default; disabled while the wake word is off).
- A status line: "Listening for "Hey Awesome"", "Starting…", or the error with **Try again**.
- If dictation is `unsupported` (macOS without Homebrew whisper, Windows without the VC++ runtime), the wake switch is disabled and the existing hint is shown.

### 7. Behaviour per OS

| | Wake word | Notes |
|---|---|---|
| Windows x64 | Prebuilt sherpa-onnx, downloaded on consent | Shown without focus through `showNoActivate`. Needs "Let desktop apps access your microphone"; if the helper can't open the mic, the status shows the same hint as dictation. |
| macOS (arm64, x64) | Prebuilt sherpa-onnx, downloaded on consent | Dictation still needs `brew install whisper.cpp` (Phase 1.5). The mic prompt should be attributed to Awesome Reminder as the responsible process; verify. `NSMicrophoneUsageDescription` is updated to mention the wake word. Files downloaded by Go's HTTP client get no quarantine flag, so Gatekeeper should not block the helper; verify, and if it is blocked, show an error with a hint. Shown without focus through `orderFrontRegardless` (cgo), with the keyboard guard as the safety net. |
| Linux x64/arm64 | Prebuilt sherpa-onnx, downloaded on consent | PortAudio uses the ALSA default device; PipeWire and PulseAudio provide it. On X11 the box is shown without accepting focus. On Wayland the compositor decides, and the keyboard guard protects the draft. Needs glibc of a similar age to the whisper build; record it in the README. |

### 8. Privacy

- Off by default. Turning it on is an explicit choice, and the help text says the mic stays on.
- Audio is processed in the helper's memory only. Nothing is written to disk, and nothing is sent: the stock program has no network function, and the app passes it none.
- Turning it off kills the helper at once, and the OS mic indicator goes off. Quitting or crashing the app kills it too (job object / `Pdeathsig`).
- `awesome.log` records only `wake: detected` with a time, helper start/stop/restart and errors. It never records transcripts or dictated text.

## Budgets and the spike gate

The first plan task is a measured spike on the user's Windows machine, with Linux and macOS checks in the manual test pass. Its results go into this document's "Measurements" section (to add) and the README.

| Gate | Target |
|---|---|
| Helper memory | ≤ 80 MB working set |
| Helper CPU, quiet room | ≤ 3 % of one core, averaged over 5 min |
| Helper CPU, continuous speech or music nearby | ≤ 10 % of one core, averaged over 5 min |
| Wake word to box listening | ≤ 500 ms |
| False wakes | ≤ 1 per hour of podcast or meeting audio played through the speakers |
| Missed wakes | ≥ 9 of 10 "Hey Awesome" at normal voice from 1 m |

If a gate fails: try the other model variant (int8 / fp32, or the `-mobile` archive), the thread count, and the threshold or boost. If it still fails, **stop and bring the measurements back to the user** with fallback approach 2 (our own helper with a loudness check) as the proposal. Do not ship a wake word that misses the CPU gate.

The spike also confirms: the program's name and arguments, its stdout format, which shared libraries it needs on each OS, the keyword tokens, and whether a capture page with no user input yet can start an `AudioContext` (see "Audio without a user gesture").

## Files and Phase 2 isolation

- **New Go:** `internal/voice/wake.go`, `wake_assets.go`, `wake_keywords.txt` (embedded), tests; `internal/app/wake.go` (event registration, wake wiring into `VoiceService`, `capture:wake`).
- **Changed Go (small):** `internal/voice/extract.go` (file filter per asset, bzip2), `internal/voice/manager.go` / `engine.go` / `cli.go` (prompt pass-through for segments), `internal/voice/text.go` (hallucination filter), `internal/app/voice.go` (new methods; `registerVoice` also creates `Wake`), `internal/app/windows.go` (`ShowCaptureForWake`), `build/darwin/Info.plist` (usage string).
- **New TS:** `frontend/src/voice-commands.ts` (+ `.test.ts`), `frontend/src/voice-session.ts` (session controller: segment queue, commands, end rules, modes) if `voice.ts` gets too big.
- **Changed TS/CSS:** `voice.ts` (segment mode, chime), `capture.ts` (review list, wake mode, Enter saves all), `api.ts`, `settings.ts` (Voice section), `tokens.css`, `style.css`, `icons.ts`.
- **Docs:** README (Voice section, roadmap status), `docs/manual-test-checklist.md`, UI design guide ("Listening state", review list, Settings Voice section).
- **Native show-without-focus:** `internal/app/noactivate_darwin.go` (cgo, `orderFrontRegardless`) and `internal/app/noactivate_linux.go` (cgo, GTK accept-focus). These split today's `noactivate_other.go`, which then covers only the remaining OSes.
- **Not touched:** `internal/store`, `internal/core`, `internal/settings`, `internal/app/events.go`, and `app.go` beyond the existing `registerVoice(app)` call. The one exception is the WebView2 autoplay option line, and only if the spike shows it is needed. The tray icon/status and menu code stays untouched until Phase 2 has merged. The last plan task (gated on Phase 2 being in `master`) adds one tray menu checkbox, "Listen for "Hey Awesome"", which calls the same `SetWakeEnabled`.

## Review focus (what must not break)

1. **A false wake while typing:** the box appears, the user's typing stays in their app, and the box hides by itself 6 s later with nothing saved.
2. **A misheard phrase never saves:** a boundary only creates a row. "save all" inside a longer sentence stays text. Every spoken save and cancel can be undone for 5 s.
3. **The helper crashes or can't open the mic:** restarts with backoff, then an `error` state in Settings. The app keeps working. Nothing loops at 100 % CPU.
4. **Off means off:** the switch, quitting the app and killing the app all stop the helper and release the mic.
5. **A segment's transcription fails, or whisper dies mid-session:** an inline error, the session continues, and the next segment starts a fresh whisper process.
6. **Sleep/resume and mic unplug/replug:** the helper is restarted after resume. A device change is a documented limitation (toggle off/on).
7. **Single-task dictation** still works as in Phase 1.5, including blur cancelling and Esc.

## Left to the builder

- Exact pixel layout of the review list and the moved listening indicators, within the design guide and the sketch above.
- The tuning constants (700 ms segment pause, 2 s / 8 s / 15 s / 6 s end rules, 3 s wake cooldown, thresholds), if testing shows better values. Record the changes in this document.
- How the helper's stdout is parsed, and how its stderr is turned into a user-facing error.
- The macOS "show without focus" mechanism.
- Whether the session controller lives in `voice.ts` or a new `voice-session.ts`.

## Summary of my calls

All decisions in this document are mine, made at the user's request. The ones most worth checking:

1. sherpa-onnx keyword spotting as a separate helper process, with a CPU/memory spike gate and a fallback.
2. The wake word is off by default, stored per device in `voice/wake.json` (not synced), and switched on and off in Settings (tray toggle after Phase 2).
3. A wake opens the box without keyboard focus, and the box hides quietly if no speech follows within 6 s.
4. Every dictation becomes a multi-task session. For a single task it behaves as before, with a 2 s pause instead of 1.5 s.
5. A review list, with Enter or "save all" saving everything, and Undo after a spoken save or cancel. **This reverses the README roadmap line**, which had "new task" saving at once.
6. Fixed English phrase lists for boundary / scratch / save / cancel. Commands other than the boundary only count as a whole segment or a final sentence.
7. Esc with finished tasks in the box asks first (double Esc). Blur keeps everything as the draft.
8. On macOS and Linux X11, the box is shown without focus through native cgo calls. Everywhere, a keyboard guard ignores typing in a wake-opened box until the user clicks it or presses the hotkey.
9. If the spike finds that audio without a user gesture is blocked on Windows, one option line is added to `app.go` (the WebView2 autoplay flag).
