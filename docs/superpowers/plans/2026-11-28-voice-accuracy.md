# Plan: voice accuracy

Spec: `docs/superpowers/specs/2026-11-28-voice-accuracy-design.md`.

1. `internal/voice/models.go`: the model catalogue (`base`, `small`, `turbo`) pinned by commit, size and SHA-256; `ModelInfo`; `voice.json` load/save (`voiceConfig`); `NormalizeWords`.
2. `internal/voice/prompt.go`: the fixed passage, `BuildPrompt(words, recent)` within the 600-char budget, and `IsPromptEcho`.
3. `internal/voice/manager.go`: the active model from `voice.json` (default rules), `Models()`, `SetModel` (immediate, absent, background switch with cancel and old-file removal), `SetWords`/`Words`, the prompt built for every transcription, and `Status.model/target/switchError`.
4. `internal/voice/engine.go`, `cli.go`: decoding flags and per-request fields.
5. `internal/app/voice.go`: bindings for `Models`, `SetModel`, `CancelModelSwitch`, `Words` and `SetWords`; regenerate the TS bindings.
6. `frontend/src/api.ts` and `voice-settings.ts`: the "Speech model" select, switch progress, the recommendation line and the "Words I use" input, with pure helpers tested in `voice-settings.test.ts`.
7. Tests: Go (catalogue pins, default rules, switch flow, cancel, prompt budget and order, echo guard, words normalisation, `voice.json` round trip, server request fields and launch flags) and vitest (helpers, section rendering, round trip through the mocked api).
8. Review, merge `origin/master`, run the full suite, merge into `master`, push.

## Measurements

These were taken on the user's laptop: an i7-11850H (8 cores, 16 threads, AVX-512) with an NVIDIA T600 that is not used, running whisper-server b5454 with `-t 8`. The machine was busy with its normal load (Teams, browsers, other builds), so treat the times as rough.

Encoder only (`whisper-bench`, 30 s window):

| model | encode |
|---|---|
| base.en | 1.27 s |
| small.en q5_1 | 3.97 s |
| small.en q8_0 | 3.03 s |
| large-v3-turbo q5_0 | 22.2 s |
| large-v3-turbo q8_0 | 16.3 s |

Whole request for a 5 to 6 s phrase, with beam 5, the fixed prompt and "Words I use: Venkatesh, Shubham, Aadhaar". Run with `AR_VOICE_BENCH=<folder> go test ./internal/voice -run TestRealModels -v`.

| model | time per phrase | how it wrote "Aadhaar" |
|---|---|---|
| Base | 2.4 to 2.6 s | "Aadhar" |
| Small q8_0 | 4.9 to 5.0 s | "Aadhaar" (correct) |
| Turbo q5_0 | about 25 s | "Aadhar" (run without the words) |

- **Beam 5 against greedy:** about 2 s more per phrase on Small, and nothing measurable on Base.
- **A clip-sized `audio_ctx`:** rejected (spec, ruling 11). Turbo returned "and then, it's the", and Base returned "Venkates … oddheart".
- **The test clips:** synthetic, made with Windows' US English voice "Zira", because no Indian English voice is installed. They measure speed and the effect of the vocabulary, not accent robustness. That needs the user's own voice. Record a few phrases as 16 kHz mono WAV files into a folder with the engine and models, then run `TestRealModels` on it.
