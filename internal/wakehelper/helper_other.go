//go:build !windows

// Package wakehelper is the Windows "Hey Awesome" listener. Linux and macOS
// use sherpa-onnx's own microphone keyword spotter instead.
package wakehelper

import (
	"fmt"
	"os"
)

// Run reports that helper mode exists only on Windows.
func Run(args []string) int {
	fmt.Fprintln(os.Stderr, "wake helper: not supported on this OS")
	return 2
}
