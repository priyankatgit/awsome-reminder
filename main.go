package main

import (
	"embed"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	_ "time/tzdata" // IANA zones for items' TZ on systems without a zoneinfo database

	"awesome-reminder/internal/app"
	"awesome-reminder/internal/paths"
	"awesome-reminder/internal/wakehelper"
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
	// Wake word helper mode (Windows): the app's exe started by its own
	// supervisor. It must return before any Wails, single-instance, logging
	// or database setup.
	if len(os.Args) > 1 && os.Args[1] == "--wake-helper" {
		os.Exit(wakehelper.Run(os.Args[2:]))
	}
	args := app.WaitForParent(os.Args[1:])
	res := paths.Resolve()
	if res.State != paths.StateReady {
		cfg, _ := paths.ConfigDir()
		logger := setupLogging(filepath.Join(cfg, "setup.log"), true)
		if err := app.RunSetup(app.SetupOptions{Assets: assets, Logger: logger, Resolution: res}); err != nil {
			app.ReportFatal(err, fatalDir(res))
			os.Exit(1)
		}
		if app.RestartRequested() {
			if err := app.Relaunch(); err != nil {
				log.Printf("relaunch: %v", err)
			}
		}
		return
	}
	lay, err := paths.NewLayout(res.Root)
	if err != nil {
		app.ReportFatal(err, res.Root)
		os.Exit(1)
	}
	// The folder is known to exist (Resolve checked), so the log can open.
	logger := setupLogging(lay.Log(), false)
	if err := app.Run(app.Options{Assets: assets, TrayIconLight: trayIconLight, TrayIconDark: trayIconDark, TrayIconTemplate: trayIconTemplate, Logger: logger, DataDir: res.Root, Args: args}); err != nil {
		// A tray app has no window to fail in: tell the user why it isn't
		// running (message box with the error and the data folder).
		app.ReportFatal(err, res.Root)
		os.Exit(1)
	}
	if app.RestartRequested() {
		if err := app.Relaunch(); err != nil {
			log.Printf("relaunch: %v", err)
		}
	}
}

// fatalDir is the folder named in a fatal dialog when setup fails: the
// resolved root, else the standard one, else unknown.
func fatalDir(res paths.Resolution) string {
	if res.Root != "" {
		return res.Root
	}
	if d, err := paths.StandardRoot(); err == nil {
		return d
	}
	return ""
}

// setupLogging sends the standard logger and Wails' logger to path (truncated
// when truncate is set, appended otherwise). Falls back to stderr if the log
// file cannot be opened.
func setupLogging(path string, truncate bool) *slog.Logger {
	var w io.Writer = os.Stderr
	flags := os.O_CREATE | os.O_WRONLY
	if truncate {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		if f, err := os.OpenFile(path, flags, 0o644); err == nil {
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
