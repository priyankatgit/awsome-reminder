package app

import (
	"os/exec"
	"runtime"
)

// openFolder shows dir in the system file manager.
func openFolder(dir string) error {
	name := "xdg-open"
	switch runtime.GOOS {
	case "windows":
		name = "explorer.exe"
	case "darwin":
		name = "open"
	}
	return exec.Command(name, dir).Start()
}
