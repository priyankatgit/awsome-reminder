// Package autostart registers the app to launch at user login.
//
// Set(enabled, exePath) adds or removes the OS-specific entry; Set(false)
// is a no-op when no entry exists. Enabled reports whether an entry is
// present, and Target returns the executable path it starts ("" if none).
package autostart
