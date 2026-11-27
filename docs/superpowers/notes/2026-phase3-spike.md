# Phase 3 wake-word spike: raw numbers (2026-11-28)

Release: sherpa-onnx **v2.0.3** (latest at the time). Tag base: `https://github.com/k2-fsa/sherpa-onnx/releases/download/v2.0.3/`. Model base: `.../releases/download/kws-models/`.

All accuracy numbers are a **TTS proxy** (Windows SAPI voices Hazel en-GB and Zira en-US, 16 kHz mono). Real-voice and 1 h real-audio tests, and every microphone run, are left to the user's manual checklist. This machine's session could not speak into a microphone, and no microphone program exists for Windows (see below), so nothing here ran a live mic.

## 1. Finding: the release ships no Windows microphone program

`sherpa-onnx-v2.0.3-win-x64-shared-MD-Release.tar.bz2` and `...-MT-Release.tar.bz2` contain only the file-based `sherpa-onnx-keyword-spotter.exe` (no `*microphone*` .exe of any kind). Linux and macOS archives do ship `sherpa-onnx-keyword-spotter-microphone`. Decision (controller): Windows uses our own helper (app exe with `--wake-helper`) that loads the C API DLL, captures with winmm waveIn, RMS-gates, and prints lines in the same format as the stock microphone program. Linux and macOS use the stock program.

## 2. Asset table (6 rows)

There is no asset named `*-win-x64-shared.tar.bz2`; the Windows name pattern is `win-x64-shared-{MD,MT}-{Release,MinSizeRel,RelWithDebInfo,Debug}[-no-tts][-lib]`. `-lib` archives hold only `lib/` (DLLs + import libs) and `include/`, no programs. macOS: `osx-x64-shared` exists, so no universal2 is needed.

| Platform | Asset | URL | Size (bytes) | SHA-256 |
|---|---|---|---|---|
| windows/amd64 | `sherpa-onnx-v2.0.3-win-x64-shared-MT-Release-no-tts-lib.tar.bz2` | `https://github.com/k2-fsa/sherpa-onnx/releases/download/v2.0.3/sherpa-onnx-v2.0.3-win-x64-shared-MT-Release-no-tts-lib.tar.bz2` | 8307815 | `06bfa330d96cba1963fc7f43df1049db785e0662724a15ff8eb8467d3086db16` |
| linux/amd64 | `sherpa-onnx-v2.0.3-linux-x64-shared.tar.bz2` | `https://github.com/k2-fsa/sherpa-onnx/releases/download/v2.0.3/sherpa-onnx-v2.0.3-linux-x64-shared.tar.bz2` | 29936514 | `615b1bf0140bf09051c15fde6579e48543696352ebd477217d2e69b49d9aec13` |
| linux/arm64 | `sherpa-onnx-v2.0.3-linux-aarch64-shared-cpu.tar.bz2` | `https://github.com/k2-fsa/sherpa-onnx/releases/download/v2.0.3/sherpa-onnx-v2.0.3-linux-aarch64-shared-cpu.tar.bz2` | 29516176 | `c42748225180235bdd4ba10f8c4575b54beb8d5b26a5303bb3051026a0fe4e53` |
| darwin/arm64 | `sherpa-onnx-v2.0.3-osx-arm64-shared.tar.bz2` | `https://github.com/k2-fsa/sherpa-onnx/releases/download/v2.0.3/sherpa-onnx-v2.0.3-osx-arm64-shared.tar.bz2` | 21914267 | `14afb4ed15dc4f376c69be5a795039824e47f5ffcfc2488556f796ecaf420d07` |
| darwin/amd64 | `sherpa-onnx-v2.0.3-osx-x64-shared.tar.bz2` | `https://github.com/k2-fsa/sherpa-onnx/releases/download/v2.0.3/sherpa-onnx-v2.0.3-osx-x64-shared.tar.bz2` | 24628586 | `8fdd4f2c4dcda061437f68b292ebee9f3be1f3ab0f533cbbd20be9730dae68f0` |
| KWS model | `sherpa-onnx-kws-zipformer-gigaspeech-3.3M-2024-01-01.tar.bz2` | `https://github.com/k2-fsa/sherpa-onnx/releases/download/kws-models/sherpa-onnx-kws-zipformer-gigaspeech-3.3M-2024-01-01.tar.bz2` | 17626723 | `f170013b4716e41b62b9bfd809687c207cef798ef9bc6534d524e17af9b6561a` |

