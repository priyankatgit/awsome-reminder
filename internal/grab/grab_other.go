//go:build !windows && !linux && !(darwin && cgo)

package grab

import "context"

// Grab has no selection grab on this platform: it returns an empty Result
// and never touches the clipboard.
func Grab(ctx context.Context, keys []Key) Result {
	return Result{}
}

// AccessibilityTrusted is always true here: there is no permission to ask
// for.
func AccessibilityTrusted() bool { return true }
