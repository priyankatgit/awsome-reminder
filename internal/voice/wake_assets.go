package voice

import (
	_ "embed"
	"strings"
)

// WakeRelease is the pinned sherpa-onnx release that provides the wake-word
// engine (see docs/superpowers/notes/2026-phase3-spike.md).
const WakeRelease = "v2.0.3"

const (
	wakeBase      = "https://github.com/k2-fsa/sherpa-onnx/releases/download/" + WakeRelease + "/"
	wakeModelBase = "https://github.com/k2-fsa/sherpa-onnx/releases/download/kws-models/"
)

// wakeKeywords is the tokenised keyword line ("Hey Awesome"), written next
// to the model at install time.
//
//go:embed wake_keywords.txt
var wakeKeywords []byte

var wakeEngines = map[string]Asset{
	"windows/amd64": {
		Name:    "wake word engine",
		URL:     wakeBase + "sherpa-onnx-v2.0.3-win-x64-shared-MT-Release-no-tts-lib.tar.bz2",
		SHA256:  "06bfa330d96cba1963fc7f43df1049db785e0662724a15ff8eb8467d3086db16",
		Size:    8307815,
		Archive: "tar.bz2",
	},
	"linux/amd64": {
		Name:    "wake word engine",
		URL:     wakeBase + "sherpa-onnx-v2.0.3-linux-x64-shared.tar.bz2",
		SHA256:  "615b1bf0140bf09051c15fde6579e48543696352ebd477217d2e69b49d9aec13",
		Size:    29936514,
		Archive: "tar.bz2",
	},
	"linux/arm64": {
		Name:    "wake word engine",
		URL:     wakeBase + "sherpa-onnx-v2.0.3-linux-aarch64-shared-cpu.tar.bz2",
		SHA256:  "c42748225180235bdd4ba10f8c4575b54beb8d5b26a5303bb3051026a0fe4e53",
		Size:    29516176,
		Archive: "tar.bz2",
	},
	"darwin/arm64": {
		Name:    "wake word engine",
		URL:     wakeBase + "sherpa-onnx-v2.0.3-osx-arm64-shared.tar.bz2",
		SHA256:  "14afb4ed15dc4f376c69be5a795039824e47f5ffcfc2488556f796ecaf420d07",
		Size:    21914267,
		Archive: "tar.bz2",
	},
	"darwin/amd64": {
		Name:    "wake word engine",
		URL:     wakeBase + "sherpa-onnx-v2.0.3-osx-x64-shared.tar.bz2",
		SHA256:  "8fdd4f2c4dcda061437f68b292ebee9f3be1f3ab0f533cbbd20be9730dae68f0",
		Size:    24628586,
		Archive: "tar.bz2",
	},
}

// WakeEngineAsset returns the pinned sherpa-onnx build for goos/goarch;
// false when there is none.
func WakeEngineAsset(goos, goarch string) (Asset, bool) {
	a, ok := wakeEngines[goos+"/"+goarch]
	return a, ok
}

// WakeModelAsset is the gigaspeech 3.3M keyword-spotting model.
var WakeModelAsset = Asset{
	Name:    "wake word model",
	URL:     wakeModelBase + "sherpa-onnx-kws-zipformer-gigaspeech-3.3M-2024-01-01.tar.bz2",
	SHA256:  "f170013b4716e41b62b9bfd809687c207cef798ef9bc6534d524e17af9b6561a",
	Size:    17626723,
	Archive: "tar.bz2",
}

// wakeEngineFile reports whether an engine archive entry is kept: the
// microphone keyword spotter (Linux and macOS) and the C API, onnxruntime
// and their providers library on every system. Windows ships no microphone
// program; the app itself is the helper there.
func wakeEngineFile(p string) bool {
	base := baseName(p)
	switch {
	case base == "sherpa-onnx-keyword-spotter-microphone":
		return true
	case base == "sherpa-onnx-c-api.dll", base == "onnxruntime.dll", base == "onnxruntime_providers_shared.dll":
		return true
	case strings.HasPrefix(base, "libsherpa-onnx-c-api."), strings.HasPrefix(base, "libsherpa-onnx-cxx-api."):
		return true
	case strings.HasPrefix(base, "libonnxruntime."):
		return true
	}
	return false
}

// wakeModelFiles are the int8 model files the spotter loads, plus tokens.
var wakeModelFiles = []string{
	"tokens.txt",
	"encoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx",
	"decoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx",
	"joiner-epoch-12-avg-2-chunk-16-left-64.int8.onnx",
}

// wakeModelFile reports whether a model archive entry is kept.
func wakeModelFile(p string) bool {
	base := baseName(p)
	for _, f := range wakeModelFiles {
		if base == f {
			return true
		}
	}
	return false
}
