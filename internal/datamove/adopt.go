package datamove

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"awesome-reminder/internal/paths"
	"awesome-reminder/internal/store"
)

func previousOf(prev *paths.Location) string {
	if prev == nil {
		return ""
	}
	return prev.PreviousDir
}

// Adopt switches to a folder that already holds Awesome Reminder data. The
// Atlas connection saved on this computer is kept: it is per computer, not
// per folder.
func Adopt(target string, prev *paths.Location) error {
	db := filepath.Join(target, paths.DBFile)
	if !exists(db) {
		return errors.New("That folder has no Awesome Reminder data.")
	}
	s, err := store.Open(db)
	if err != nil {
		return fmt.Errorf("That folder's data can't be opened: %w", err)
	}
	if err := s.Close(); err != nil {
		return err
	}
	raw, err := openRaw(db)
	if err != nil {
		return err
	}
	err = integrityOK(raw)
	raw.Close()
	if err != nil {
		return err
	}
	return paths.SaveLocation(paths.Location{DataDir: target, PreviousDir: previousOf(prev)})
}

// StartFresh creates an empty database in target and switches to it.
func StartFresh(target string, prev *paths.Location) error {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	s, err := store.Open(filepath.Join(target, paths.DBFile))
	if err != nil {
		return err
	}
	if err := s.Close(); err != nil {
		return err
	}
	return paths.SaveLocation(paths.Location{DataDir: target, PreviousDir: previousOf(prev)})
}

// KeepStandard points the app back at the standard data folder.
func KeepStandard() error {
	root, err := paths.StandardRoot()
	if err != nil {
		return err
	}
	return paths.SaveLocation(paths.Location{DataDir: root})
}

// RequestMove records that the move to target should run at the next start.
// It refuses while an earlier move's old copy is still kept.
func RequestMove(target string) error {
	loc, found, err := paths.LoadLocation()
	if err != nil {
		return err
	}
	if found && oldCopyKept(loc) {
		return errors.New(oldCopyMsg)
	}
	if !found {
		root, err := paths.StandardRoot()
		if err != nil {
			return err
		}
		loc = paths.Location{DataDir: root}
	}
	loc.PendingMove = target
	return paths.SaveLocation(loc)
}
