//go:build darwin || linux

package autostart

import (
	"os"
	"testing"
)

func runtimeIsUnix() bool { return true }

func setupTest(t *testing.T) {
	old := baseDir
	baseDir = t.TempDir()
	t.Cleanup(func() { baseDir = old })
}

func readEntry(t *testing.T) string {
	b, err := os.ReadFile(entryPath())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
