// Package paths resolves on-disk locations used by the app.
package paths

import (
	"os"
	"path/filepath"
)

const appDirName = "awesome-reminder"

// Dir returns <UserConfigDir>/awesome-reminder without creating it.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appDirName), nil
}

// DataDir returns <UserConfigDir>/awesome-reminder, creating it if missing.
func DataDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// DBPath returns the path of the SQLite database file inside DataDir.
func DBPath() (string, error) { return inDataDir("awesome.db") }

// LogPath returns the path of the log file inside DataDir.
func LogPath() (string, error) { return inDataDir("awesome.log") }

func inDataDir(name string) (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}
