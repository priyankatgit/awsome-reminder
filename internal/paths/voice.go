package paths

import (
	"os"
	"path/filepath"
)

// VoiceDir returns <UserCacheDir>/awesome-reminder/voice, creating it if
// missing. It holds the downloaded speech engine and model (about 160 MB),
// so it lives in the local cache (%LOCALAPPDATA% on Windows), not in the
// roaming config folder.
func VoiceDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, appDirName, "voice")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
