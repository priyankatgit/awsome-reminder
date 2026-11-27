package voice

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// runtimeDLLs are the MSVC runtime and OpenMP libraries the official
// Windows engine links but does not ship.
var runtimeDLLs = []string{"msvcp140.dll", "vcruntime140.dll", "vcruntime140_1.dll", "vcomp140.dll"}

// runtimeMissing lists the runtime DLLs not found in System32.
func runtimeMissing() []string {
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		return nil
	}
	var missing []string
	for _, d := range runtimeDLLs {
		if _, err := os.Stat(filepath.Join(sys, d)); err != nil {
			missing = append(missing, d)
		}
	}
	return missing
}