Sizes of all rows were checked against the GitHub API and match; the Windows row is the downloaded file size.

For comparison (not pinned): `win-x64-shared-MD-Release.tar.bz2`, 27162680 bytes, SHA-256 `dd1eea0b1f88733aefebd8397412b7f17d00fa1d44140f4c69d11f060c0156d5` (has the exe and the DLLs, needs the VC++ runtime). KWS `-mobile` archive: 15667804 bytes, SHA-256 `2e6ac2577310bfa2f4b6b5fab0478b868c9d0b2cb2c51b3e13b50581b588864d` (has int8 encoder 4.0 MB and joiner, but only the fp32 decoder; not used).

## 3. Windows archive (MT-Release-no-tts-lib): contents and imports

Files (inside `sherpa-onnx-v2.0.3-win-x64-shared-MT-Release-no-tts-lib/`):

| File | Bytes |
|---|---|
| `lib/onnxruntime.dll` | 17799168 |
| `lib/onnxruntime_providers_shared.dll` | 104960 |
| `lib/sherpa-onnx-c-api.dll` (the C API DLL) | 4888064 |
| `lib/sherpa-onnx-cxx-api.dll` | 846336 |
| `lib/*.lib`, `include/` | not needed |

**Keep:** `lib/sherpa-onnx-c-api.dll`, `lib/onnxruntime.dll`, `lib/onnxruntime_providers_shared.dll` (providers_shared is loaded by onnxruntime on demand; keep it). Drop `sherpa-onnx-cxx-api.dll`, `*.lib`, headers.

DLL imports (PE import table, read with `pefile`):

| DLL | Imports |
|---|---|
| `sherpa-onnx-c-api.dll` | KERNEL32.dll, onnxruntime.dll |
| `onnxruntime.dll` | ADVAPI32.dll, KERNEL32.dll, SETUPAPI.dll, api-ms-win-core-path-l1-1-0.dll, dbghelp.dll, dxgi.dll |
| `onnxruntime_providers_shared.dll` | KERNEL32.dll |
| `sherpa-onnx-cxx-api.dll` | KERNEL32.dll, sherpa-onnx-c-api.dll |

Every import is a Windows system DLL. **No MSVCP140 / VCRUNTIME140 / api-ms-win-crt**: the MT (static CRT) build does not need the Visual C++ runtime. Contrast MD-Release: `sherpa-onnx-c-api.dll` and `onnxruntime.dll` import MSVCP140.dll, VCRUNTIME140.dll, VCRUNTIME140_1.dll and ~10 `api-ms-win-crt-*` DLLs, so MD needs the VC++ 2015-2022 x64 runtime. So with the MT archive the `RuntimeMissing` check is not needed for the wake helper.

Exports of `sherpa-onnx-c-api.dll` with `Keyword` in the name: SherpaOnnxCreateKeywordSpotter, SherpaOnnxCreateKeywordStream, SherpaOnnxCreateKeywordStreamWithKeywords, SherpaOnnxDecodeKeywordStream, SherpaOnnxDecodeMultipleKeywordStreams, SherpaOnnxDestroyKeywordResult, SherpaOnnxDestroyKeywordSpotter, SherpaOnnxFreeKeywordResultJson, SherpaOnnxGetKeywordResult, SherpaOnnxGetKeywordResultAsJson, SherpaOnnxIsKeywordStreamReady, SherpaOnnxResetKeywordStream. (`SherpaOnnxOnlineStreamAcceptWaveform` and `SherpaOnnxDestroyOnlineStream` are also exported.)

**Verified working:** a Python `ctypes` program loaded this MT `sherpa-onnx-c-api.dll` with the structs below, created a spotter from the int8 model and `wake_keywords.txt` content, fed 0.1 s chunks of a "Hey Awesome" TTS wav and got a detection (`keyword` = `HEY_AWESOME`, `count` = 7, `json` as in section 5). Script: `ct.py` in the scratch folder (not committed).

## 4. C API declarations (v2.0.3 `sherpa-onnx/c-api/c-api.h`)

