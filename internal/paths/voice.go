package paths

import (
	"os"
	"path/filepath"
)

// LegacyVoiceDir returns <UserCacheDir>/awesome-reminder/voice without
// creating it. It holds the downloaded speech engine and model (about 160 MB),
// so it lives in the local cache (%LOCALAPPDATA% on Windows), not in the
// roaming config folder.
func LegacyVoiceDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appDirName, "voice"), nil
}

// VoiceDir is LegacyVoiceDir, creating the directory if missing.
func VoiceDir() (string, error) {
	dir, err := LegacyVoiceDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
