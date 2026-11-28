//go:build !windows && !darwin && !linux

package paths

func networkFSType(string) bool { return false }
