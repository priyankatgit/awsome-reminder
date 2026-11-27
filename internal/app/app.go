package app

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"slices"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"awesome-reminder/internal/autostart"
	"awesome-reminder/internal/core"
	"awesome-reminder/internal/export"
	"awesome-reminder/internal/hotkey"
	"awesome-reminder/internal/paths"
	"awesome-reminder/internal/scheduler"
	"awesome-reminder/internal/store"
)

const (
	appName        = "Awesome Reminder"
	captureFlag    = "--capture"
	listFlag       = "--list"
	exportDebounce = time.Second
)

// Options configure Run.
type Options struct {
	Assets   fs.FS  // built frontend (frontend/dist)
	TrayIcon []byte // PNG
	Logger   *slog.Logger
}

// Run opens the store, wires core, scheduler, exporter and hotkey to the
// Wails application and blocks until the app quits.
func Run(opts Options) error {
	dbPath, err := paths.DBPath()
	if err != nil {
		return fmt.Errorf("db path: %w", err)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	c := core.New(st, time.Now, time.Local)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		win  *wailsWindows
		tray *application.SystemTray
		svc  *Service
	)
	app := application.New(application.Options{
		Name:        appName,
		Description: "Hotkey quick-capture to-dos and reminders",
		Logger:      opts.Logger,
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(opts.Assets)},
		Mac: application.MacOptions{
			// Tray-only app: keep running with no windows.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		Windows: application.WindowsOptions{DisableQuitOnLastWindowClosed: true},
		Linux:   application.LinuxOptions{DisableQuitOnLastWindowClosed: true},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "dev.awesome-reminder.app",
			OnSecondInstanceLaunch: func(d application.SecondInstanceData) {
				if slices.Contains(d.Args, captureFlag) {
					log.Printf("second instance: %s", captureFlag)
					go win.ShowCapture()
					return
				}
				// --list, or a plain second launch (e.g. the Start menu entry).
				log.Printf("second instance: open list")
				go win.OpenList("")
			},
		},
	})
	hotkey.SetMainThreadRunner(application.InvokeSync)

	emit := func(name string, data ...any) { app.Event.Emit(name, data...) }
	sched := scheduler.New(st, func() []time.Duration {
		s, err := c.Settings()
		if err != nil {
			return nil
		}
		return s.LeadTimes
	}, func(alerts []scheduler.Alert) {
		// Runs inline in the scheduler loop: never block it.
		for _, a := range alerts {
			log.Printf("alert: %s %q due %s missed=%v", a.Key, a.Text, a.DueAt.Local().Format(time.DateTime), a.Missed)
		}
		// TODO(task-11): show the alert window.
		go emit(EventAlertsShow, alerts)
	}, time.Now)
	exp := export.NewExporter(func() string {
		s, err := c.Settings()
		if err != nil {
			return ""
		}
		return s.ExportPath
	}, c.View, time.Local, exportDebounce)
	defer exp.Close()

	win = newWindows(app)
	hk := hotkey.NewManager(hotkey.Platform(), win.ShowCapture)
	defer hk.Close()

	svc = NewService(Deps{
		Core:    c,
		Windows: win,
		Hotkey:  hk,
		Autostart: func(on bool) error {
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("autostart: %w", err)
			}
			return autostart.Set(on, exe)
		},
		Export:        exp.Trigger,
		StatusChanged: func() { tray.SetTooltip(tooltip(svc.Status())) },
	})
	app.RegisterService(application.NewService(svc))

	c.OnChange(func() {
		sched.Kick()
		exp.Trigger()
		emit(EventItemsChanged)
	})

	tray = app.SystemTray.New()
	tray.SetIcon(opts.TrayIcon)
	tray.SetTooltip(appName)
	tray.SetMenu(trayMenu(app, win))
	tray.OnClick(func() { go win.OpenList("") })

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		s, err := c.Settings()
		if err != nil {
			log.Printf("settings: %v", err)
		} else {
			if err := hk.Set(s.Hotkey); err != nil {
				log.Printf("hotkey %s: %v", s.Hotkey, err)
			}
			syncAutostart(s.Autostart)
		}
		tray.SetTooltip(tooltip(svc.Status()))
		sched.Start(ctx)
		switch args := os.Args[1:]; {
		case slices.Contains(args, captureFlag):
			go win.ShowCapture()
		case slices.Contains(args, listFlag):
			go win.OpenList("")
		}
	})

	return app.Run()
}

func trayMenu(app *application.App, win *wailsWindows) *application.Menu {
	menu := app.NewMenu()
	menu.Add("Open List").OnClick(func(*application.Context) { go win.OpenList("") })
	menu.Add("Settings").OnClick(func(*application.Context) { go win.OpenSettings() })
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) { app.Quit() })
	return menu
}

func tooltip(s Status) string {
	if s.HotkeyErr != "" {
		return appName + " — hotkey error: " + s.HotkeyErr
	}
	return appName
}