Saved copy: `C:/Users/PRIYAN~1.PAT/AppData/Local/Temp/claude/163512b7affcac32/88874908-086b-480f-a9df-f44099c25b21/scratchpad/spike/c-api.h` (5248 lines, from `https://raw.githubusercontent.com/k2-fsa/sherpa-onnx/v2.0.3/sherpa-onnx/c-api/c-api.h`). `SHERPA_ONNX_API` is `__declspec(dllimport)` on Windows (the DLL exports plain C names, `__cdecl`, which on x64 is the one Windows calling convention).

```c
typedef struct SherpaOnnxOnlineTransducerModelConfig {
  const char *encoder; const char *decoder; const char *joiner;
} SherpaOnnxOnlineTransducerModelConfig;
typedef struct SherpaOnnxOnlineParaformerModelConfig {
  const char *encoder; const char *decoder;
} SherpaOnnxOnlineParaformerModelConfig;
typedef struct SherpaOnnxOnlineZipformer2CtcModelConfig { const char *model; } ...;
typedef struct SherpaOnnxOnlineNemoCtcModelConfig       { const char *model; } ...;
typedef struct SherpaOnnxOnlineToneCtcModelConfig       { const char *model; } ...;

typedef struct SherpaOnnxOnlineModelConfig {
  SherpaOnnxOnlineTransducerModelConfig transducer;
  SherpaOnnxOnlineParaformerModelConfig paraformer;
  SherpaOnnxOnlineZipformer2CtcModelConfig zipformer2_ctc;
  const char *tokens;
  int32_t num_threads;
  const char *provider;
  int32_t debug;
  const char *model_type;
  const char *modeling_unit;
  const char *bpe_vocab;
  const char *tokens_buf;
  int32_t tokens_buf_size;
  SherpaOnnxOnlineNemoCtcModelConfig nemo_ctc;
  SherpaOnnxOnlineToneCtcModelConfig t_one_ctc;
} SherpaOnnxOnlineModelConfig;

typedef struct SherpaOnnxFeatureConfig {
  int32_t sample_rate;   // 16000
  int32_t feature_dim;   // 80
} SherpaOnnxFeatureConfig;

typedef struct SherpaOnnxKeywordSpotterConfig {
  SherpaOnnxFeatureConfig feat_config;
  SherpaOnnxOnlineModelConfig model_config;
  int32_t max_active_paths;     // 4 in the stock program
  int32_t num_trailing_blanks;  // 1 in the stock program
  float keywords_score;         // default 1.0
  float keywords_threshold;     // default 0.25
  const char *keywords_file;
  const char *keywords_buf;
  int32_t keywords_buf_size;
} SherpaOnnxKeywordSpotterConfig;

typedef struct SherpaOnnxKeywordResult {
  const char *keyword;              // label, e.g. "HEY_AWESOME"; empty string when no detection
  const char *tokens;
  const char *const *tokens_arr;
  int32_t count;                    // number of tokens
  float *timestamps;
  float start_time;
  const char *json;                 // same JSON as the file-based program prints
} SherpaOnnxKeywordResult;

typedef struct SherpaOnnxKeywordSpotter SherpaOnnxKeywordSpotter;   // opaque
typedef struct SherpaOnnxOnlineStream  SherpaOnnxOnlineStream;      // opaque

const SherpaOnnxKeywordSpotter *SherpaOnnxCreateKeywordSpotter(const SherpaOnnxKeywordSpotterConfig *config);
void SherpaOnnxDestroyKeywordSpotter(const SherpaOnnxKeywordSpotter *spotter);
const SherpaOnnxOnlineStream *SherpaOnnxCreateKeywordStream(const SherpaOnnxKeywordSpotter *spotter);
const SherpaOnnxOnlineStream *SherpaOnnxCreateKeywordStreamWithKeywords(const SherpaOnnxKeywordSpotter *spotter, const char *keywords);
void SherpaOnnxOnlineStreamAcceptWaveform(const SherpaOnnxOnlineStream *stream, int32_t sample_rate, const float *samples, int32_t n);
int32_t SherpaOnnxIsKeywordStreamReady(const SherpaOnnxKeywordSpotter *spotter, const SherpaOnnxOnlineStream *stream);
void SherpaOnnxDecodeKeywordStream(const SherpaOnnxKeywordSpotter *spotter, const SherpaOnnxOnlineStream *stream);
void SherpaOnnxResetKeywordStream(const SherpaOnnxKeywordSpotter *spotter, const SherpaOnnxOnlineStream *stream);
const SherpaOnnxKeywordResult *SherpaOnnxGetKeywordResult(const SherpaOnnxKeywordSpotter *spotter, const SherpaOnnxOnlineStream *stream);
void SherpaOnnxDestroyKeywordResult(const SherpaOnnxKeywordResult *r);
void SherpaOnnxDestroyOnlineStream(const SherpaOnnxOnlineStream *stream);
```

