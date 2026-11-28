package app

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"awesome-reminder/internal/paths"
)

// Setup window geometry in DIPs.
const (
	setupWidth      = 560
	setupHeight     = 560
	setupMinWidth   = 480
	setupMinHeight  = 480
	setupWindowName = "setup"
)

// SetupOptions configure RunSetup.
type SetupOptions struct {
	Assets     fs.FS
	Logger     *slog.Logger
	Resolution paths.Resolution
}

// setupWebviewDir is the setup window's WebView2 user data folder.
func setupWebviewDir() string {
	return filepath.Join(os.TempDir(), "awesome-reminder-setup-webview")
}

// setupAppOptions are the setup app's options; onSecond runs when the app is
// launched again while setup is open.
func setupAppOptions(o SetupOptions, onSecond func()) application.Options {
	return application.Options{
		Name:        appName,
		Description: "Hotkey quick-capture to-dos and reminders",
		Logger:      o.Logger,
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(o.Assets)},
		// Closing the setup window ends the process on every OS, so it never
		// lingers windowless holding the single-instance lock.
		Mac: application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: false,
			// Its own WebView2 profile in the temp folder: setup never creates
			// %APPDATA%\awesome-reminder.exe for a data folder elsewhere and
			// never shares a live profile with the main app.
			WebviewUserDataPath:   setupWebviewDir(),
			AdditionalBrowserArgs: webviewBrowserArgs(),
		},
		Linux: application.LinuxOptions{DisableQuitOnLastWindowClosed: false},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               "dev.awesome-reminder.app",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { onSecond() },
		},
	}
}

// RunSetup shows the data folder setup window and returns when it closes.
func RunSetup(o SetupOptions) error {
	var win *application.WebviewWindow
	app := application.New(setupAppOptions(o, func() {
		if win != nil {
			win.Show()
			win.Focus()
		}
	}))
	svc := newSetupService(o.Resolution, app.Quit, func(name string, data any) { app.Event.Emit(name, data) })
	app.RegisterService(application.NewService(svc))
	bg := listBgLight
	if app.Env.IsDarkMode() {
		bg = listBgDark
	}
	win = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             setupWindowName,
		Title:            appName,
		Width:            setupWidth,
		Height:           setupHeight,
		MinWidth:         setupMinWidth,
		MinHeight:        setupMinHeight,
		URL:              "/#setup",
		InitialPosition:  application.WindowCentered,
		BackgroundColour: bg,
	})
	// Closing the window mid-move rolls the copy back before the app exits.
	win.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) { svc.stopMove() })
	return app.Run()
}
