//go:build !windows

package app

import "runtime"

// showFatal shows the error with osascript on macOS and zenity on Linux; it
// fails (and ReportFatal falls back to stderr) when neither is available.
func showFatal(title, text string) error {
	if runtime.GOOS == "darwin" {
		return runDialog(osascriptCommand(title, text))
	}
	return runDialog(zenityCommand(title, text))
}
