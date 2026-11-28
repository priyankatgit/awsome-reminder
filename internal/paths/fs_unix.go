//go:build !windows

package paths

import (
	"strings"

	"golang.org/x/sys/unix"
)

func freeSpace(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

func isNetwork(dir string) bool {
	return networkFSType(nearestExisting(dir))
}

func hasAny(s string, set ...string) bool {
	for _, v := range set {
		if s == v {
			return true
		}
	}
	return false
}

func underVolumes(dir string) bool {
	return dir == "/Volumes" || strings.HasPrefix(dir, "/Volumes/")
}
