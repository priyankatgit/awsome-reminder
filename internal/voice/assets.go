// Package voice turns dictated audio into text locally with whisper.cpp.
//
// The engine (whisper.cpp release binaries) and the English model are
// downloaded only after the user agrees, and each download is checked
// against the SHA-256 pinned here. The engine runs as a subprocess only
// while the user dictates and exits after a short idle, so no model sits in
// memory otherwise.
package voice

// Asset is one pinned download.
type Asset struct {
	Name    string
	URL     string
	SHA256  string // lowercase hex
	Size    int64  // bytes
	Archive string // "zip", "tar.gz", or "" for a plain file
}

// EngineRelease is the pinned whisper.cpp release build: b5454 is the
// release of tag v1.9.5 (commit d1be6fde11ac6e0407606b4e42fe72d34add8037).
const EngineRelease = "b5454"

const engineBase = "https://github.com/ggml-org/whisper.cpp/releases/download/" + EngineRelease + "/"

// engines lists the official prebuilt CPU engines, keyed by GOOS/GOARCH.
// Hashes are the digests GitHub reports for the release assets, checked
// against a local download.
var engines = map[string]Asset{
	"windows/amd64": {
		Name:    "speech engine",
		URL:     engineBase + "whisper-bin-x64.zip",
		SHA256:  "6ba69e3482d7826214f90a6a9c84ca07782aec1e1d0c6a7c30c994fd5d816ccb",
		Size:    8928640,
		Archive: "zip",
	},
	"linux/amd64": {
		Name:    "speech engine",
		URL:     engineBase + "whisper-bin-ubuntu-x64.tar.gz",
		SHA256:  "a72becf15d7917f990f6313867a52638b82b7f9ef237fb0c980dac56a135781c",
		Size:    10364195,
		Archive: "tar.gz",
	},
	"linux/arm64": {
		Name:    "speech engine",
		URL:     engineBase + "whisper-bin-ubuntu-arm64.tar.gz",
		SHA256:  "6b95ebfc60447df48e70ef00a73bdc3f41679ed2d01ef465827206a2ff325149",
		Size:    4608377,
		Archive: "tar.gz",
	},
}

// EngineAsset returns the official prebuilt engine for goos/goarch; false
// when whisper.cpp publishes none (macOS, for one).
func EngineAsset(goos, goarch string) (Asset, bool) {
	a, ok := engines[goos+"/"+goarch]
	return a, ok
}
