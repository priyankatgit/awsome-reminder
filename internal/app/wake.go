package app

import (
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"

	"awesome-reminder/internal/voice"
)

// Wake word events.
const (
	// EventWakeStatus carries voice.WakeStatus whenever it changes (install
	// progress included).
	EventWakeStatus = "voice:wake-status"
	// EventCaptureWake tells the capture page it was opened by "Hey Awesome"
	// (shown without focus) and should start a wake-mode dictation.
	EventCaptureWake = "capture:wake"
)

func init() {
	application.RegisterEvent[voice.WakeStatus](EventWakeStatus)
	application.RegisterEvent[application.Void](EventCaptureWake)
}

// newWake builds the "Hey Awesome" wake word for dir. A detection opens the
// capture box without focus; while the box is showing (any mode, reply
// included) detections are suppressed. It starts nothing (Wake.Start does).
func newWake(app *application.App, win *wailsWindows, dir string, also func(voice.WakeStatus)) *voice.Wake {
	return voice.NewWake(voice.WakeConfig{
		Dir: dir,
		OnStatus: func(s voice.WakeStatus) {
			app.Event.Emit(EventWakeStatus, s)
			also(s)
		},
		OnWake: func() {
			log.Printf("wake: detected") // the time only: never audio or text
			go win.ShowCaptureForWake()
		},
		// Lock-free (an atomic): it runs on the helper's output reader.
		Suppress: win.CaptureVisible,
	})
}

// WakeStatus reports the wake word's state, settings and install progress.
func (v *VoiceService) WakeStatus() voice.WakeStatus { return v.w.Status() }

// SetWakeEnabled switches the wake word on or off on this device; on needs
// the files installed (InstallWake).
func (v *VoiceService) SetWakeEnabled(on bool) error { return v.w.SetEnabled(on) }

// SetWakeChime sets whether a wake plays a short chime.
func (v *VoiceService) SetWakeChime(on bool) error { return v.w.SetChime(on) }

// InstallWake starts the download the user just agreed to; progress arrives
// as EventWakeStatus, and a finished install switches the wake word on.
func (v *VoiceService) InstallWake() error { return v.w.Install() }

// CancelWakeInstall stops a running wake word download.
func (v *VoiceService) CancelWakeInstall() { v.w.CancelInstall() }

// SessionEnded tells the wake word a dictation session just ended, so its
// echo does not open the box again.
func (v *VoiceService) SessionEnded() { v.w.SessionEnded() }
