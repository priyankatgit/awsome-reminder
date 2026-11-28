//go:build darwin

package paths

import "golang.org/x/sys/unix"

func networkFSType(dir string) bool {
	if !underVolumes(dir) {
		return false
	}
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return false
	}
	return hasAny(unix.ByteSliceToString(st.Fstypename[:]), "smbfs", "nfs", "afpfs", "webdav")
}
