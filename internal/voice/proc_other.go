//go:build !windows && !linux

package voice

import (
	"os"
	"os/exec"
)

// On macOS the engine is stopped by the idle timer and on quit; there is no
// parent-death signal to rely on.
func hideAndContain(*exec.Cmd) {}

func afterStart(*os.Process) (release func()) { return func() {} }