Notes for the Go binding: pass samples as float32 in [-1, 1]; unused strings must be NULL (zero the whole config struct first); on amd64 the structs follow normal C alignment (4-byte int32/float, 8-byte pointers, padding after int32 before a pointer). Detection loop as in the stock program: `while IsKeywordStreamReady { Decode; r = GetKeywordResult; if r->keyword[0] != 0 { report; ResetKeywordStream }; DestroyKeywordResult(r) }`.

## 5. The stock program's output and start-up (source read at tag v2.0.3; Linux/macOS not run here)

Source: `sherpa-onnx/csrc/sherpa-onnx-keyword-spotter-microphone.cc`, `display.h`, `microphone.cc`, `keyword-spotter.cc` (`KeywordResult::AsJsonString`), `macros.h`.

**Detections go to stderr, not stdout** (`Display::Print` writes with `fprintf(stderr, ...)`, then `fflush(stderr)`). The stock program writes nothing to stdout. Format:
- Per detection: `<index>:<json>` where index counts 0, 1, 2, ... in this process, and `<json>` is:
  `{"start_time":0.00, "keyword": "HEY_AWESOME", "timestamps": [3.52, 3.64, 3.84, 3.96, 4.00, 4.04, 4.08], "tokens":[" HE", "Y", " A", "W", "ES", "OM", "E"]}`
  (the JSON above was produced by the DLL / file-based exe in this spike; the mic program prints the same `AsJsonString`).
- **Non-MSVC builds (Linux, macOS), `display.h`:** the first detection is printed as `\r0:{json}` with **no trailing newline**; each later detection is preceded by `\n\r` (so `\n\r1:{json}`). The newline therefore arrives only with the *next* detection (or never). A line-based parser must not wait for `\n`: scan the stream for `\r?<digits>:{` and read to the closing `}` (the JSON is one line, no nested braces except none in tokens). Terminal clear escapes (`ESC[2K`, `ESC[1A`) are used only if the same segment id repeats, which cannot happen here because the index increments on every detection.
- MSVC builds would print `<index>:<json>\n` (this is the branch our Windows helper should mimic: `0:{json}\n`, one flush per detection). Our parser should accept both forms: find `{"start_time":` ... `}` and read `"keyword": "<LABEL>"`.
- After a detection the program calls `spotter.Reset` (so the same phrase is not reported twice).

**Start-up lines (stderr), in order:**
1. `<source path>:Read:379 '<argv joined>'` (parse-options echo; on Windows file-based exe it is `D:\a\sherpa-onnx\...\parse-options.cc:Read:379 '...'`), then a blank line.
2. `KeywordSpotterConfig(feat_config=FeatureExtractorConfig(sampling_rate=16000, feature_dim=80, ...), model_config=OnlineModelConfig(transducer=..., ...), max_active_paths=4, num_trailing_blanks=1, keywords_score=1, keywords_threshold=0.25, keywords_file="...")` (seen from the file-based exe, same `config.ToString()`).
3. (mic program only) `Num devices: N`, then one line per device: ` <*| > <index> <name>` (`*` marks the chosen one), `Use device: <index>`, `  Name: <name>`, `  Max input channels: <n>`. Optionally `Use specified device: <v>` and `Use sample rate <f> for mic` when the env vars below are set.
- Env vars: `SHERPA_ONNX_MIC_DEVICE=<portaudio index>` and `SHERPA_ONNX_MIC_SAMPLE_RATE=<hz>`. There is no `--mic-device` flag.
- No "ready" line exists. "Listening" can only be inferred from `Use device:` / `Name:` lines having been printed (and no exit).

