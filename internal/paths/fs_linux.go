//go:build linux

package paths

import (
	"os"
	"path/filepath"
	"strings"
)

// networkFSType finds the longest /proc/mounts mount point containing dir and
// checks its filesystem type.
func networkFSType(dir string) bool {
	b, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	dir = filepath.Clean(dir)
	best, bestType := "", ""
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		mp := strings.ReplaceAll(f[1], "\\040", " ")
		if (dir == mp || strings.HasPrefix(dir, strings.TrimSuffix(mp, "/")+"/")) && len(mp) >= len(best) {
			best, bestType = mp, f[2]
		}
	}
	return hasAny(bestType, "nfs", "nfs4", "cifs", "smb3", "fuse.sshfs")
}
