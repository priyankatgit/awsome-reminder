package app

import "golang.org/x/sys/windows"

// showFatal shows a modal, topmost error message box (user32 MessageBoxW,
// loaded lazily by golang.org/x/sys/windows; no cgo).
func showFatal(title, text string) error {
	t, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return err
	}
	m, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return err
	}
	const flags = windows.MB_OK | windows.MB_ICONERROR | windows.MB_SETFOREGROUND | windows.MB_TOPMOST
	if _, err := windows.MessageBox(0, m, t, flags); err != nil {
		return err
	}
	return nil
}
