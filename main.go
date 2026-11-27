package main

import (
	"embed"
	"io"
	"log"
	"log/slog"
	"os"

	_ "time/tzdata" // IANA zones for items' TZ on systems without a zoneinfo database

	"awesome-reminder/internal/app"
	"awesome-reminder/internal/paths"
)

// Embedded frontend assets (built by `wails3 build`).
//
//go:embed all:frontend/dist
var assets embed.FS

// Tray icons (rasterized from build/tray/*.svg).
var (
	//go:embed build/tray/tray-light.png
	trayIconLight []byte
	//go:embed build/tray/tray-dark.png
	trayIconDark []byte
	//go:embed build/tray/tray-template.png
	trayIconTemplate []byte
)

func main() {
	logger := setupLogging()
	if err := app.Run(app.Options{Assets: assets, TrayIconLight: trayIconLight, TrayIconDark: trayIconDark, TrayIconTemplate: trayIconTemplate, Logger: logger}); err != nil {
		// A tray app has no window to fail in: tell the user why it isn't
		// running (message box with the error and the data folder).
		app.ReportFatal(err)
		os.Exit(1)
	}
}

// setupLogging sends the standard logger and Wails' logger to paths.LogPath().
// Falls back to stderr if the log file cannot be opened.
func setupLogging() *slog.Logger {
	var w io.Writer = os.Stderr
	if p, err := paths.LogPath(); err == nil {
		if f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			w = f
		}
	}
	log.SetOutput(w)
	return newLogger(w)
}

// logLevel MUST stay above slog.LevelDebug. At Debug, Wails logs every
// binding call with its arguments ("Binding call complete ... args"), and
// SyncConnect's argument is the Atlas connection string, password included,
// which would then sit in the log file in plain text.
const logLevel = slog.LevelInfo

// newLogger is Wails' logger: text lines at logLevel.
func newLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: logLevel}))
}
