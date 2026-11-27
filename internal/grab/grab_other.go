//go:build !windows && !linux && !(darwin && cgo)

package grab

import "context"

// Grab has no selection grab on this platform: it returns an empty Result
// and never touches the clipboard.
func Grab(ctx context.Context, mods []Key) Result {
	return Result{}
}
