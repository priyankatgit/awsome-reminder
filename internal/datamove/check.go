package datamove

import (
	"fmt"
	"os"
	"path/filepath"

	"awesome-reminder/internal/paths"
)

// Sizes are the bytes a move has to copy.
type Sizes struct{ DB, Attachments, Voice int64 }

// Total is the sum of all parts.
func (s Sizes) Total() int64 { return s.DB + s.Attachments + s.Voice }

func fileSize(p string) int64 {
	if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
		return fi.Size()
	}
	return 0
}

func dirSize(dir string) (int64, error) {
	var n int64
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n, err
}

// Measure sizes the data in a layout. Missing folders count as zero.
func Measure(from paths.Layout) (Sizes, error) {
	var s Sizes
	s.DB = fileSize(from.DB()) + fileSize(from.DB()+"-wal")
	var err error
	if s.Attachments, err = dirSize(from.Attachments()); err != nil {
		return s, err
	}
	if s.Voice, err = dirSize(from.Voice()); err != nil {
		return s, err
	}
	return s, nil
}

// FormatBytes renders a size for people: "84 MB", "1.2 GB".
func FormatBytes(n int64) string {
	const kb, mb, gb = 1 << 10, 1 << 20, 1 << 30
	switch {
	case n >= gb:
		return fmt.Sprintf("%.1f GB", float64(n)/gb)
	case n >= mb:
		return fmt.Sprintf("%d MB", (n+mb/2)/mb)
	case n >= kb:
		return fmt.Sprintf("%d KB", (n+kb/2)/kb)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// Check is what the Settings screen shows about a picked folder.
type Check struct {
	Path    string `json:"path"`
	Free    uint64 `json:"free"`
	Need    int64  `json:"need"`
	HasData bool   `json:"hasData"`
	Network bool   `json:"network"`
	Cloud   bool   `json:"cloud"`
	Same    bool   `json:"same"`
	Err     string `json:"err"`
}

const outsideMsg = "Choose a folder outside the current data folder."

// CheckTarget inspects the folder the user picked. from is the current data
// folder, nil on a fresh install. It leaves no folders behind.
func CheckTarget(picked string, from *paths.Layout) Check {
	c := Check{Path: picked, Need: SpaceMargin}
	target, err := Target(picked)
	if err != nil {
		c.Err = err.Error()
		return c
	}
	c.Path = target
	c.Network = paths.IsNetwork(target)
	c.Cloud = paths.IsCloudSynced(target)
	if _, err := os.Stat(filepath.Join(target, paths.DBFile)); err == nil {
		c.HasData = true
	}
	if from != nil {
		if sz, err := Measure(*from); err == nil {
			c.Need = sz.Total() + SpaceMargin
		}
		if paths.SameDir(target, from.Root) {
			c.Same = true
			if free, err := paths.FreeSpace(target); err == nil {
				c.Free = free
			}
			return c
		}
		if paths.Within(target, from.Root) || paths.Within(from.Root, target) ||
			paths.Within(target, from.Voice()) {
			c.Err = outsideMsg
			return c
		}
	}
	if err := probeWritable(target); err != nil {
		c.Err = "Can't write to this folder."
		return c
	}
	free, err := paths.FreeSpace(target)
	if err == nil {
		c.Free = free
		if !c.HasData && int64(free) < c.Need {
			c.Err = fmt.Sprintf("Not enough space: needs %s, %s free.", FormatBytes(c.Need), FormatBytes(int64(free)))
		}
	}
	return c
}

// probeWritable creates and deletes a file in dir, removing any folders it had
// to create so a check leaves nothing behind.
func probeWritable(dir string) error {
	created, err := mkdirTracked(dir)
	if err != nil {
		return err
	}
	defer func() {
		for i := len(created) - 1; i >= 0; i-- {
			_ = os.Remove(created[i])
		}
	}()
	p := filepath.Join(dir, ".awesome-probe")
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(p)
}
