package app

import (
	"fmt"
	"io/fs"
	"log/slog"

	"awesome-reminder/internal/paths"
)

// SetupOptions configure RunSetup.
type SetupOptions struct {
	Assets     fs.FS
	Logger     *slog.Logger
	Resolution paths.Resolution
}

// RunSetup shows the data folder setup window. Stub until the setup UI lands.
func RunSetup(o SetupOptions) error {
	return fmt.Errorf("setup required: %s", o.Resolution.State)
}