**Failure behaviour (from source):** no default input device: stderr `No default input device found`, then two lines about switching to `./bin/sherpa-onnx-keyword-spotter-alsa` on Linux, exit code 1 (`_Exit(EXIT_FAILURE)`). Invalid device index: `Invalid device index: <n>`; then `portaudio error: <n>`, exit code 1. Bad config: `Errors in config!`, exit code -1 (255 on Linux/macOS, 4294967295 shows as -1 on Windows). `Ctrl+C` / SIGINT prints `Caught Ctrl + C. Exiting...` and returns 0. Not run locally (no Linux/macOS machine and no Windows mic program).

**Argument list (all platforms; model files from the model archive):**
```
sherpa-onnx-keyword-spotter-microphone \
  --tokens=<model>/tokens.txt \
  --encoder=<model>/encoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx \
  --decoder=<model>/decoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx \
  --joiner=<model>/joiner-epoch-12-avg-2-chunk-16-left-64.int8.onnx \
  --keywords-file=<keywords file> --num-threads=1 --provider=cpu
```
`--max-active-paths` (default 4) and `--num-trailing-blanks` (default 1) are left at defaults; `keywords_score`/`keywords_threshold` are default 1 / 0.25 but are overridden per line by `:N` and `#N` in the keywords file. The file-based program takes the same flags plus one wav path (positional) and prints the JSON (one per detection) on **stdout**, then `Number of threads`, `Audio duration`, `Elapsed seconds`, `RTF` lines.

## 6. Program names and keep-lists

Verified from `tar -tjf` listings only for Linux and macOS (not run; shared-library dependencies are what is shipped in `lib/`, **unverified by ldd/otool**).

| Platform | Program (path in archive) | Keep from archive |
|---|---|---|
| windows/amd64 (MT-lib) | none shipped: our own helper | `lib/sherpa-onnx-c-api.dll`, `lib/onnxruntime.dll`, `lib/onnxruntime_providers_shared.dll` |
| linux/amd64 | `bin/sherpa-onnx-keyword-spotter-microphone` (995848 B; also `-alsa`, 844472 B) | + `lib/libsherpa-onnx-c-api.so` (5795392), `lib/libsherpa-onnx-cxx-api.so` (276712), `lib/libonnxruntime.so` (27026609) |
| linux/arm64 | `bin/sherpa-onnx-keyword-spotter-microphone` (792440 B; also `-alsa`) | + `lib/libsherpa-onnx-c-api.so` (5236496), `lib/libsherpa-onnx-cxx-api.so` (283488), `lib/libonnxruntime.so` (34606912) |
| darwin/arm64 | `bin/sherpa-onnx-keyword-spotter-microphone` (728672 B) | + `lib/libsherpa-onnx-c-api.dylib` (4962096), `lib/libsherpa-onnx-cxx-api.dylib` (183760), `lib/libonnxruntime.dylib` (28775120) |
| darwin/amd64 | `bin/sherpa-onnx-keyword-spotter-microphone` (767328 B) | + `lib/libsherpa-onnx-c-api.dylib` (5396320), `lib/libsherpa-onnx-cxx-api.dylib` (155744), `lib/libonnxruntime.dylib` (32315856) |

Archive top directory is `sherpa-onnx-v2.0.3-<platform>-shared[-cpu]/`. The programs most likely load rpath `$ORIGIN/../lib` (Linux) / `@loader_path/../lib` (macOS), so keep `bin/` and `lib/` as siblings. This was not verified. The cxx-api library may be unneeded by the spotter (not checked; the file-based Windows exe imports only c-api and onnxruntime, so for Linux/macOS the cxx-api library is probably an unused dependency, but without `ldd`/`otool` keep it to be safe).

**Model archive** `sherpa-onnx-kws-zipformer-gigaspeech-3.3M-2024-01-01/` (keep these paths): `tokens.txt` (5006), `encoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx` (4807159), `decoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx` (277985), `joiner-epoch-12-avg-2-chunk-16-left-64.int8.onnx` (163380). `bpe.model` (244837) is only needed for text2token (the spike), not at runtime. fp32 files (encoder 12174219, decoder 1063189, joiner 642462) are not needed. Total kept: about 5.25 MB.

## 7. Keywords

`text2token` (sherpa-onnx 2.0.3 PyPI, plus `click`, `pypinyin`, `sentencepiece`, which `sherpa-onnx-cli` needs but does not declare):

