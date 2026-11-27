package voice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// ErrWakeNotInstalled means the wake word was switched on before its files
// were downloaded.
var ErrWakeNotInstalled = errors.New("voice: wake word files are not installed")

const hintWakeUnsupported = "The wake word isn't available on this system yet."

const (
	wakeModelDir  = "kws-gigaspeech-3.3M"
	wakeKeywordsF = "wake_keywords.txt"
	wakeConfigF   = "wake.json"
)

// WakeStatus is what the settings and the capture box need to know about
// the "Hey Awesome" wake word.
type WakeStatus struct {
	// State is "off" (installed, switched off), "absent" (a consented
	// download of Download bytes makes it available), "installing",
	// "starting", "listening", "error" or "unsupported".
	State    string `json:"state"`
	Enabled  bool   `json:"enabled"`
	Chime    bool   `json:"chime"`
	Phase    string `json:"phase"`    // "engine" | "model" while installing
	Done     int64  `json:"done"`     // bytes downloaded so far
	Total    int64  `json:"total"`    // bytes this install downloads
	Download int64  `json:"download"` // bytes a consent click will fetch
	Error    string `json:"error"`
	Hint     string `json:"hint"`
}

// WakeConfig configures a Wake. Zero fields get production defaults.
//
// RuntimeMissing is deliberately absent: the Windows build is the static-CRT
// (MT) one, whose DLLs import only system libraries, so the wake word never
// needs the Visual C++ runtime check.
type WakeConfig struct {
	Dir          string // voice folder (paths.VoiceDir)
	GOOS, GOARCH string
	Client       *http.Client
	OnStatus     func(WakeStatus)
	OnWake       func()      // used by the helper supervision (Task 4)
	Suppress     func() bool // used by the helper supervision (Task 4)
	Engine       *Asset      // test override for WakeEngineAsset(GOOS, GOARCH)
	Model        *Asset      // test override for WakeModelAsset
	// Bin is a test override for the helper program path.
	Bin     func(engineDir string) string
	Backoff []time.Duration
	Tick    time.Duration
	Now     func() time.Time
}

type wakeConfigFile struct {
	Enabled bool `json:"enabled"`
	Chime   bool `json:"chime"`
}

// Wake owns the wake-word files, the consented install, the per-device
// on/off and chime settings, and the helper process.
type Wake struct {
	c WakeConfig

	mu         sync.Mutex
	cfg        wakeConfigFile
	installing bool
	cancel     context.CancelFunc
	prog       WakeStatus
	lastErr    string
	lastEmit   time.Time
	helper     *wakeProc
	sessionEnd time.Time
}

// NewWake fills in defaults and reads wake.json (missing or unreadable means
// off, chime on). It starts nothing; Start does.
func NewWake(c WakeConfig) *Wake {
	if c.GOOS == "" {
		c.GOOS, c.GOARCH = runtime.GOOS, runtime.GOARCH
	}
	if c.Client == nil {
		c.Client = &http.Client{}
	}
	if c.Engine == nil {
		if a, ok := WakeEngineAsset(c.GOOS, c.GOARCH); ok {
			c.Engine = &a
		}
	}
	if c.Model == nil {
		m := WakeModelAsset
		c.Model = &m
	}
	if c.Bin == nil {
		c.Bin = c.defaultBin
	}
	if c.Backoff == nil {
		c.Backoff = []time.Duration{time.Second, 5 * time.Second, 30 * time.Second}
	}
	if c.Tick == 0 {
		c.Tick = time.Second
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	w := &Wake{c: c, cfg: wakeConfigFile{Chime: true}}
	if b, err := os.ReadFile(filepath.Join(c.Dir, wakeConfigF)); err == nil {
		var f wakeConfigFile
		f.Chime = true // a file without the field keeps the default
		if json.Unmarshal(b, &f) == nil {
			w.cfg = f
		}
	}
	return w
}

// defaultBin is the helper program: on Windows our own executable (started
// with --wake-helper), elsewhere the stock microphone spotter.
func (c WakeConfig) defaultBin(engineDir string) string {
	if c.GOOS == "windows" {
		if exe, err := os.Executable(); err == nil {
			return exe
		}
		return ""
	}
	return filepath.Join(engineDir, "sherpa-onnx-keyword-spotter-microphone")
}

func (w *Wake) engineDir() string { return filepath.Join(w.c.Dir, "wake-"+WakeRelease) }
func (w *Wake) modelDir() string  { return filepath.Join(w.c.Dir, "models", wakeModelDir) }

func (w *Wake) engineInstalled() bool {
	b, err := os.ReadFile(filepath.Join(w.engineDir(), okFile))
	return err == nil && string(b) == WakeRelease
}

func (w *Wake) modelInstalled() bool {
	for _, f := range append([]string{wakeKeywordsF}, wakeModelFiles...) {
		if st, err := os.Stat(filepath.Join(w.modelDir(), f)); err != nil || st.Size() == 0 {
			return false
		}
	}
	return true
}

func (w *Wake) installed() bool { return w.engineInstalled() && w.modelInstalled() }

// Status reports the current wake-word state.
func (w *Wake) Status() WakeStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.statusLocked()
}

