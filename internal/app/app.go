package app

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"awesome-reminder/internal/autostart"
	"awesome-reminder/internal/cloudsync"
	"awesome-reminder/internal/cloudsync/atlas"
	"awesome-reminder/internal/core"
	"awesome-reminder/internal/export"
	"awesome-reminder/internal/hotkey"
	"awesome-reminder/internal/paths"
	"awesome-reminder/internal/scheduler"
	"awesome-reminder/internal/secret"
	"awesome-reminder/internal/store"
)

const (
	appName        = "Awesome Reminder"
	captureFlag    = "--capture"
	listFlag       = "--list"
	settingsFlag   = "--settings"
	exportDebounce = time.Second
)

// Options configure Run.
type Options struct {
	Assets fs.FS // built frontend (frontend/dist)
	// Tray icons (PNG). Light is the dark glyph for light taskbars, Dark is
	// the white glyph for dark ones, Template the black-on-transparent macOS
	// template image.
	TrayIconLight, TrayIconDark, TrayIconTemplate []byte
	Logger                                        *slog.Logger
}

// setTrayIcons picks the tray image for the platform: a template image on
// macOS, a light/dark pair on Windows, and the white glyph on Linux (panels
// are dark there and the status-notifier API has no dark-mode variant).
func setTrayIcons(tray *application.SystemTray, o Options) {
	switch runtime.GOOS {
	case "darwin":
		tray.SetTemplateIcon(o.TrayIconTemplate)
	case "windows":
		tray.SetIcon(o.TrayIconLight).SetDarkModeIcon(o.TrayIconDark)
	default:
		tray.SetIcon(o.TrayIconDark)
	}
}