```
sherpa-onnx-cli text2token --tokens tokens.txt --tokens-type bpe --bpe-model bpe.model kw_raw.txt kw_out.txt
input : HEY AWESOME :2.0 #0.25 @HEY_AWESOME
output: ▁HE Y ▁A W ES OM E :2.0 #0.25 @HEY_AWESOME
```
(`▁` is U+2581; on Windows run with `PYTHONUTF8=1`.) Final tuned line, committed as `internal/voice/wake_keywords.txt`:

```
▁HE Y ▁A W ES OM E :1.0 #0.25 @HEY_AWESOME
```

## 8. Accuracy sweep (TTS proxy)

Method: `sherpa_onnx.KeywordSpotter` (Python 2.0.3), `num_threads=1`, `provider=cpu`, `max_active_paths=4`, `num_trailing_blanks=1`, audio fed in 0.1 s chunks with 0.5 s of zeros at the end, reset after each detection.
- Positives: 18 files = voices {Hazel en-GB, Zira en-US} x rates {-2, 0, +2} x lead-in {none, "Okay, I am ready now. ", "The weather looks nice today. "} + "Hey Awesome". The "noisy" column adds Gaussian noise sigma 0.003 (about -50 dBFS).
- Negatives: 24.3 min (0.405 h) of TTS, 12 files, same two voices at three rates, each file containing 25 varied sentences in random order, including near-misses ("Hey, a sum of two numbers...", "They're awesome, said the crowd...", "Hey Austin, can you send me...", "Please pay a wesome amount...", "Hay is stored in the barn...", "He said hey, I am awesome at this...", "Hey, a swan is swimming...", "Hey Siri, hey Alexa, and okay Google...", "Aweso me, a some, hey a some thing odd...").
- A first negative set had a sentence that literally contained "Hey, awesome ..." which gave exactly one "false" detection per file (12 in 24 min, all identical across models/params): that was a true detection and the sentence was replaced. Numbers below are from the corrected set.

| model | boost (`:N`) | threshold (`#N`) | positives | noisy positives | false detections / 0.405 h | per hour |
|---|---|---|---|---|---|---|
| int8 | 1.0 | 0.15 | 18/18 | 18/18 | 0 | 0 |
| **int8** | **1.0** | **0.25** | **18/18** | **18/18** | **0** | **0** |
| int8 | 1.0 | 0.35 | 18/18 | 17/18 | 0 | 0 |
| int8 | 1.5 | 0.15 | 18/18 | 18/18 | 2 | 4.9 |
| int8 | 1.5 | 0.25 | 18/18 | 18/18 | 2 | 4.9 |
| int8 | 1.5 | 0.35 | 17/18 | 17/18 | 2 | 4.9 |
| int8 | 2.0 | 0.15 | 18/18 | 18/18 | 10 | 24.7 |
| int8 | 2.0 | 0.25 | 18/18 | 17/18 | 9 | 22.2 |
| int8 | 2.0 | 0.35 | 17/18 | 14/18 | 8 | 19.8 |
| fp32 | 1.0 | 0.15 | 18/18 | 18/18 | 0 | 0 |
| fp32 | 1.0 | 0.25 | 18/18 | 18/18 | 0 | 0 |
| fp32 | 1.0 | 0.35 | 17/18 | 17/18 | 0 | 0 |
| fp32 | 1.5 | 0.15 | 18/18 | 18/18 | 1 | 2.5 |
| fp32 | 1.5 | 0.25 | 18/18 | 18/18 | 1 | 2.5 |
| fp32 | 1.5 | 0.35 | 17/18 | 16/18 | 1 | 2.5 |
| fp32 | 2.0 | 0.15 | 18/18 | 18/18 | 9 | 22.2 |
| fp32 | 2.0 | 0.25 | 18/18 | 17/18 | 7 | 17.3 |
| fp32 | 2.0 | 0.35 | 17/18 | 12/18 | 6 | 14.8 |

