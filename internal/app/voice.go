package app

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"awesome-reminder/internal/paths"
	"awesome-reminder/internal/voice"
)

// EventVoiceStatus carries voice.Status whenever it changes (install
// progress included).
const EventVoiceStatus = "voice:status"

func init() {
	application.RegisterEvent[voice.Status](EventVoiceStatus)
}

// VoiceService is the capture box's dictation backend, bound to the
// frontend next to Service. It is a separate service so voice stays out of
// the core wiring.
type VoiceService struct {
	m *voice.Manager
	w *voice.Wake // "Hey Awesome" (wake.go)
}

// Status reports whether voice is ready, downloadable or unsupported.
func (v *VoiceService) Status() voice.Status { return v.m.Status() }

// Install starts the download the user just agreed to; progress arrives as
// EventVoiceStatus.
func (v *VoiceService) Install() error { return v.m.Install() }

// CancelInstall stops a running download.
func (v *VoiceService) CancelInstall() { v.m.CancelInstall() }

// Warm starts the speech engine while the user speaks.
func (v *VoiceService) Warm(ctx context.Context) error { return v.m.Warm(ctx) }

// Transcribe turns base64 16 kHz mono Int16 PCM into one capture line.
func (v *VoiceService) Transcribe(ctx context.Context, pcm string) (string, error) {
	return v.m.Transcribe(ctx, pcm)
}

// TranscribeSegment transcribes one piece of a continuous dictation; prompt
// is the recent text and speechMs the speech in the piece.
func (v *VoiceService) TranscribeSegment(ctx context.Context, pcm, prompt string, speechMs int) (string, error) {
	return v.m.TranscribeSegment(ctx, pcm, prompt, speechMs)
}

// ServiceShutdown stops the engine, the wake word helper and any download
// when the app quits.
func (v *VoiceService) ServiceShutdown() error {
	v.m.Close()
	v.w.Close()
	return nil
}

// registerVoice binds VoiceService to app and starts the wake word if it is
// on and installed. A wake opens win's capture box without focus.
func registerVoice(app *application.App, win *wailsWindows) {
	dir, err := paths.VoiceDir()
	if err != nil {
		log.Printf("voice dir: %v", err)
		dir = filepath.Join(os.TempDir(), "awesome-reminder-voice")
	}
	m := voice.NewManager(voice.Config{
		Dir:      dir,
		OnStatus: func(s voice.Status) { app.Event.Emit(EventVoiceStatus, s) },
	})
	w := newWake(app, win, dir)
	app.RegisterService(application.NewService(&VoiceService{m: m, w: w}))
	w.Start()
}
