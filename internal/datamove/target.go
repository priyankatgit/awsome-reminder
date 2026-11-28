// Package datamove checks, moves, adopts and cleans up the user's data folder.
package datamove

import (
	"errors"
	"os"
	"path/filepath"

	"awesome-reminder/internal/paths"
)

// SubfolderName is the folder created inside a picked folder that already
// holds other files.
const SubfolderName = "Awesome Reminder"

// SpaceMargin is the free space required on top of the data being copied.
const SpaceMargin = 50 << 20

var errNotEmpty = errors.New("That folder isn't empty. Choose an empty folder or one with Awesome Reminder data.")

// usable reports whether dir can be used as a data folder as is: missing,
// empty, or already holding a database.
func usable(dir string) (bool, error) {
	fi, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !fi.IsDir() {
		return false, errNotEmpty
	}
	if _, err := os.Stat(filepath.Join(dir, paths.DBFile)); err == nil {
		return true, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

// Target turns the folder the user picked into the data folder to use: the
// folder itself when it is missing, empty or holds a database, otherwise an
// "Awesome Reminder" subfolder under the same rule.
func Target(picked string) (string, error) {
	ok, err := usable(picked)
	if err != nil {
		return "", err
	}
	if ok {
		return picked, nil
	}
	sub := filepath.Join(picked, SubfolderName)
	ok, err = usable(sub)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errNotEmpty
	}
	return sub, nil
}

// mkdirTracked creates dir and any missing parents and returns the ones it
// created, outermost first.
func mkdirTracked(dir string) ([]string, error) {
	var missing []string
	for d := dir; ; {
		_, err := os.Stat(d)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		missing = append(missing, d)
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		// Remove whatever part was created.
		for _, d := range missing {
			_ = os.Remove(d)
		}
		return nil, err
	}
	for i, j := 0, len(missing)-1; i < j; i, j = i+1, j-1 {
		missing[i], missing[j] = missing[j], missing[i]
	}
	return missing, nil
}