func (w *Wake) statusLocked() WakeStatus {
	if w.installing {
		p := w.prog
		p.Enabled, p.Chime = w.cfg.Enabled, w.cfg.Chime
		return p
	}
	st := WakeStatus{Enabled: w.cfg.Enabled, Chime: w.cfg.Chime, Error: w.lastErr}
	switch {
	case w.c.Engine == nil:
		st.State, st.Hint, st.Enabled = "unsupported", hintWakeUnsupported, false
	case !w.installed():
		st.State = "absent"
		if !w.engineInstalled() {
			st.Download += w.c.Engine.Size
		}
		if !w.modelInstalled() {
			st.Download += w.c.Model.Size
		}
	case !w.cfg.Enabled:
		st.State, st.Error = "off", ""
	default:
		st.State = "starting"
	}
	return st
}

func (w *Wake) emit(force bool) {
	w.mu.Lock()
	if !force && time.Since(w.lastEmit) < statusEvery {
		w.mu.Unlock()
		return
	}
	w.lastEmit = time.Now()
	st := w.statusLocked()
	w.mu.Unlock()
	if w.c.OnStatus != nil {
		w.c.OnStatus(st)
	}
}

// saveLocked writes wake.json atomically (temp file, then rename).
func (w *Wake) saveLocked() error {
	b, err := json.Marshal(w.cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(w.c.Dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(w.c.Dir, "wake-*.json.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(w.c.Dir, wakeConfigF)); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// SetEnabled switches the wake word on or off for this device. Turning it on
// needs the files (ErrWakeNotInstalled otherwise); turning it off stops the
// helper.
func (w *Wake) SetEnabled(on bool) error {
	w.mu.Lock()
	if w.c.Engine == nil {
		w.mu.Unlock()
		return errors.New("voice: " + hintWakeUnsupported)
	}
	if on && (w.installing || !w.installed()) {
		w.mu.Unlock()
		return ErrWakeNotInstalled
	}
	prev := w.cfg.Enabled
	w.cfg.Enabled = on
	if err := w.saveLocked(); err != nil {
		w.cfg.Enabled = prev
		w.mu.Unlock()
		return err
	}
	if on {
		w.lastErr = ""
		w.startHelperLocked()
	} else {
		w.stopHelperLocked()
	}
	w.mu.Unlock()
	w.emit(true)
	return nil
}

// SetChime switches the "heard you" chime on or off.
func (w *Wake) SetChime(on bool) error {
	w.mu.Lock()
	prev := w.cfg.Chime
	w.cfg.Chime = on
	if err := w.saveLocked(); err != nil {
		w.cfg.Chime = prev
		w.mu.Unlock()
		return err
	}
	w.mu.Unlock()
	w.emit(true)
	return nil
}

// Start brings the helper up at app start when the wake word is on and
// installed. It is a no-op otherwise.
func (w *Wake) Start() {
	w.mu.Lock()
	if w.cfg.Enabled && w.installed() {
		w.startHelperLocked()
	}
	w.mu.Unlock()
	w.emit(true)
}

// SessionEnded tells the wake word a dictation session just ended, so the
// helper supervision can suppress an echo of it.
func (w *Wake) SessionEnded() {
	w.mu.Lock()
	w.sessionEnd = w.c.Now()
	w.mu.Unlock()
}

// Install starts the consented download of whatever is missing and, when it
// succeeds, switches the wake word on and starts it. It returns at once;
// progress arrives through OnStatus.
func (w *Wake) Install() error {
	w.mu.Lock()
	if w.installing {
		w.mu.Unlock()
		return nil
	}
	st := w.statusLocked()
	if st.State != "absent" {
		w.mu.Unlock()
		return fmt.Errorf("voice: nothing to install (%s)", st.State)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.installing, w.cancel, w.lastErr = true, cancel, ""
	phase := "model"
	if !w.engineInstalled() {
		phase = "engine"
	}
	w.prog = WakeStatus{State: "installing", Phase: phase, Total: st.Download}
	w.mu.Unlock()
	w.emit(true)

	go func() {
		err := w.install(ctx)
		w.mu.Lock()
		w.installing, w.cancel = false, nil
		switch {
		case err == nil:
			w.lastErr = ""
			w.cfg.Enabled = true
			if serr := w.saveLocked(); serr != nil {
				w.cfg.Enabled = false
				w.lastErr = "Couldn't save the wake word setting: " + serr.Error()
			} else {
				w.startHelperLocked()
			}
		case errors.Is(err, context.Canceled):
			w.lastErr = ""
		default:
			w.lastErr = installErrText(err)
			log.Printf("voice: wake install: %v", err)
		}
		w.mu.Unlock()
		cancel()
		w.emit(true)
	}()
	return nil
}

func (w *Wake) install(ctx context.Context) error {
	var base int64 // bytes finished in earlier phases
	progress := func(phase string, size int64) func(int64) {
		return func(done int64) {
			w.mu.Lock()
			force := w.prog.Phase != phase || done == size
			w.prog.Phase, w.prog.Done = phase, base+done
			w.mu.Unlock()
			w.emit(force)
		}
	}
	if err := os.MkdirAll(w.c.Dir, 0o755); err != nil {
		return err
	}

	if !w.engineInstalled() {
		a := *w.c.Engine
		report := progress("engine", a.Size)
		report(0)
		archive := filepath.Join(w.c.Dir, "wake-"+WakeRelease+"."+a.Archive)
		if err := Download(ctx, w.c.Client, a, archive, report); err != nil {
			return err
		}
		dir := w.engineDir()
		_ = os.RemoveAll(dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			os.Remove(archive)
			return err
		}
		err := Extract(archive, a.Archive, dir, wakeEngineFile)
		os.Remove(archive)
		if err != nil {
			return fmt.Errorf("voice: unpack wake word engine: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, okFile), []byte(WakeRelease), 0o644); err != nil {
			return err
		}
		base += a.Size
	}

	if !w.modelInstalled() {
		a := *w.c.Model
		report := progress("model", a.Size)
		report(0)
		archive := filepath.Join(w.c.Dir, "wake-model."+a.Archive)
		if err := Download(ctx, w.c.Client, a, archive, report); err != nil {
			return err
		}
		dir := w.modelDir()
		_ = os.RemoveAll(dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			os.Remove(archive)
			return err
		}
		err := Extract(archive, a.Archive, dir, wakeModelFile)
		os.Remove(archive)
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, wakeKeywordsF), wakeKeywords, 0o644)
		}
		if err != nil {
			_ = os.RemoveAll(dir)
			return fmt.Errorf("voice: unpack wake word model: %w", err)
		}
	}
	return nil
}

// CancelInstall stops a running install; the state returns to "absent".
func (w *Wake) CancelInstall() {
	w.mu.Lock()
	cancel := w.cancel
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Close cancels an install and stops the helper.
func (w *Wake) Close() {
	w.CancelInstall()
	w.mu.Lock()
	w.stopHelperLocked()
	w.mu.Unlock()
}

// wakeProc is one running helper process.
type wakeProc struct {
	cmd   *exec.Cmd
	stdin io.Closer
	done  chan struct{}
}

func (w *Wake) helperRunning() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.helper != nil
}

// startHelperLocked is the seam Task 4 replaces with real supervision
// (arguments, output parsing, restart backoff). Here it just starts the
// helper program and tracks it so Close and SetEnabled(false) can stop it.
func (w *Wake) startHelperLocked() {
	if w.helper != nil {
		return
	}
	cmd := exec.Command(w.c.Bin(w.engineDir()))
	hideAndContain(cmd)
	switch w.c.GOOS {
	case "linux":
		cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+w.engineDir())
	case "darwin":
		cmd.Env = append(os.Environ(), "DYLD_LIBRARY_PATH="+w.engineDir())
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		w.lastErr = "Couldn't start the wake word: " + err.Error()
		return
	}
	if err := cmd.Start(); err != nil {
		w.lastErr = "Couldn't start the wake word: " + err.Error()
		log.Printf("voice: wake helper start: %v", err)
		return
	}
	release := afterStart(cmd.Process)
	p := &wakeProc{cmd: cmd, stdin: stdin, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		release()
		close(p.done)
	}()
	w.helper = p
}

func (w *Wake) stopHelperLocked() {
	p := w.helper
	if p == nil {
		return
	}
	w.helper = nil
	_ = p.stdin.Close()
	_ = p.cmd.Process.Kill()
	<-p.done
}
