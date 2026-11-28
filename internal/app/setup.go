package app

import (
	"io/fs"
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"

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

// RunSetup shows the data folder setup window and returns when it closes.
func RunSetup(o SetupOptions) error {
	var win *application.WebviewWindow
	app := application.New(application.Options{
		Name:        appName,
		Description: "Hotkey quick-capture to-dos and reminders",
		Logger:      o.Logger,
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(o.Assets)},
		Windows:     application.WindowsOptions{DisableQuitOnLastWindowClosed: false},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "dev.awesome-reminder.app",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if win != nil {
					win.Show()
					win.Focus()
				}
			},
		},
	})
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
	return app.Run()
}
