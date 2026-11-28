# Voice accuracy: better models, decoding and vocabulary

The user's words: "Voice to text quality is really poor, some word is not able to identify, looks like indian accent issue - need this real". Dictation ran `ggml-base.en.bin` with greedy decoding and only the recent text as a prompt. Base is the weakest model worth using, and it is weakest of all on accented English.

## Rulings

1. **Three speech models, chosen per device.** All are pinned to Hugging Face `ggerganov/whisper.cpp` commit `5359861c739e955e79d9a303bcbc70fb988958b1` (the same commit as before). The sizes and LFS SHA-256 values come from the HF API, and were checked against HEAD requests (`X-Linked-Size`, `X-Linked-ETag`) and against full downloads.

   | id | file | size | label in Settings |
   |---|---|---|---|
   | `base` | `ggml-base.en.bin` | 147,964,211 | Base: fastest, often misses words with accents |
   | `small` | `ggml-small.en-q8_0.bin` | 264,477,561 | Small: much more accurate with Indian and other accents (**recommended**) |
   | `turbo` | `ggml-large-v3-turbo-q5_0.bin` | 574,041,195 | Large v3 Turbo: most accurate, but slow without a GPU |

   **Small is the recommended model, not Large v3 Turbo.** The measurements in the plan were taken on the user's own laptop (i7-11850H, under its normal load). With the CPU engine, Turbo's encoder needs 16 to 22 s per 30 s window, so a dictated phrase takes 20 s or more. Small takes about 5 s, and Base about 2.5 s. Small is the largest model that still works for dictation on a CPU. Turbo stays on offer for someone who accepts the wait, and its hint says so.

   Small uses **q8_0 rather than q5_1**. On this CPU (AVX-512 VNNI) q8_0 encodes faster (3.0 s against 4.0 s) and is practically lossless. It costs 74 MB more to download.

   Turbo is multilingual, so **English is always forced**: `-l en` at launch and `language=en` on every request. That keeps it from drifting into Hindi or translating.

2. **The default.** A device with no valid `voice.json` keeps Base if `ggml-base.en.bin` is installed, so existing users don't face a surprise download. Otherwise it uses another installed catalogue model (the most accurate first), or else `small`. New installs therefore download the recommended model through the usual consent flow (engine plus model, about 273 MB). Settings tells Base users plainly that Small is much more accurate for accented speech.

3. **Switching models.**
   - **Nothing installed yet** (`absent`): choosing a model only records it. The capture box's consent then offers that model's size.
   - **A model is ready**: choosing another downloads it in the background (`.part`, then checked for size and hash, then renamed). **Dictation keeps working on the old model meanwhile**: the state stays `ready`, and `Status.target/phase/done/total` report the switch. When the file is in place, the choice is saved, the engine is replaced at its next use, and the old model file is deleted (one model on disk at a time; a failed delete, and any `.part` left by quitting mid-download, is cleaned up at the next start).
   - **The model is already on disk**: switching to it is immediate.
   - **Cancel and errors**: a switch can be cancelled. An error appears in `Status.switchError`, and the old model stays.
   - **During the first install**, or while another switch is running, choosing a model is refused.

4. **Per-device settings in `voice.json`.** The file sits in the voice folder next to `wake.json`, holding `{"model":"small","words":"…"}` and written atomically. Two reasons: the model file is per device, and the voice folder moves with the data folder. The vocabulary is not synced through Atlas; one device is the common case, and the synced `Settings`/`Shared` contract stays as it is.

5. **Decoding.** whisper-server b5454 supports each of these; checked in `examples/server/server.cpp` at `d1be6fde`.
   - **Launch flags:** `-bs 5 -bo 5 -sns`.
   - **Every request also sends** `language=en`, `beam_size=5`, `best_of=5`, `temperature=0`, `temperature_inc=0.2` (temperature fallback stays on), `no_speech_thold=0.6` and `suppress_nst=true`.
   - **Thresholds:** the entropy (2.4) and logprob (-1.0) thresholds stay at whisper's defaults, and they drive the fallback.
   - **Why both:** the server copies its launch defaults into each request, so the request fields only make the intent explicit and survive a changed default.
   - **whisper-cli** (the Homebrew path on macOS) already defaults to beam 5 / best-of 5, and gets the same prompt.

