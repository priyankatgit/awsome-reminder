//go:build !windows && !(darwin && cgo)

package app

// Linux (and a darwin build without cgo): the count is on the tray icon
// only. The Unity LauncherEntry count is deferred (the spec allows "tray
// only").

func setAppUserModelID() {}

// newTaskbar returns a taskbar that shows nothing.
func newTaskbar(func()) taskbar { return noTaskbar{} }

type noTaskbar struct{}

func (noTaskbar) SetCount(int) {}
func (noTaskbar) Close()       {}
