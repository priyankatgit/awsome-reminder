package paths

import (
	"path/filepath"
	"runtime"
)

// Layout derives every data path from a root folder.
type Layout struct {
	Root     string
	Standard bool
}

// NewLayout builds the layout for root; Standard is true when root is the
// default config folder.
func NewLayout(root string) (Layout, error) {
	std, err := StandardRoot()
	if err != nil {
		return Layout{}, err
	}
	return Layout{Root: root, Standard: SameDir(root, std)}, nil
}

func (l Layout) DB() string          { return filepath.Join(l.Root, DBFile) }
func (l Layout) Log() string         { return filepath.Join(l.Root, "awesome.log") }
func (l Layout) Attachments() string { return filepath.Join(l.Root, "attachments") }
func (l Layout) SyncKey() string     { return filepath.Join(l.Root, "sync.key") }

// Voice is the legacy cache folder for the standard root (not created here),
// otherwise <root>/voice.
func (l Layout) Voice() string {
	if l.Standard {
		if d, err := LegacyVoiceDir(); err == nil {
			return d
		}
	}
	return filepath.Join(l.Root, "voice")
}

// Webview is the WebView2 user data folder; empty means Wails' default
// (standard root, or any non-Windows OS).
func (l Layout) Webview() string {
	if l.Standard || runtime.GOOS != "windows" {
		return ""
	}
	return filepath.Join(l.Root, "webview")
}