6. **Prompt.** `BuildPrompt(words, recent)` builds the prompt in Go for every transcription, whole dictations and segments alike. It joins three parts, in this order:
   - a fixed passage of reminders with Indian names and terms, with times written as digits ("at 9:30 am", "5 pm");
   - `Words I use:` and the user's list;
   - the recent text the capture box already sends.

   whisper keeps the **last** `n_text_ctx/2 = 224` prompt tokens, so the most specific context goes last. The prompt is capped at 600 characters, about 220 tokens even at a pessimistic 2.7 characters per token for names. The recent text is cut to its last 200 characters at a word boundary. The words list is limited to 300 characters when saved. Whole sentences are dropped from the start of the fixed passage until the prompt fits.

7. **Times written with a dot.** With the prompt, whisper often writes "9.30 am" (the Indian and British form). `CleanTranscript` rewrites two shapes as `H:MM`, which the capture parser reads:
   - `H.MM am/pm`, with an hour of 1 to 12;
   - `at|until|till|before|after H.MM`, with an hour of 0 to 23, unless an amount follows (each, lakh, rupees, %, kg and so on).

   `by` and `from` are left out, because they come before amounts too often ("by 1.50 lakh"). Other decimals ("2.50", "at 2.50 each") are left alone, and "a.m"/"p.m" without the final dot count as am/pm.

8. **Prompt echo guard.** whisper sometimes repeats its prompt on silence. `DropPromptEcho` drops two kinds of transcript:
   - one that starts with "Words I use", which is the vocabulary line;
   - for a dictation segment with under 2.5 s of speech, one that is exactly a sentence of the fixed passage, ignoring case and punctuation.

   A sentence that long can't be said in under 2.5 s, so a real dictation of it is kept.

9. **"Words I use".** Entries are separated by commas, semicolons or new lines. Each is trimmed, empty entries are dropped, and duplicates are removed (case-insensitively, keeping the first spelling). The list is stored joined with ", ", and a list over 300 characters (counted as characters, not bytes) is refused with a message.

10. **Settings UI** (`voice-settings.ts`, at the top of the Voice section; none of the theme session's files change). It reuses the existing classes and adds no CSS:
    - a "Speech model" `select.input.input-select`, whose options show the label, size and "· Recommended";
    - a help line for the choice, which also mentions the download size when choosing it would download;
    - a recommendation line while another model is active;
    - while a switch runs, its progress (`cap-progress`), a status line and Cancel, with the select locked;
    - a "Words I use" text input (`input input-path`), saved on change and redrawn in its normalised form.

11. **Rejected: a clip-sized `audio_ctx`.** Shrinking the encoder window to the clip makes it 2 to 4 times faster. But Turbo then returns nonsense ("and then, it's the"), and Base starts missing words ("Venkates", "oddheart"). It is not used.

12. **Follow-up, not built here: a GPU engine.** The laptop has an NVIDIA T600 (4 GB). whisper.cpp b5454 ships Windows CUDA builds (285 MB for CUDA 11.8, 685 MB for CUDA 12.4) and no Vulkan build. A CUDA engine would make Turbo fast. It needs its own engine choice, a driver check and a CPU fallback.

## API

- `VoiceService.Models() []voice.ModelInfo`: `{id, label, hint, size, recommended, installed, active}`.
- `VoiceService.SetModel(id) error` and `VoiceService.CancelModelSwitch()`.
- `VoiceService.Words() string` and `VoiceService.SetWords(text) error`.
- `voice.Status` gains `model`, `target` and `switchError`.

## Measurements

See "Measurements" at the end of the plan, `docs/superpowers/plans/2026-11-28-voice-accuracy.md`.