// Run opens the store, wires core, scheduler, exporter and hotkey to the
// Wails application and blocks until the app quits.
func Run(opts Options) error {
	setAppUserModelID() // before any window exists (Windows taskbar button)
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
	c.SetAttachmentDir(filepath.Join(filepath.Dir(dbPath), "attachments")) // DBPath lives in DataDir
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		win    *wailsWindows
		tray   *application.SystemTray
		svc    *Service
		badges *badgeWiring
		// Second-instance requests can arrive before win is assigned below;
		// they are held until ApplicationStarted.
		launches launchQueue
	)
	app := application.New(application.Options{
		Name:        appName,
		Description: "Hotkey quick-capture to-dos and reminders",
		Logger:      opts.Logger,
		Assets: application.AssetOptions{
			Handler:    application.AssetFileServerFS(opts.Assets),
			Middleware: attMiddleware(c),
		},
		Mac: application.MacOptions{
			// Tray-only app: keep running with no windows.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
			// WebView2 memory (Task 12b, measured on the idle app with the
			// capture window pre-created): --disable-gpu replaces the GPU
			// process's hardware compositor with software compositing
			// (64 -> 12 MB private), and --in-process-gpu folds what is left
			// into the browser process (one process fewer, about 8 MB less).
			// The windows are small and mostly static, rendering looked the
			// same, and the Acrylic backdrop is drawn by DWM, not Chromium.
			AdditionalBrowserArgs: []string{"--disable-gpu", "--in-process-gpu"},
		},
		Linux: application.LinuxOptions{DisableQuitOnLastWindowClosed: true},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "dev.awesome-reminder.app",
			OnSecondInstanceLaunch: func(d application.SecondInstanceData) {
				if slices.Contains(d.Args, captureFlag) {
					log.Printf("second instance: %s", captureFlag)
					launches.Do(func() { win.ShowCapture() })
					return
				}
				if slices.Contains(d.Args, settingsFlag) {
					log.Printf("second instance: %s", settingsFlag)
					launches.Do(func() { win.OpenSettings() })
					return
				}
				// --list, or a plain second launch (e.g. the Start menu entry).
				log.Printf("second instance: open list")
				launches.Do(func() { win.OpenList("") })
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
		// Runs inline in the scheduler loop: never block it. The scheduler
		// only fires after Start (ApplicationStarted), when svc is set.
		for _, a := range alerts {
			log.Printf("alert: %s %q due %s missed=%v", a.Key, a.Text, a.DueAt.Local().Format(time.DateTime), a.Missed)
		}
		go svc.showAlerts(alerts)
	}, time.Now)
	exp := export.NewExporter(func() string {
		s, err := c.Settings()
		if err != nil {
			return ""
		}
		// Settings.Validate rejects a relative path; one stored before that
		// check would land in the working directory (System32 at login).
		if s.ExportPath != "" && !filepath.IsAbs(s.ExportPath) {
			log.Printf("export: ignoring relative path %q", s.ExportPath)
			return ""
		}
		return s.ExportPath
	}, c.View, time.Local, exportDebounce)
	defer exp.Close() // writes a pending export; runs before st.Close
	// Sync opens no Mongo client unless a connection string is saved, and
	// never at the caller's pace: Start returns at once.
	syncCtl := newSyncController(syncDeps{
		Core:     c,
		Secret:   secret.New(filepath.Dir(dbPath), st),
		Open:     atlasOpen,
		Host:     atlas.Host,
		Classify: atlasClassify,
		OnStatus: func(s cloudsync.Status) {
			emit(EventSyncStatus, s)
			badges.SyncChanged() // tray dot and tooltip; nil-safe before badges exists
		},
		// Off the engine's goroutine, like showAlerts: the alert window
		// calls must not hold up a cycle that shutdown waits for. It does
		// not touch the store.
		OnApplied: func(a []core.Applied) { go svc.remoteApplied(a) },
	})
	// On the way out, stop the scheduler and wait for a plan in flight, so it
	// never reaches a closed store and its last change still reaches the
	// export (deferred after exp.Close and st.Close, so it runs before them).
	// Any other goroutine that reads the store (e.g. a debounced badge
	// refresh) must be stopped by cancel() and joined here too.
	defer func() {
		cancel()
		sched.Wait()
		syncCtl.Stop() // joins the opener and the engine's cycle in flight
		badges.Stop()  // joins a refresh in flight, closes the taskbar window
	}()

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
		StatusChanged: func() { badges.StatusChanged() },
		Emit:          func(name string, data any) { emit(name, data) },
		OpenURL:       app.Browser.OpenURL,
		Sync:          syncCtl,
	})
	app.RegisterService(application.NewService(svc))
	// Recurring reminders not marked done move on to their latest occurrence
	// before each plan (ruling R15); the scheduler wakes at the next one.
	sched.SetBeforePlan(svc.advanceRecurring)
	registerVoice(app)
	win.onAlertsClosed = svc.alertsClosed
	// A settings window closed while recording a hotkey must not leave the
	// global hotkey released.
	win.onSettingsClosed = func() { svc.SetHotkeyRecording(false) }

	c.OnChange(func() {
		sched.Kick()
		exp.Trigger()
		emit(EventItemsChanged)
		badges.Changed() // also on a settings save
		syncCtl.Kick()
	})

	tray = app.SystemTray.New()
	setTrayIcons(tray, opts)
	tray.SetTooltip(appName)
	tray.SetMenu(trayMenu(app, win))
	tray.OnClick(func() { go win.OpenList("") })
	badges = newBadgeWiring(badgeDeps{
		Count: c.PendingCount,
		Show: func() bool {
			s, err := c.Settings()
			return err != nil || s.ShowPendingCount
		},
		HotkeyErr: func() string { return svc.Status().HotkeyErr },
		Icons:     trayIcons{opts.TrayIconLight, opts.TrayIconDark, opts.TrayIconTemplate},
		Tray:      wailsBadgeTray{app, tray},
		Debounce:  badgeDebounce,
		Sync:      syncCtl.Status,
	})

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
		badges.Start(newTaskbar(func() {
			if app.Context().Err() != nil {
				return // shutting down: the windows are being torn down
			}
			launches.Do(win.Activate)
		}))
		sweepAttachments(c)
		sched.Start(ctx)
		syncCtl.Start(ctx)
		// Bring the export up to date with the database at startup (it may
		// be missing, or older than changes made by an earlier run).
		exp.Trigger()
		launches.Ready()
		switch args := os.Args[1:]; {
		case slices.Contains(args, captureFlag):
			go win.ShowCapture()
		case slices.Contains(args, listFlag):
			go win.OpenList("")
		case slices.Contains(args, settingsFlag):
			go win.OpenSettings()
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
