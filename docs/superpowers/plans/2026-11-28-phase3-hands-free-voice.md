# Phase 3: Hands-free Voice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Saying "Hey Awesome" opens the capture box and starts listening, and any dictation can hold several tasks: "new task" moves the current one into a review list, and Enter or "save all" saves them all.

**Architecture:** A new `voice.Wake` in `internal/voice` downloads (after consent) and supervises the prebuilt sherpa-onnx microphone keyword spotter as a helper process. It reports detections as an event, and Go then shows the capture box without focus. In the page, the Phase 1.5 `Recorder` gains a segment mode: audio is cut at pauses and each piece goes to a new `TranscribeSegment` binding while recording continues. A pure command parser turns each piece's text into text / boundary / scratch / save / cancel operations, which drive a review list in the capture box.

**Tech Stack:** Go (stdlib `os/exec`, `compress/bzip2`, `archive/tar`), Wails v3.1.0 bindings and events, cgo on darwin/linux only (show without focus), vanilla TS, Web Audio API, Vitest (jsdom), sherpa-onnx (prebuilt release, Apache-2.0), whisper.cpp (Phase 1.5).

**Spec:** `docs/superpowers/specs/2026-11-28-hands-free-voice-design.md` (approved by the user, 2026-11-28). Read it with this plan. Its "Decisions" section is authoritative wherever this plan is silent.

**When it lands:** after Phase 2 (Atlas sync, branch `claude/phase2-atlas-sync`) and the replies-owed feature (plan `docs/superpowers/plans/2026-11-28-replies-owed.md` on `claude/replies-owed-design`), which is queued behind Phase 2. Start from `master` once both have merged. Do not start before that unless the user says so.

## Global Constraints

- Nothing new runs, and nothing new is held in memory, while the wake word is off. Off by default (`voice/wake.json` missing → `{"enabled": false, "chime": true}`).
- Nothing is saved without Enter or a spoken save. A boundary phrase only creates a review row.
- No audio or text leaves the computer. Wake audio is never written to disk. `awesome.log` gets only `wake: detected`, helper start/stop/restart/errors, and never transcripts.
- Downloads only after an explicit click, each checked against a pinned SHA-256 and size (existing `Download`). Files go under `<os.UserCacheDir()>/awesome-reminder/voice/`: `wake-<release>/` (program + libraries + `.ok`) and `models/kws-gigaspeech-3.3M/`.
- Helper: `--num-threads=1`, CPU provider; started through the existing `hideAndContain` / `afterStart` hooks (Windows job object, Linux `Pdeathsig`); killed on off, quit and `Close()`.
- Restart backoff 1 s, 5 s, 30 s; 3 unexpected exits within 10 min → `error` state. Restart after a wall-clock jump > 5 s between 1 s ticks (resume).
- Wake suppression: capture box visible; < 3 s since the last reported wake; < 2 s since a dictation session ended.
- Windows builds stay `CGO_ENABLED=0`. cgo only in `_darwin.go` / `_linux.go` files.
- Phase 2 isolation (from the spec, still binding after Phase 2 merges, to keep the diff small): no changes to `internal/store`, `internal/core`, `internal/settings`, `internal/app/events.go`. `app.go` changes are limited to (a) the existing `registerVoice(app)` line becoming `registerVoice(app, win)`, (b) the WebView2 autoplay option line, only if Task 9 finds it is needed, (c) Task 12's tray menu item.
- Session timing (spec §3): pre-roll 300 ms; segment cut after 700 ms of silence; force cut at 20 s at the quietest 100 ms window of the last 2 s; drop segments with < 250 ms of speech; focused mode ends 2 s after speech, 8 s after a boundary, or "nothing" after 8 s with no speech; wake mode ends 15 s after speech, or hides quietly after 6 s with no speech; 5 min cap for both.
- Hallucination filter (Go): segments with < 1500 ms of speech whose normalised text is `thank you`, `thanks for watching`, `thank you for watching`, `you` or `bye` become `""`.
- Command phrases, exactly as spec §4 (copied into Task 6).
- Copy (exact): Settings help text and switch labels from spec §6; "Didn't catch that. Try again closer to the mic." (existing); "Saved N tasks" / "Discarded N tasks" with **Undo** (5 s); "Discard N tasks? Esc again to discard · ↵ to save" (3 s); "N unsaved tasks from voice"; "Listening — say your task"; "Click or press <hotkey> to type"; "Couldn't transcribe the last bit: <error>"; "Stopped after 5 minutes"; macOS `NSMicrophoneUsageDescription`: `Awesome Reminder listens while you dictate, and for "Hey Awesome" if you turn that on. Audio is processed on this computer.`
- UI: design guide rules (tokens only in `tokens.css`, Lucide icons, no emoji, ≤ 180 ms motion except the listening pulse, reduced motion, ≥ 4.5:1 contrast, every icon button has an `aria-label`).

## Review Focus

1. **The helper cannot open the mic** (Windows mic privacy off, no device, device busy): the helper exits at once and repeatedly. It must reach `error` with a readable message after 3 exits, not loop forever or spin CPU. → Task 4 `TestWakeGivesUpAfterThreeExits` (fake helper exiting at once with a PortAudio-like stderr line; the error carries it).
2. **whisper dies or errors mid-session:** the failed segment shows an inline error, later segments are still transcribed in order, and the rows already made survive. → Task 7 `segment queue keeps order and survives a failed segment`.
3. **A wake while the reply-mode box is open, or Ctrl+M in reply mode** (replies-owed): the wake is suppressed (the box is visible), and dictation in reply mode is single-task: boundary phrases stay text and save/cancel are not commands. → Task 6 `multi:false keeps boundaries as text` and the Task 8 manual step.
4. **The user types while a wake-opened box has focus** (Wayland, or a native show-without-focus that failed): printable keys, Enter, Backspace and Tab are ignored until a click, the hotkey or Ctrl+M. → Task 10 `keyGuard` tests.
5. **A save fails halfway through the list** (database error on row 2 of 4): row 1 is saved and leaves the list, row 2 shows the error, rows 3–4 stay, nothing is saved twice on retry. → Task 8 `saveSequential stops at the first failure`.

---

### Task 0: Spike — pin the engine, measure, decide

No product code. The result is pinned values, a keywords file and measurements, all committed.

**Files:**
- Create: `internal/voice/wake_keywords.txt`, `docs/superpowers/notes/2026-phase3-spike.md` (raw numbers and commands)
- Modify: `docs/superpowers/specs/2026-11-28-hands-free-voice-design.md` (add a "Measurements" section)

