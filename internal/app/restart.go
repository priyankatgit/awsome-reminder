package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

const afterSetupFlag = "--after-setup"

const (
	parentWaitTimeout = 10 * time.Second
	parentPollEvery   = 100 * time.Millisecond
)

var restartRequested atomic.Bool

// RequestRestart asks main to relaunch the app once the current run ends.
func RequestRestart() { restartRequested.Store(true) }

// RestartRequested reports whether RequestRestart was called.
func RestartRequested() bool { return restartRequested.Load() }

// Relaunch starts a detached copy of this executable that waits for this
// process to exit before it starts (see WaitForParent). It does not wait for
// the child.
func Relaunch() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, afterSetupFlag, strconv.Itoa(os.Getpid()))
	cmd.Dir = filepath.Dir(exe)
	detach(cmd)
	return cmd.Start()
}

// WaitForParent handles "--after-setup <pid>": it waits up to 10 s for that
// process to exit (so the single-instance lock is free) and returns args
// without those two tokens. Other args are returned unchanged.
func WaitForParent(args []string) []string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] != afterSetupFlag {
			continue
		}
		if pid, err := strconv.Atoi(args[i+1]); err == nil {
			waitForExit(pid, parentWaitTimeout)
		}
		out := make([]string, 0, len(args)-2)
		out = append(out, args[:i]...)
		return append(out, args[i+2:]...)
	}
	return args
}
