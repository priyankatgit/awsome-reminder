package datamove

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"awesome-reminder/internal/paths"
)

var ownedFiles = []string{
	"awesome.db", "awesome.db-wal", "awesome.db-shm",
	"awesome.db.moved", "awesome.db-wal.moved", "awesome.db-shm.moved",
	"sync.key", "awesome.log", "setup.log",
}

func legacyExe() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	if a := os.Getenv("APPDATA"); a != "" {
		return filepath.Join(a, "awesome-reminder.exe")
	}
	return ""
}

// OldCopySize is the size of what RemoveOldCopy would delete from dir.
func OldCopySize(dir string) int64 {
	if dir == "" {
		return 0
	}
	var n int64
	for _, f := range ownedFiles {
		n += fileSize(filepath.Join(dir, f))
	}
	a, _ := dirSize(filepath.Join(dir, "attachments"))
	return n + a
}

// RemoveOldCopy deletes the files Awesome Reminder left in the previous data
// folder and clears the pointer's previousDir. Anything else in the folder is
// kept.
func RemoveOldCopy(loc paths.Location) error {
	prev := loc.PreviousDir
	if prev == "" {
		return errors.New("There is no old copy to remove.")
	}
	if paths.SameDir(prev, loc.DataDir) {
		return errors.New("The old copy is the data folder in use, so it can't be removed.")
	}
	for _, f := range ownedFiles {
		if err := os.Remove(filepath.Join(prev, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.RemoveAll(filepath.Join(prev, "attachments")); err != nil {
		return err
	}
	if exe := legacyExe(); exe != "" {
		if err := os.Remove(exe); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	cfg, _ := paths.ConfigDir()
	if entries, err := os.ReadDir(prev); err == nil && len(entries) == 0 && (cfg == "" || !paths.SameDir(prev, cfg)) {
		_ = os.Remove(prev)
	}
	cur, found, err := paths.LoadLocation()
	if err != nil {
		return err
	}
	if !found {
		cur = loc
	}
	cur.PreviousDir = ""
	return paths.SaveLocation(cur)
}