- [ ] **Step 1:** Pick the current sherpa-onnx release (v2.0.3 or newer). For each of windows/amd64 (`*-win-x64-shared.tar.bz2`), linux/amd64 (`*-linux-x64-shared.tar.bz2`), linux/arm64 (`*-linux-aarch64-shared-cpu.tar.bz2`), darwin/arm64 (`*-osx-arm64-shared.tar.bz2`) and darwin/amd64 (`*-osx-x64-shared.tar.bz2`, or universal2 if that is what is published), plus `kws-models/sherpa-onnx-kws-zipformer-gigaspeech-3.3M-2024-01-01.tar.bz2`, record the URL, the size and the SHA-256 (download into the scratchpad; ask the user first if your harness requires it). Expected: 6 rows in the notes file.
- [ ] **Step 2:** List each archive and record the microphone keyword-spotter program's exact name (expected `bin/sherpa-onnx-keyword-spotter-microphone[.exe]`), the shared libraries it loads (Windows: `dumpbin /dependents` or `objdump -p`; Linux: `ldd`; macOS: `otool -L`, from the archive listing if no Mac is at hand), and the model files (int8 and fp32 encoder/decoder/joiner, `tokens.txt`). **Ruling during execution (2026-11-28):** the v2.0.3 Windows archives ship no microphone spotter (Linux and macOS do). Windows therefore uses our own helper (Task 4b, spec fallback approach 2). For Windows, Task 0 pins the smallest MT `-lib` archive (DLLs only), records the C API declarations and the stock program's stdout format, and estimates CPU from the file-based spotter's real-time factor.
- [ ] **Step 3:** In a scratch venv, `pip install sherpa-onnx sentencepiece`, then run `sherpa-onnx-cli text2token --tokens tokens.txt --tokens-type bpe --bpe-model bpe.model` on the input line `HEY AWESOME :2.0 #0.25 @HEY_AWESOME` (boost and threshold are starting values). Write the output line to `internal/voice/wake_keywords.txt`.
- [ ] **Step 4:** Run the Windows program from the extracted folder with the int8 model, `--keywords-file`, `--num-threads=1`, `--provider=cpu`. Record its exact argument list and three sample stdout lines for a detection (and anything printed at start-up), plus its stderr and exit code when the mic is blocked (Settings › Privacy › Microphone off).
- [ ] **Step 5:** Measure on the user's machine and record: working set (`Get-Process` every 5 s) and CPU (`(Get-Counter '\Process(<name>)\% Processor Time')` every 5 s, divided by 100 for "of one core") over 5 min in a quiet room and 5 min with speech or music; 10 "Hey Awesome" at normal voice from 1 m; false wakes over 1 h of podcast/meeting audio through the speakers. Tune the boost/threshold in `wake_keywords.txt` and the int8/fp32/`-mobile` choice until the spec gates pass: ≤ 80 MB, ≤ 3 % (quiet), ≤ 10 % (speech), ≥ 9/10 hits, ≤ 1 false wake/h.
- [ ] **Step 6:** Write the "Measurements" section into the spec (chosen model files, keyword line, numbers). **If any gate still fails, stop and bring the numbers to the user** with approach 2 as the proposal. Do not continue the plan.
- [ ] **Step 7:** Commit `docs(voice): Phase 3 wake-word spike, pinned assets and measurements`.

### Task 1: Extraction per asset, with tar.bz2

**Files:**
- Modify: `internal/voice/extract.go`, `internal/voice/manager.go` (call site), `internal/voice/extract_test.go`

**Interfaces:**
- Produces: `func Extract(archive, kind, dir string, keep func(path string) bool) error`, where `kind` is `"zip" | "tar.gz" | "tar.bz2"` and `keep` gets the entry's slash path inside the archive (for model files in sub-folders) and decides whether to keep it. Kept files are still written flat into `dir` by base name. `func WhisperFile(path string) bool` is the old `engineFile` applied to the base name. The existing whisper call passes `WhisperFile`.

- [ ] **Step 1: Write the failing tests:** `TestExtractTarBz2` (build the archive in-test by writing a tar and compressing it with the `bzip2` command if available, else commit a 1 KB fixture `testdata/tiny.tar.bz2` containing `x/bin/kws-mic`, `x/lib/libonnxruntime.so.1`, `x/README`). With a `keep` that accepts `bin/` and `lib/` entries, `dir` holds exactly `kws-mic` and `libonnxruntime.so.1`. Also `TestExtractKeepFilterReceivesPath`: `keep` sees `x/lib/libonnxruntime.so.1`. Existing zip/tar.gz tests pass `WhisperFile`.
- [ ] **Step 2:** `go test ./internal/voice/ -run TestExtract` → FAIL (signature).
- [ ] **Step 3:** Implement, using `compress/bzip2` for `tar.bz2` and reusing the tar path (symlinks become copies, executables `0o755`).
- [ ] **Step 4:** `go test ./internal/voice/` → PASS.
- [ ] **Step 5:** Commit `refactor(voice): per-asset extraction filter and tar.bz2`.

### Task 2: Segment transcription with a prompt and the hallucination filter

**Files:**
- Modify: `internal/voice/engine.go`, `internal/voice/cli.go`, `internal/voice/manager.go`, `internal/voice/text.go`, `internal/app/voice.go`, tests in `engine_test.go`, `manager_test.go`, `text_test.go`; regenerated `frontend/bindings/**`

**Interfaces:**
- Produces:
  - `Engine.Transcribe(ctx context.Context, wav []byte, prompt string) (string, error)`. `ServerEngine` adds the multipart field `prompt` when non-empty; `CLIEngine` adds `--prompt <prompt>` when non-empty. The existing `Manager.Transcribe` passes `""`.
  - `func DropHallucination(text string, speechMs int) string`: returns `""` when `speechMs < 1500` and the text, lowercased and without `.,!?` and surrounding spaces, is one of the five phrases in Global Constraints. Otherwise returns `text` unchanged.
  - `func (m *Manager) TranscribeSegment(ctx context.Context, pcmB64, prompt string, speechMs int) (string, error)`: like `Transcribe`, plus the prompt, then `DropHallucination(CleanTranscript(raw), speechMs)`. Too-short PCM is `ErrTooShort`, as today.
  - Binding: `VoiceService.TranscribeSegment(ctx, pcm, prompt string, speechMs int) (string, error)`.

- [ ] **Step 1: Write the failing tests:**
  - `TestServerEngineSendsPrompt`: the fake whisper echoes the `prompt` field back as `text` when `FAKE_ECHO_PROMPT=1`. `Transcribe(wav, "new task")` → `"new task"`. An empty prompt sends no `prompt` part (the fake replies `" hello world"`).
  - `TestCLIEngineSendsPrompt`: the fake cli prints its `--prompt` value.
  - `TestDropHallucination` table: `("Thank you.", 900) → ""`, `("you", 400) → ""`, `("Thank you.", 2000) → "Thank you."`, `("thank you Sam", 900) → "thank you Sam"`, `("Bye!", 1000) → ""`.
  - `TestTranscribeSegmentFilters`: the stub engine returns `" Thank you."` and speechMs 800 → `""`. The stub records the prompt it received.
