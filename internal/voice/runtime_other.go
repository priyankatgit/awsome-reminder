//go:build !windows

package voice

// runtimeMissing: the Linux engine's libraries ship in its archive; system
// engines come with their own.
func runtimeMissing() []string { return nil }
