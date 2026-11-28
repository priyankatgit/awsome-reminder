package paths

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DBFile is the database file name inside a data folder.
const DBFile = "awesome.db"

const pointerFile = "location.json"

// ConfigDir returns <UserConfigDir>/awesome-reminder without creating it.
func ConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appDirName), nil
}

// StandardRoot is the default data folder; it equals ConfigDir.
func StandardRoot() (string, error) { return ConfigDir() }

// Location is the pointer file that names the chosen data folder.
type Location struct {
	Version     int    `json:"version"`
	DataDir     string `json:"dataDir"`
	PreviousDir string `json:"previousDir,omitempty"`
	PendingMove string `json:"pendingMove,omitempty"`
}

// PointerPath returns the path of location.json inside the config dir.
func PointerPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, pointerFile), nil
}

// LoadLocation reads the pointer. found is false (with a nil error) when the
// file does not exist; an unreadable, invalid or unsupported file is an error.
func LoadLocation() (loc Location, found bool, err error) {
	p, err := PointerPath()
	if err != nil {
		return Location{}, false, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Location{}, false, nil
	}
	if err != nil {
		return Location{}, false, err
	}
	if err := json.Unmarshal(b, &loc); err != nil {
		return Location{}, false, fmt.Errorf("invalid %s: %w", pointerFile, err)
	}
	if loc.Version != 1 {
		return Location{}, false, fmt.Errorf("unsupported %s version %d", pointerFile, loc.Version)
	}
	return loc, true, nil
}

// SaveLocation writes the pointer atomically (temp file, fsync, rename).
func SaveLocation(loc Location) error {
	loc.Version = 1
	p, err := PointerPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(loc, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
