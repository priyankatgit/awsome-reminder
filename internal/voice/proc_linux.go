package voice

import (
	"os"
	"os/exec"
	"syscall"
)

// hideAndContain makes the kernel kill the engine if the app dies.
func hideAndContain(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}

func afterStart(*os.Process) (release func()) { return func() {} }
