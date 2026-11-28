//go:build windows

package paths

import (
	"strings"

	"golang.org/x/sys/windows"
)

func freeSpace(dir string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &free); err != nil {
		return 0, err
	}
	return avail, nil
}

func isNetwork(dir string) bool {
	dir = strings.ReplaceAll(dir, "/", `\`)
	switch {
	case strings.HasPrefix(strings.ToUpper(dir), `\\?\UNC\`):
		return true
	case strings.HasPrefix(dir, `\\?\`) || strings.HasPrefix(dir, `\\.\`):
		dir = dir[4:]
	case strings.HasPrefix(dir, `\\`):
		return true
	}
	if len(dir) < 2 || dir[1] != ':' {
		return false
	}
	p, err := windows.UTF16PtrFromString(dir[:2] + `\`)
	if err != nil {
		return false
	}
	return windows.GetDriveType(p) == windows.DRIVE_REMOTE
}