- [ ] **Step 2:** `go test ./internal/voice/` → FAIL.
- [ ] **Step 3:** Implement. Update the fake whisper in `TestMain`.
- [ ] **Step 4:** `go test ./internal/voice/` → PASS; then `wails3 generate bindings -clean=true -ts` → `voiceservice.ts` has `TranscribeSegment`; `go vet ./...` clean.
- [ ] **Step 5:** Commit `feat(voice): segment transcription with prompt and silence-hallucination filter`.

### Task 3: Wake assets, install and the per-device switch

**Files:**
- Create: `internal/voice/wake_assets.go`, `internal/voice/wake.go`, `internal/voice/wake_test.go`

**Interfaces:**
- Consumes: `Asset`, `Download`, `Extract` (Task 1), `ErrChecksum`, the Task 0 table and `wake_keywords.txt`.
- Produces:
  - `const WakeRelease = "<Task 0 release>"`; `func WakeEngineAsset(goos, goarch string) (Asset, bool)`; `var WakeModelAsset Asset`; `func wakeEngineFile(path string) bool` and `func wakeModelFile(path string) bool` (the files named in Task 0); `//go:embed wake_keywords.txt` → `var wakeKeywords []byte`.
  - The status type:
    ```go
    type WakeStatus struct {
        State    string `json:"state"` // "off" | "absent" | "installing" | "starting" | "listening" | "error" | "unsupported"
        Enabled  bool   `json:"enabled"`
        Chime    bool   `json:"chime"`
        Phase    string `json:"phase"` // "engine" | "model" while installing
        Done     int64  `json:"done"`
        Total    int64  `json:"total"`
        Download int64  `json:"download"` // bytes a consent click will fetch
        Error    string `json:"error"`
        Hint     string `json:"hint"`
    }
    ```
  - `type WakeConfig struct { Dir, GOOS, GOARCH string; Client *http.Client; RuntimeMissing func() []string; OnStatus func(WakeStatus); OnWake func(); Suppress func() bool; Engine, Model *Asset /* test overrides */; Bin func(engineDir string) string /* test override for the helper path */; Backoff []time.Duration; Tick time.Duration; Now func() time.Time }`. Defaults: `Backoff {1s, 5s, 30s}`, `Tick 1s`, `Now time.Now`.
  - `func NewWake(c WakeConfig) *Wake`; `Status() WakeStatus`; `SetEnabled(on bool) error` (returns `ErrWakeNotInstalled` when turning on while files are absent; on success persists to `wake.json`); `SetChime(on bool) error`; `Install() error` (async like `Manager.Install`; on success sets enabled, persists, starts); `CancelInstall()`; `SessionEnded()`; `Close()`.
  - `var ErrWakeNotInstalled = errors.New("voice: wake word files are not installed")`.
  - Files: `wake.json` in `Dir`, written atomically (temp + rename). The keywords file is written next to the model at install time.
  - Unsupported: no asset for the platform → `Hint: "The wake word isn't available on this system yet."`. Windows runtime missing → the Phase 1.5 VC++ hint.

