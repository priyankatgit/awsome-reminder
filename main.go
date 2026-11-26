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

// Tray icon: reuse the template's app icon for now.
//
//go:embed build/appicon.png
var trayIcon []byte

func main() {
	logger := setupLogging()
	if err := app.Run(app.Options{Assets: assets, TrayIcon: trayIcon, Logger: logger}); err != nil {
		log.Fatal(err)
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
	return slog.New(slog.NewTextHandler(w, nil))
}
