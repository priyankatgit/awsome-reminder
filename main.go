package main

import (
	"embed"
	"io"
	"log"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

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

	app := application.New(application.Options{
		Name:        "Awesome Reminder",
		Description: "Hotkey quick-capture to-dos and reminders",
		Logger:      logger,
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			// Tray-only app: keep running with no windows.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
		Linux: application.LinuxOptions{
			DisableQuitOnLastWindowClosed: true,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "dev.awesome-reminder.app",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				// No-op for now; later tasks will surface the list window.
			},
		},
	})

	menu := app.NewMenu()
	menu.Add("Open List").OnClick(func(*application.Context) {})
	menu.Add("Settings").OnClick(func(*application.Context) {})
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) { app.Quit() })

	tray := app.SystemTray.New()
	tray.SetIcon(trayIcon)
	tray.SetTooltip("Awesome Reminder")
	tray.SetMenu(menu)

	if err := app.Run(); err != nil {
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