- [ ] **Step 1: Write the failing tests** (httptest serving small fake archives; `Engine`/`Model` overrides with their real hashes):
  - `TestWakeDefaultsOff`: empty dir → `State "absent"`, `Enabled false`, `Chime true`, `Download == engine.Size + model.Size`.
  - `TestWakeConfigCorruptFallsBack`: `wake.json` = `{` → defaults, no error.
  - `TestSetEnabledNeedsInstall`: `SetEnabled(true)` → `ErrWakeNotInstalled`, still off.
  - `TestWakeInstallThenEnabled`: `Install` → eventually `Enabled true` and the state is `starting` or `listening` (fake helper from Task 4's `TestMain`, or `Bin` pointing at a stub). `wake.json` says enabled. `.ok` and `wake_keywords.txt` exist.
  - `TestWakeInstallChecksumFailure`: wrong model bytes → `absent` with `Error` containing "checksum", no model files left.
  - `TestWakeUnsupportedPlatform`: `GOOS "plan9"` → `unsupported` with the hint above.
  - `TestSetChimePersists`: `SetChime(false)`, then a new `Wake` on the same dir → `Chime false`.
- [ ] **Step 2:** `go test ./internal/voice/ -run 'TestWake|TestSetEnabled|TestSetChime'` → FAIL.
- [ ] **Step 3:** Implement (install layout as in Global Constraints; `.ok` holds `WakeRelease`).
- [ ] **Step 4:** → PASS.
- [ ] **Step 5:** Commit `feat(voice): wake-word assets, consented install and per-device switch`.

### Task 4: Wake helper supervision

**Files:**
- Create: `internal/voice/wake_proc.go`
- Modify: `internal/voice/wake.go`, `internal/voice/wake_test.go`, `internal/voice/engine_test.go` (`TestMain` fake modes)

**Interfaces:**
- Consumes: `hideAndContain`, `afterStart`, `WakeConfig` (Task 3), the Task 0 argument list and stdout format.
- Produces:
  - `func helperCommand(goos, exe, engineDir, modelDir, keywords string) (bin string, args []string)`: on linux/darwin, `bin` is the stock spotter in `engineDir` and `args` is the exact Task 0 list with `--num-threads=1` and `--provider=cpu`. On windows, `bin` is `exe` (the app's own `os.Executable()`) and `args` is `--wake-helper --lib-dir <engineDir>` followed by the same model, keyword and thread flags (Task 4b).
  - `func scanHelperOutput(r io.Reader, onDetect func(keyword string), onLine func(line string))`. Task 0 found that the stock program writes detections to **stderr** as `<n>:{json}`, and on Linux/macOS with no trailing newline (`0:{json}`, then `
1:{json}`). So this function scans the byte stream, not lines. Each `{"start_time": … }` object (single line, no nested braces) is parsed with `encoding/json`, and `onDetect` gets its `keyword`. Every other complete line (split on `
` or ``, empty lines dropped) goes to `onLine`. It runs on stderr (detections and the last error line) and on stdout (stdout lines are logged only).
  - `func parseDetection(s string) bool`: true when `s` contains a detection object whose keyword is `HEY_AWESOME`. Used by Task 4b's test.
  - Behaviour: the helper runs iff `Enabled && installed`. Each detection calls `OnWake` unless `Suppress()` is true, or the last wake was < 3 s ago, or `SessionEnded` was < 2 s ago. Restart and give-up follow Global Constraints. The `error` state carries the helper's last non-empty stderr line. A wall jump > 5 s between `Tick`s restarts the helper. `SetEnabled(false)` and `Close()` kill it and wait for it to exit.
  - State: `starting` from spawn until 2 s alive (the stock program prints no ready line), then `listening`.
- Fake helper: `FAKE_KWS=detect` writes the stock start-up lines to stderr, then for every line that arrives on its stdin (tests write to it) writes a detection to stderr in the Linux form (`0:{json}` with no trailing newline, then `
1:{json}`, …), using the JSON from the Task 0 notes; `FAKE_KWS=die` prints `$FAKE_STDERR` to stderr and exits 1 at once; `FAKE_KWS=idle` prints the start line and blocks.

- [ ] **Step 1: Write the failing tests** (`Backoff: {10ms, 20ms, 40ms}`, `Tick: 20ms`, injectable `Now`):
  - `TestScanHelperOutput`: the Linux form without a trailing newline → `onDetect("HEY_AWESOME")` fires before EOF (use an `io.Pipe` and check before closing); the Windows form `0:{json}
` → fires; start-up lines and `Invalid device index: 3` → `onLine` only; a JSON object for another keyword → no `HEY_AWESOME` detection.
  - `TestWakeReportsDetection`: enabled + `detect` → one stdin line → `OnWake` called once; state `listening`.
  - `TestWakeSuppressed`: `Suppress` returns true → no `OnWake`.
  - `TestWakeCooldowns`: two detections 1 s apart (fake clock) → one `OnWake`; a detection 1 s after `SessionEnded()` → none; one 4 s after → called.
  - `TestWakeRestartsWithBackoff`: kill the helper → a new PID after ~10 ms; state goes back to `listening`.
  - `TestWakeGivesUpAfterThreeExits`: `die` with `FAKE_STDERR="PortAudio error: Invalid device"` → after 3 exits `State "error"`, `Error` contains `Invalid device`, no fourth spawn within 200 ms (Review Focus 1).
  - `TestWakeOffKillsHelper`: `SetEnabled(false)` → the PID is gone when it returns; state `off`.
  - `TestWakeRestartsAfterClockJump`: advance the fake wall clock by 10 s between ticks → a new PID.
  - `TestWakeCloseKillsHelper`: `Close()` → no process left.
- [ ] **Step 2:** `go test ./internal/voice/ -run 'TestParseDetection|TestWake'` → FAIL.
- [ ] **Step 3:** Implement. One supervisor goroutine per enabled period, owning the `exec.Cmd`. Stdout and stderr are read by line; stderr keeps the last line (cap 512 bytes).
- [ ] **Step 4:** `go test -race ./internal/voice/` → PASS.
- [ ] **Step 5:** Commit `feat(voice): supervise the wake-word helper with backoff, cooldowns and resume restart`.

### Task 4b: Windows wake helper (our own, because the release has no Windows mic spotter)

**Files:**
- Create: `internal/wakehelper/helper.go` (flag parsing, the loop, output lines), `internal/wakehelper/gate.go` (pure RMS gate), `internal/wakehelper/gate_test.go`, `internal/wakehelper/kws_windows.go` (sherpa-onnx C API through `golang.org/x/sys/windows`), `internal/wakehelper/mic_windows.go` (winmm waveIn), `internal/wakehelper/helper_windows_test.go`, `internal/wakehelper/helper_other.go` (`//go:build !windows`: `Run` prints `wake helper: not supported on this OS` to stderr and returns 2), `internal/wakehelper/testdata/hey-awesome.wav` and `testdata/negative.wav` (16 kHz mono s16 TTS clips from Task 0, each under 200 KB)
- Modify: `main.go` (the first statement in `main`), `internal/voice/wake_proc.go` (`helperCommand` windows branch, if Task 4 left it as a stub)

**Interfaces:**
- Consumes: the Task 0 notes (C API DLL name and declarations, the stock stdout format, the keyword file), `voice.parseDetection` (Task 4).
- Produces:
  - `main.go`: `if len(os.Args) > 1 && os.Args[1] == "--wake-helper" { os.Exit(wakehelper.Run(os.Args[2:])) }` before any Wails, single-instance or logging setup. Helper mode never touches the app's lock, database or windows.
  - `func Run(args []string) int`. Flags: `--lib-dir`, `--encoder`, `--decoder`, `--joiner`, `--tokens`, `--keywords-file`, `--num-threads` (default 1), and test-only `--wav <file>`, which feeds a WAV file at full speed instead of the mic and exits 0 at its end.
    - Loads the C API DLL from `--lib-dir`, so `onnxruntime.dll` resolves from the same folder (`windows.LoadLibraryEx` with `LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_DEFAULT_DIRS`).
    - Prints each detection to stderr as `<n>:<json>
` (n counts from 0, json is the C API result's `json` field) and flushes, which is the stock program's MSVC form.
    - A mic open failure prints `wake helper: microphone: <winmm error text>` to stderr and exits 3. A model load failure prints `wake helper: model: <detail>` and exits 4.
    - Exits 0 when stdin reaches EOF, so it dies if the app's pipe closes. The job object already covers a crash.
  - `type Gate struct`; `func NewGate(rate int) *Gate`; `func (g *Gate) Push(block []int16) (feed []int16, reset bool)`. Uses the same speech test as `voice.ts`: RMS of 30 ms windows normalised to [0,1], noise floor `rms < floor ? rms : floor*0.995 + rms*0.005` starting at 0.003, speech when RMS > `max(0.015, floor*3)`.
    - While quiet it feeds nothing and keeps the last 500 ms in a ring.
    - On speech it returns the pre-roll plus the block, and keeps feeding until 1.5 s without speech.
    - It then returns `reset == true` once, and the caller resets the keyword stream.
  - Mic: `waveInOpen(WAVE_MAPPER, {WAVE_FORMAT_PCM, 1 ch, 16000 Hz, 16-bit}, CALLBACK_EVENT)`, 4 buffers of 100 ms, re-queued as they complete. `waveInReset` and `waveInClose` on exit.
  - C API structs are mirrored with the exact field order and C types from the Task 0 header (`uintptr` for `const char*`, `int32`, `float32`). Strings are kept alive for the spotter's lifetime.
- Pinned Windows asset: `sherpa-onnx-v2.0.3-win-x64-shared-MT-Release-no-tts-lib.tar.bz2` (Task 0 notes). `wakeEngineFile` keeps `sherpa-onnx-c-api.dll`, `onnxruntime.dll` and `onnxruntime_providers_shared.dll` on windows. They import only system DLLs, so the wake word needs no VC++ runtime check on Windows (dictation still does).

- [ ] **Step 1: Write the failing tests:**
  - `TestGateQuietFeedsNothing`: 2 s of ±50 noise → never feeds, never resets.
  - `TestGateOpensWithPreRoll`: 0.6 s quiet, then a loud 30 ms block → the first feed has 500 ms (8000 samples) of pre-roll plus the block.
  - `TestGateClosesAfterQuietAndResets`: loud, then 1.5 s quiet → `reset` true exactly once, then no feed.
  - `TestHelperDetectsFromWav` (windows; skipped unless `AR_KWS_DIR` points at an installed engine and model): `Run` with `--wav testdata/hey-awesome.wav` → stderr has one line for which `voice.parseDetection` is true. With `negative.wav` → no detection line.
  - `TestHelperMicFailureExitCode` cannot be automated without breaking the mic. Instead, test the error-text function: `micErrorText(MMSYSERR_BADDEVICEID)` contains "device".
- [ ] **Step 2:** `go test ./internal/wakehelper/` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `go test ./internal/wakehelper/ ./internal/voice/` → PASS; with `AR_KWS_DIR` set to the Task 0 extraction → `TestHelperDetectsFromWav` PASS. `go vet ./...` clean; `GOOS=linux go vet ./internal/wakehelper/` clean. Manual measurement (sherpa only, the app is not started): run the helper through a `go test -run TestManualMic` that is skipped unless `AR_KWS_MIC=1` and runs the mic loop for 300 s while sampling its own process CPU and working set → record quiet-room CPU and RAM in the spec's Measurements section.
- [ ] **Step 5:** Commit `feat(voice): Windows wake helper on the sherpa-onnx C API with an RMS gate`.

### Task 5: App wiring — services, events, show without focus

**Files:**
- Create: `internal/app/wake.go`, `internal/app/noactivate_darwin.go`, `internal/app/noactivate_linux.go`
- Modify: `internal/app/voice.go`, `internal/app/app.go` (only `registerVoice(app)` → `registerVoice(app, win)`), `internal/app/windows.go`, `internal/app/noactivate_other.go` (build tag `!windows && !darwin && !linux`), `internal/app/windows_test.go`, `build/darwin/Info.plist`, regenerated `frontend/bindings/**`, `frontend/src/api.ts`

**Interfaces:**
- Consumes: `voice.Wake` (Tasks 3–4), `showNoActivate`.
- Produces:
  - Events, registered in `wake.go`'s `init()`: `EventWakeStatus = "voice:wake-status"` (`voice.WakeStatus`), `EventCaptureWake = "capture:wake"` (`application.Void`).
  - `VoiceService` methods: `WakeStatus() voice.WakeStatus`, `SetWakeEnabled(on bool) error`, `SetWakeChime(on bool) error`, `InstallWake() error`, `CancelWakeInstall()`, `SessionEnded()`. `ServiceShutdown` also closes `Wake`.
  - `func (w *wailsWindows) ShowCaptureForWake()`: positions like `ShowCapture` (screen under the cursor, centred), shows through `showNoActivate`, never calls `Focus`, then emits `EventCaptureWake`. `func (w *wailsWindows) CaptureVisible() bool`, backed by an `atomic.Bool` set in `ShowCapture`, `ShowCaptureForWake` and `HideCapture`.
  - `registerVoice(app, win)` builds the `Wake` with `OnWake: func() { go win.ShowCaptureForWake() }`, `Suppress: win.CaptureVisible`, `OnStatus` → `EventWakeStatus`, and the same `RuntimeMissing` as the `Manager`. Log `wake: detected` in `OnWake`.
  - `noactivate_darwin.go` (cgo): `showNoActivate` makes the window visible with `orderFrontRegardless` and without activating the app. Check that Wails' own visibility state stays consistent (a later `Hide` and `Show` work). If it can't be done cleanly, call `win.Show()` and leave a comment saying the page's keyboard guard (Task 10) covers it.
  - `noactivate_linux.go` (cgo): `gtk_window_set_accept_focus(FALSE)` on the native window, `win.Show()`, then accept-focus is restored on the next `ShowCapture` (hotkey) or on the window's focus event after a click.
  - `api.ts`: `api.voice.transcribeSegment(pcm: Int16Array, prompt: string, speechMs: number)`, `wakeStatus`, `setWakeEnabled`, `setWakeChime`, `installWake`, `cancelWakeInstall`, `sessionEnded`; `events.wakeStatus = "voice:wake-status"`, `events.captureWake = "capture:wake"`; `type WakeStatus` re-exported from the bindings.
  - Info.plist: the `NSMicrophoneUsageDescription` text from Global Constraints.

- [ ] **Step 1: Write the failing test:** `TestCaptureVisibleTracksShowHide` in `windows_test.go` (using the existing fake-window style there, if present; otherwise test the `atomic.Bool` helper directly): show → true, hide → false, wake-show → true.
- [ ] **Step 2:** `go test ./internal/app/` → FAIL.
- [ ] **Step 3:** Implement. Regenerate bindings.
- [ ] **Step 4:** `go test ./...`, `go vet ./...`, `npm --prefix frontend run typecheck` → PASS. The darwin/linux cgo files don't compile on Windows; Task 13's macOS/Linux pass builds and checks them.
- [ ] **Step 5:** Commit `feat(app): wake word opens the capture box without focus`.

### Task 6: Spoken command parser

**Files:**
- Create: `frontend/src/voice-commands.ts`, `frontend/src/voice-commands.test.ts`

**Interfaces:**
- Produces:
  ```ts
  export type Op =
    | { kind: "text"; text: string }
    | { kind: "boundary" }
    | { kind: "scratch" }
    | { kind: "save" }
    | { kind: "cancel" };
  export function parseSegment(text: string, opts?: { multi?: boolean }): Op[]; // multi defaults to true
  ```
- Phrases (spec §4): boundary `new task`, `create new task`, `create a new task`, `next task`, `add another task` (anywhere); scratch `scratch that`, `delete that`; save `save all`, `save them`, `save them all`, `save tasks`, `that's all`, `that's it`; cancel `cancel`, `cancel all`, `never mind`. Scratch, save and cancel count only as the whole segment or its last sentence (split on `.`, `?`, `!`). Matching is case-insensitive, ignores `.,!?;:"'` and repeated spaces, and uses whole words. Text pieces are trimmed of punctuation and of a leading or trailing `and` / `okay` next to a boundary. Empty text pieces are dropped.
- `multi: false` (reply mode): boundary phrases stay text; save and cancel are not commands (stay text); scratch still works.

- [ ] **Step 1: Write the failing tests** (`describe("parseSegment")`, table-driven):

| input | ops |
|---|---|
| `"Call Sam tomorrow at 10."` | text "Call Sam tomorrow at 10" |
| `"Call Sam. New task. Buy milk"` | text "Call Sam", boundary, text "Buy milk" |
| `"call Sam and create a new task okay buy milk next task"` | text "call Sam", boundary, text "buy milk", boundary |
| `"New task."` | boundary |
| `"Save all."` | save |
| `"Buy milk. That's all."` | text "Buy milk", save |
| `"remind me to save all the logs"` | text "remind me to save all the logs" |
| `"Cancel the dentist appointment."` | text "Cancel the dentist appointment" |
| `"Scratch that."` | scratch |
| `"Never mind"` | cancel |
| `"new task template for sprint"` | boundary, text "template for sprint" (accepted cost) |
| `"Renew task list"` | text "Renew task list" (whole words) |
| multi:false `"Thanks, new task. Save all."` | text "Thanks, new task. Save all" |

- [ ] **Step 2:** `npm --prefix frontend test -- voice-commands` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** → PASS.
- [ ] **Step 5:** Commit `feat(ui): spoken command parser for multi-task dictation`.

### Task 7: Segmenting recorder and session rules

**Files:**
- Create: `frontend/src/voice-segment.ts`, `frontend/src/voice-segment.test.ts`
- Modify: `frontend/src/voice.ts` (`Recorder`)

**Interfaces:**
- Produces:
  - `export class Segmenter { constructor(rate: number); push(block: Float32Array, speech: boolean): Segment | null; flush(): Segment | null }` with `type Segment = { pcm: Float32Array; speechMs: number }`. Constants from Global Constraints (`PRE_ROLL_MS = 300`, `CUT_SILENCE_MS = 700`, `MAX_SEGMENT_MS = 20000`, `FORCE_CUT_WINDOW_MS = 2000`, `QUIET_WINDOW_MS = 100`, `MIN_SPEECH_MS = 250`). On a force cut, audio after the cut point carries over into the next segment.
  - `export type SessionMode = "focused" | "wake"`; `export type EndReason = "silence" | "nothing" | "limit" | "quiet-wake"`; `export function endReason(t: { mode: SessionMode; elapsedMs: number; heardSpeech: boolean; sinceSpeechMs: number; afterBoundary: boolean }): EndReason | null`. Rules: focused `silence` at ≥ 2000 ms since speech (≥ 8000 when `afterBoundary`), `nothing` at ≥ 8000 ms with no speech; wake `silence` at ≥ 15000, `quiet-wake` at ≥ 6000 with no speech; `limit` at ≥ 300000 in both.
  - `export type PcmSegment = { pcm: Int16Array /* 16 kHz mono */; speechMs: number }`. The `Segmenter` works on Float32 audio at the recorder's rate; the `Recorder` converts each `Segment` with the existing `resampleTo16k` + `toInt16` before handing it on.
  - `export class SegmentQueue { constructor(transcribe: (seg: PcmSegment, prompt: string) => Promise<string>, apply: (text: string) => void, fail: (err: unknown) => void, prompt: () => string); push(seg: PcmSegment): void; idle(): Promise<void>; cancel(): void }`. One request at a time; results applied in push order; a failure calls `fail` and moves on; `cancel` drops pending items and ignores late results.
  - `Recorder.start(onLevel, onAutoStop, opts?: { onSegment?: (seg: PcmSegment) => void })`: with `onSegment`, the recorder feeds a `Segmenter` from the existing speech test, emits segments and does **not** auto-stop on silence (the session decides). `stopSegments(): PcmSegment | null` flushes and releases. `lastSpeechAt()` and `heardSpeech` are exposed for `endReason`. Without `onSegment`, the Phase 1.5 behaviour is unchanged.

- [ ] **Step 1: Write the failing tests** (16 kHz, 128-sample blocks, synthetic speech flags):
  - `cuts after 700 ms of silence and keeps 300 ms pre-roll`: 1 s silence, 1 s speech, 1 s silence → one segment, length ≈ 300 ms + 1 s + ≤ 700 ms of samples (±1 block), `speechMs ≈ 1000`.
  - `drops segments under 250 ms of speech`.
  - `force-cuts at 20 s at the quietest window`: 22 s of speech with a low-amplitude 100 ms window at 19.0–19.1 s → first segment ends within that window; the rest carries over.
  - `endReason` table: one row per rule plus the boundaries just under each threshold.
  - `segment queue keeps order and survives a failed segment`: three segments; the second's transcribe rejects; the first resolves after the third → `apply` gets texts 1 then 3, `fail` once (Review Focus 2).
  - `cancel ignores late results`.
- [ ] **Step 2:** `npm --prefix frontend test -- voice-segment` → FAIL.
- [ ] **Step 3:** Implement `voice-segment.ts`, then the `Recorder` option.
- [ ] **Step 4:** `npm --prefix frontend test` and `npm --prefix frontend run typecheck` → PASS.
- [ ] **Step 5:** Commit `feat(ui): segmenting recorder, ordered transcription queue and session end rules`.

### Task 8: Review list and saving several tasks

**Files:**
- Create: `frontend/src/review-list.ts`, `frontend/src/review-list.test.ts`
- Modify: `frontend/src/capture.ts`, `frontend/src/style.css`, `frontend/src/tokens.css`, `frontend/src/icons.ts` (`x`, `undo-2` if missing)

**Interfaces:**
- Consumes: `api.capture`, `api.preview`, `api.deleteItem`, `api.undelete`.
- Produces:
  - `export async function saveSequential(texts: string[], capture: (t: string) => Promise<Item>): Promise<{ saved: Item[]; failedAt: number | null; error: string }>`: saves in order and stops at the first failure.
  - `export class ReviewList { constructor(container: HTMLElement, onChange: () => void); add(text: string): void; removeAt(i: number): void; popLast(): string | undefined; takeAt(i: number): string; items(): string[]; restore(items: string[]): void; clear(): void; setError(i: number, msg: string): void; focusLast(): void }`. Each row renders the number, kind icon, text, compact preview (from `api.preview` on add) and a × button with `aria-label="Remove task N"`. Keys inside the list: `Up`/`Down`, `Delete`, `Enter` (calls `onEdit(i)`, passed as a constructor option `onEdit?: (i: number) => void`), `Esc` (back to the input).
  - `capture.ts`: the list container `#cap-review` sits above the input row. Enter saves `[...list.items(), input]` (input only if non-empty) with `saveSequential`. Saved rows leave the list, and the failing row gets `setError`. Enter with rows and an empty input is allowed. `Up` from the input calls `list.focusLast()`. Row edit: the row's text goes into the input, and any input text takes the row's place. The hint row shows `↵ save N` when N > 1. Esc with rows (not listening): first press shows the confirm copy for 3 s, a second Esc within 3 s clears and hides. The draft (rows plus input) survives hide/show and is cleared only by a save, a confirmed Esc or a spoken cancel. When the box opens with rows left over from a voice session, it shows "N unsaved tasks from voice" in the hint row. `#cap-note` shows "Saved N tasks" / "Discarded N tasks" with an **Undo** button (and `Ctrl+Z` when focused) for 5 s. Saved-Undo calls `api.deleteItem` for each saved id and restores the texts to the list. Discarded-Undo restores the list and input.
  - The box grows through the existing `ResizeObserver`. At most 6 rows are visible (`--review-max-rows: 6` in `tokens.css`), the rest scroll.

- [ ] **Step 1: Write the failing tests** (`review-list.test.ts`, jsdom, fake `capture`):
  - `saveSequential saves in order`: 3 texts → 3 calls in order, `failedAt null`.
  - `saveSequential stops at the first failure`: the 2nd rejects with "db locked" → `saved.length 1`, `failedAt 1`, `error "db locked"`, the 3rd is never called (Review Focus 5).
  - `ReviewList add/removeAt/popLast/takeAt/restore` keep `items()` correct and render one row each with the right `aria-label`.
- [ ] **Step 2:** `npm --prefix frontend test -- review-list` → FAIL.
- [ ] **Step 3:** Implement `review-list.ts`, then integrate into `capture.ts`. Rows only come from voice (Task 9), so the end-to-end checks of this task are in Task 9's manual step.
- [ ] **Step 4:** `npm --prefix frontend test`, `npm --prefix frontend run typecheck` → PASS. With no rows, the capture box behaves exactly as before (type, Enter saves one item, Esc hides).
- [ ] **Step 5:** Commit `feat(ui): review list in the capture box, save all, confirm discard`.

### Task 9: Multi-task dictation session

**Files:**
- Modify: `frontend/src/voice.ts` (`mountVoice`, `VoiceHost`, `VoiceControl`), `frontend/src/capture.ts`, `frontend/src/style.css`, `frontend/src/tokens.css`, `docs/superpowers/specs/2026-11-27-ui-design-guide.md` ("Listening state")

**Interfaces:**
- Consumes: `parseSegment` (Task 6), `Segmenter` / `SegmentQueue` / `endReason` (Task 7), `ReviewList` and the save/undo functions (Task 8), `api.voice.transcribeSegment` (Task 5).
- Produces:
  - `VoiceHost` gains: `addTask(): void` (boundary), `scratch(): void`, `saveAll(spoken: boolean): Promise<void>` (Task 8's Enter path; `spoken` adds the Undo note), `cancelAll(spoken: boolean): void`, `multi(): boolean` (false in reply mode), `sessionText(): string` (the last ~200 characters of rows plus input, for the prompt).
  - `VoiceControl` gains: `startSession(mode: SessionMode): void` (Ctrl+M and the mic call it with `"focused"`), `focusGained(): void` (switches a wake session to focused rules), `finish(): Promise<void>` (stop, wait for the queue: used by Enter while listening).
  - Behaviour per spec §3–4: segments → queue → `parseSegment(text, { multi: host.multi() })` → ops applied in order (text → `host.insert`; boundary → `addTask` and set `afterBoundary`; scratch; save → `finish()` then `saveAll(true)`; cancel → `cancelAll(true)`). The prompt is `host.sessionText()` + ` new task`. A segment failure shows "Couldn't transcribe the last bit: …" and the session continues. While the queue is busy, a small shimmer (the existing `cap-dots`) sits at the end of the input. While listening, the input stays visible and editable; the listening indicators (pulse dot, label, waveform, timer) replace the hint row; the mic stays the stop button. `role="status"` announces "Task N added", "Removed". Esc while listening stops, drops only the audio not yet in a segment, and lets the queue finish. Blur releases the mic and drops only the unsegmented audio, and the queue finishes into the draft. At the 5 min limit the session stops with "Stopped after 5 minutes". Each session end calls `api.voice.sessionEnded()`.
  - Single task: say one line and pause → same result as Phase 1.5 ("Ready to save").

- [ ] **Step 1:** Implement. The pure parts are already tested. This task wires them together.
- [ ] **Step 2:** `npm --prefix frontend test`, `npm --prefix frontend run typecheck`, `npm --prefix frontend run build` → PASS.
- [ ] **Step 3: Manual in the built app** (temp copy, temp `APPDATA`/`LOCALAPPDATA` as in Phase 1.5 Task 8, voice installed):
  - Ctrl+M: "buy milk" + pause → the input has it, nothing saved.
  - Ctrl+M: "call Sam tomorrow at 10, new task, buy milk, next task, refactor auth hashtag backend" → 2 rows + input. Enter → 3 items in the list.
  - "… new task … scratch that" removes the current task. "save all" saves and shows Undo, and Undo removes them again.
  - Esc while listening keeps the text. Blur keeps the draft.
  - Reply mode (replies-owed): Ctrl+M with "thanks new task save all" stays one line of text (Review Focus 3).
- [ ] **Step 4:** Update the design guide's "Listening state" sketch and bullets to the new layout (review rows, input visible, indicators in the hint row).
- [ ] **Step 5:** Commit `feat(ui): continuous multi-task dictation with spoken commands`.

### Task 10: Wake mode in the capture box

**Files:**
- Create: `frontend/src/key-guard.ts`, `frontend/src/key-guard.test.ts`
- Modify: `frontend/src/capture.ts`, `frontend/src/voice.ts` (chime), possibly `internal/app/app.go` (one autoplay option line, see Step 4), `internal/app/windows.go` (macOS/Linux autoplay window option, only if needed)

**Interfaces:**
- Produces:
  - `export class KeyGuard { arm(): void; release(): void; armed(): boolean; allows(e: KeyboardEvent): boolean }`. While armed, `allows` is false for printable keys, Enter, Backspace, Delete and Tab, and true for Esc and modifier-only keys. The hotkey, a click or Ctrl+M call `release()`.
  - `export function playChime(ctx?: AudioContext): Promise<void>`: two sine notes (660 Hz then 880 Hz, 70 ms each, 10 ms fades, gain 0.15). Resolves when done. Calls `ctx.resume()` first and rejects if `ctx.state` is not `"running"` after it.
  - `capture.ts`: on `events.captureWake`: `open()` without focusing the input; `keyGuard.arm()`; if `WakeStatus.chime`, `await playChime()` (on rejection, log it and continue); then `voice.startSession("wake")`; the listening hint reads "Listening — say your task" and, while armed, "Click or press <hotkey> to type" (hotkey text from `api.getSettings()`). A mousedown in the box, the `capture:open` event (hotkey) or Ctrl+M → `keyGuard.release()` and `voice.focusGained()`. A `quiet-wake` end hides the box with no error. A wake session that ends with rows or text hides and keeps the draft.
- [ ] **Step 1: Write the failing tests** (`key-guard.test.ts`): armed blocks `a`, `Enter`, `Backspace`, `Tab`; allows `Escape`, `Shift`; after `release()` allows everything.
- [ ] **Step 2:** `npm --prefix frontend test -- key-guard` → FAIL.
- [ ] **Step 3:** Implement and wire it. The document-level keydown handler checks `keyGuard.allows(e)` first and calls `preventDefault` on blocked keys.
- [ ] **Step 4: Autoplay check** (spec "Audio without a user gesture"): quit the app, start a fresh copy, do not touch the capture box, enable the wake word, say "Hey Awesome". Expected: the chime plays and the waveform moves.
  - If it fails on Windows, add `--autoplay-policy=no-user-gesture-required` to the WebView2 browser arguments in `app.go`'s `application.WindowsOptions` (one line) and retest.
  - If it fails on macOS or Linux, apply the per-window or WebKit setting in `windows.go`.
  - Record the outcome in the spec's Measurements section.
- [ ] **Step 5: Manual:**
  - With Notepad focused, typing, say "Hey Awesome": the box appears and the typing continues in Notepad. Say nothing → the box hides after ~6 s with no error (Review Focus 4 on Windows).
  - "Hey Awesome" → "buy milk, new task, call Sam, save all" → 2 items saved, Undo note shown.
  - "Hey Awesome" → "buy milk" → silence → the box hides after ~15 s. The next hotkey shows "1 unsaved task from voice" with the text in the input.
  - A wake while the box is open is ignored.
  - Measure wake word → waveform moving: ≤ 500 ms (screen recording or a log timestamp from `wake: detected` to the page's first level) → record it in Measurements.
- [ ] **Step 6:** Commit `feat(ui): hands-free wake mode with chime and keyboard guard`.

### Task 11: Settings — Voice section

**Files:**
- Modify: `frontend/src/settings.ts`, `frontend/src/style.css`, `docs/superpowers/specs/2026-11-27-ui-design-guide.md` ("Settings")

**Interfaces:**
- Consumes: `api.voice.status`, `install`, `wakeStatus`, `setWakeEnabled`, `setWakeChime`, `installWake`, `cancelWakeInstall`, `events.voiceStatus`, `events.wakeStatus`.
- Produces: a "Voice" section after the existing fields. It is outside the form's Save flow and says "Voice settings apply right away." Contents:
  - The **Listen for "Hey Awesome"** switch with the spec §6 help text, where N is the measured MB from the spec's Measurements section.
  - Turning it on calls `setWakeEnabled(true)`. On `ErrWakeNotInstalled`, an inline consent box appears with the total download (wake files plus, if `voice.status().state === "absent"`, the dictation files) and **Download** / **Not now**. Download runs `api.voice.install()` first when dictation is absent (waiting for `ready` through `voice:status`), then `installWake()`, with a progress bar and **Cancel** for each phase. The switch turns on when wake is installed.
  - The **Play a sound when it hears you** switch (disabled while wake is off).
  - A status line: "Listening for "Hey Awesome"" / "Starting…" / the error with **Try again** (off then on).
  - When dictation is `unsupported`, the wake switch is disabled and its hint is shown.
- [ ] **Step 1:** Implement.
- [ ] **Step 2:** `npm --prefix frontend run typecheck`, `npm --prefix frontend run build` → PASS.
- [ ] **Step 3: Manual** (fresh temp profile): Settings → switch on → consent shows both sizes → Download → progress through dictation, then wake → switch on → the Windows mic indicator appears. Switch off → the indicator disappears within 1 s and the helper process is gone (Task Manager). Turn the mic privacy setting off → after 3 restarts the status shows the error. Quit the app with wake on → no helper left.
- [ ] **Step 4:** Commit `feat(settings): Voice section with the Hey Awesome switch and consent`.

### Task 12: Tray toggle (only once Phase 2 is in `master`)

**Files:**
- Modify: `internal/app/app.go` (`trayMenu`)

**Interfaces:**
- Produces: a checkbox item "Listen for "Hey Awesome"" between Settings and the separator. It is checked from `WakeStatus.Enabled` (and updated on `voice:wake-status`). Clicking it calls `SetWakeEnabled`. On `ErrWakeNotInstalled` it opens Settings instead.
- [ ] **Step 1:** Check `git log master` for the Phase 2 merge. If it is absent, skip this task and note it in the PR.
- [ ] **Step 2:** Implement; `go vet ./...`, `go test ./...` → PASS. Manual: toggling from the tray updates Settings and the mic indicator.
- [ ] **Step 3:** Commit `feat(app): tray toggle for the wake word`.

### Task 13: Verify on all platforms and document

**Files:**
- Modify: `README.md` (Voice section: hands-free, commands table, privacy, Settings, per-OS table, measured memory/CPU, files, roadmap wording that "new task" fills a review list), `docs/manual-test-checklist.md` (Phase 3 items for Windows, macOS, Linux), `docs/superpowers/specs/2026-11-28-hands-free-voice-design.md` (Measurements complete)

- [ ] **Step 1:** `wails3 build`, `go vet ./...`, `go test -race ./...`, `npm --prefix frontend test`, `npm --prefix frontend run typecheck` → all pass.
- [ ] **Step 2:** Windows: the Task 9–11 manual steps on the built exe, plus sleep/resume with wake on (the helper restarts and the wake still works) and headset unplug/replug (documented limitation: toggle off/on).
- [ ] **Step 3:** macOS and Linux (or hand to the user with the checklist): wake works, the mic prompt names Awesome Reminder (macOS), Gatekeeper does not block the helper, the box appears without taking typing (macOS, Linux X11), the keyboard guard protects the draft on Wayland, no helper is left after quit.
- [ ] **Step 4:** Idle check: with wake off, the app's memory and CPU match today's baseline (about 137 MB, 0 %) and no helper is running.
- [ ] **Step 5:** Commit `docs: hands-free voice`.

## Shared files with replies-owed and Phase 2

Both land first, so rebase onto them and keep their behaviour:

| File | Replies-owed / Phase 2 change | This plan's change | Watch for |
|---|---|---|---|
| `frontend/src/capture.ts` | reply mode (`reply:open`, `reply-mode.ts`, lead icon `reply`, its own hint row, Enter → `captureReply`) | review list, wake mode, multi-task session, Enter saves all | In reply mode: no review list, `host.multi()` false, Enter keeps calling `captureReply`. The reply hint row wins over the listening hint row only when not listening. |
| `internal/app/windows.go` | `ShowReply` | `ShowCaptureForWake`, `CaptureVisible` | `ShowReply` must also set `captureVisible` (the reply box is the capture window), so wakes are suppressed while it is open. |
| `internal/app/app.go` | a second `hotkey.Manager`, `--reply`, tray tooltip; Phase 2 tray status | `registerVoice(app, win)`, maybe the autoplay line, Task 12 menu item | Keep each change to its single line. |
| `internal/app/events.go` | `reply:open` | untouched (wake events live in `wake.go`) | — |
| `frontend/src/settings.ts` | reply hotkey, working days, end of day; Phase 2 sync section | Voice section | Append after the existing sections. Phase 2's settings split (`settings.Shared` / per-device) does not affect the wake switch, which lives in `voice/wake.json`. |
| `frontend/src/api.ts` | reply bindings and settings fields | voice bindings | Additive. |
| `internal/app/service.go` | `SetHotkeyRecording` suspends both hotkeys | untouched | — |