Choice: **int8, boost 1.0, threshold 0.25** (meets 18/18 and 0 false in 24 min, with margin on both sides; a higher boost raises false triggers a lot). int8 and fp32 are equivalent on this proxy, int8 is smaller and faster. The `-mobile` archive was not tried (its decoder is fp32-only and it has no accuracy advantage). Caveat: with 0 false in 0.405 h the proxy cannot confirm "<= 1/h" statistically (upper bound about 7/h at 95 %); real speech and a 1 h podcast test are for the user. Tuning guide if real voice misses: raise boost to 1.5 (cost: more false wakes); if real audio gives false wakes: lower boost or raise threshold toward 0.35.

## 9. CPU / RAM (Windows, file-based program, streaming-equivalent)

Machine: the user's Windows 11 box (the spike session machine). Program: MD-Release `bin/sherpa-onnx-keyword-spotter.exe`, int8 model, `--num-threads=1 --provider=cpu`, run on a 5 min 16 kHz mono wav (the exe decodes in chunks as fast as it can, so wall time / audio duration = RTF = fraction of one core needed at real-time pace). Measured with a PowerShell loop polling `PeakWorkingSet64` / `TotalProcessorTime` every 200 ms.

| Input | Variant | RTF (program's own) | CPU "of one core" (RTF x 100) | Sampled CPU time / wall | Peak working set |
|---|---|---|---|---|---|
| 5 min TTS speech | int8 | 6.868 s / 300 s = 0.023 | **2.3 %** | 7.9 s CPU / 8.2 s wall (incl. 1.2 s model load) | 100.3 MB (contains 5 min of audio held in memory) |
| 5 min near-silent noise (sigma 8/32768) | int8 | 6.906 s / 300 s = 0.023 | 2.3 % (ungated) | n/a | 100.2 MB |
| 5 min TTS speech | fp32 | 8.891 s / 300 s = 0.030 | 3.0 % | 9.6 s CPU | 105.1 MB |
| 6.6 s clip (baseline, little audio buffered) | int8 | 0.024 | | | **54.5 to 60.8 MB** (two runs) |
| 6.6 s clip | fp32 | 0.024 | | | 65.3 MB |

Reading: the 5 min file runs are inflated by the stock exe loading the whole wav into memory as float (about 19 MB for samples, plus int16 copies). A streaming consumer (our helper) holds only a model and small buffers, so the **working set is about 55 to 61 MB** (int8) for the engine, below the 80 MB gate, measured with MD DLLs; MT DLLs are similar (a static CRT adds almost nothing). The helper process also includes the Go runtime and winmm code, which adds a few MB; not measured (the helper does not exist yet), so this must be re-measured on the real helper in a later task.

CPU: continuously speech-like audio costs 2.3 % of one core (gate 10 %), silence is 2.3 % if the model is fed continuously and near zero if the RMS gate skips silence (our helper's design). Both pass. The cost is dominated by the encoder; `--num-threads=1` is already the minimum.

**What was not measured:** microphone-driven runs (there is no Windows mic program; the quiet-room CPU through winmm capture and the RMS gate needs the real helper), live "speech through the speakers" mic run, real-voice hits from 1 m, 1 h false-wake test. These go to the manual checklist.

## 10. Detection output observed from the file-based program (stdout)

```
{"start_time":0.00, "keyword": "HEY_AWESOME", "timestamps": [3.28, 3.56, 3.84, 3.96, 4.00, 4.04, 4.08], "tokens":[" HE", "Y", " A", "W", "ES", "OM", "E"]}
{"start_time":0.00, "keyword": "HEY_AWESOME", "timestamps": [7.04, 7.40, 7.88, 8.00, 8.04, 8.08, 8.12], "tokens":[" HE", "Y", " A", "W", "ES", "OM", "E"]}
{"start_time":0.00, "keyword": "HEY_AWESOME", "timestamps": [34.88, 35.12, 35.52, 35.56, 35.60, 35.64, 35.68], "tokens":[" HE", "Y", " A", "W", "ES", "OM", "E"]}
```
(CRLF line endings on Windows; these were the file-based program's stdout. The mic program prints the same JSON prefixed with `<n>:` on stderr, see section 5.) Exit code 0 on success. The C-API `json` field matches (section 3).

## 11. Cleanup

No sherpa process was left running (`tasklist` checked). Downloads, extracted archives, venv and wavs are in the scratch folder `C:/Users/PRIYAN~1.PAT/AppData/Local/Temp/claude/163512b7affcac32/88874908-086b-480f-a9df-f44099c25b21/scratchpad/spike`, none in the repo.
