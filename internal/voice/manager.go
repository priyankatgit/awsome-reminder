package voice

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Status is what the capture box needs to know about voice.
type Status struct {
	// State is "unsupported" (no engine for this system; see Hint),
	// "absent" (a consented download of Download bytes makes it ready),
	// "installing" (see Phase/Done/Total) or "ready".
	State    string `json:"state"`
	Phase    string `json:"phase"`    // "engine" | "model" while installing
	Done     int64  `json:"done"`     // bytes downloaded so far
	Total    int64  `json:"total"`    // bytes this install downloads
	Download int64  `json:"download"` // bytes still to download (absent)
	Engine   string `json:"engine"`   // "bundled" | "system" | ""
	Error    string `json:"error"`    // last install error, "" if none
	Hint     string `json:"hint"`     // how to fix an unsupported setup
	// Model is the active speech model id (see Models).
	Model string `json:"model"`
	// Target is the model a background switch is downloading ("" if none);
	// the state stays "ready" (the old model keeps working) and
	// Phase/Done/Total report the download.
	Target string `json:"target"`
	// SwitchError is why the last model switch failed ("" if it did not).
	SwitchError string `json:"switchError"`
}

// User-facing hints (exact copy, see the Phase 1.5 plan).
const (
	hintHomebrew = "Install the speech engine with Homebrew: brew install whisper.cpp"
	hintVCRedist = "Install the Microsoft Visual C++ Redistributable (x64) from https://aka.ms/vs/17/release/vc_redist.x64.exe, then try again."
)

const (
	okFile      = ".ok"
	statusEvery = 100 * time.Millisecond
)

// Config configures a Manager. Zero fields get production defaults.
type Config struct {
	Dir            string // voice folder (paths.VoiceDir)
	GOOS, GOARCH   string
	Client         *http.Client
	LookPath       func(name string) (string, error)
	SearchDirs     []string // extra places to look for a system engine
	RuntimeMissing func() []string
	NewEngine      func(bin string, server bool, c EngineConfig) Engine
	OnStatus       func(Status)
	Models         []ModelSpec // test override for the Models catalogue
	EngineAsset    *Asset      // test override for EngineAsset(GOOS, GOARCH)
}

// Manager owns the voice files, the consented install and the engine.
type Manager struct {
	c Config

	mu         sync.Mutex
	installing bool
	cancel     context.CancelFunc
	prog       Status // progress while installing
	lastErr    string
	lastEmit   time.Time
	eng        Engine
	engFor     string // bin and model the engine was made for

	cfg      voiceConfig // voice.json
	swTarget string      // model a background switch downloads
	swCancel context.CancelFunc
	swDone   int64
	swErr    string
}

// NewManager fills in defaults.
func NewManager(c Config) *Manager {
	if c.GOOS == "" {
		c.GOOS, c.GOARCH = runtime.GOOS, runtime.GOARCH
	}
	if c.Client == nil {
		c.Client = &http.Client{}
	}
	if c.LookPath == nil {
		c.LookPath = exec.LookPath
	}
	if c.SearchDirs == nil && c.GOOS == "darwin" {
		// GUI apps on macOS do not get the shell's PATH.
		c.SearchDirs = []string{"/opt/homebrew/bin", "/usr/local/bin"}
	}
	if c.RuntimeMissing == nil {
		c.RuntimeMissing = runtimeMissing
	}
	if c.NewEngine == nil {
		c.NewEngine = func(bin string, server bool, ec EngineConfig) Engine {
			if server {
				return NewServerEngine(ec)
			}
			return NewCLIEngine(ec)
		}
	}
	if c.Models == nil {
		c.Models = Models
	}
	if c.EngineAsset == nil {
		if a, ok := EngineAsset(c.GOOS, c.GOARCH); ok {
			c.EngineAsset = &a
		}
	}
	m := &Manager{c: c}
	cfg, ok := loadVoiceConfig(c.Dir)
	if _, known := LookupModel(c.Models, cfg.Model); !ok || !known {
		cfg.Model = m.defaultModel()
	}
	if w, err := NormalizeWords(cfg.Words); err == nil {
		cfg.Words = w
	} else {
		cfg.Words = ""
	}
	m.cfg = cfg
	if m.modelInstalled() {
		m.removeOtherModels()
	}
	// A switch cut short by quitting leaves its partial download behind.
	if parts, _ := filepath.Glob(filepath.Join(m.modelsDir(), "*.part")); len(parts) > 0 {
		for _, p := range parts {
			_ = os.Remove(p)
		}
	}
	return m
}

// defaultModel is the model of a device without a (valid) voice.json: Base
// when it is installed (an existing user keeps what works), else another
// installed model (most accurate first), else the recommended one.
func (m *Manager) defaultModel() string {
	if s, ok := LookupModel(m.c.Models, ModelBase); ok && m.installedSpec(s) {
		return ModelBase
	}
	for i := len(m.c.Models) - 1; i >= 0; i-- {
		if m.installedSpec(m.c.Models[i]) {
			return m.c.Models[i].ID
		}
	}
	if _, ok := LookupModel(m.c.Models, RecommendedModel); ok {
		return RecommendedModel
	}
	return m.c.Models[0].ID
}

// spec is the active model.
func (m *Manager) spec() ModelSpec {
	s, _ := LookupModel(m.c.Models, m.cfg.Model)
	return s
}

// setup is how this system gets an engine.
type setup struct {
	bin     string
	server  bool
	bundled bool
	env     []string
	hint    string // non-empty: unsupported
}

func (m *Manager) engineDir() string { return filepath.Join(m.c.Dir, "engine-"+EngineRelease) }
func (m *Manager) modelsDir() string { return filepath.Join(m.c.Dir, "models") }
func (m *Manager) modelPath() string { return m.pathOf(m.spec()) }
func (m *Manager) pathOf(s ModelSpec) string {
	return filepath.Join(m.modelsDir(), s.File)
}

func (m *Manager) resolve() setup {
	if m.c.EngineAsset != nil {
		if missing := m.c.RuntimeMissing(); len(missing) > 0 {
			return setup{hint: hintVCRedist}
		}
		exe := "whisper-server"
		if m.c.GOOS == "windows" {
			exe += ".exe"
		}
		s := setup{bin: filepath.Join(m.engineDir(), exe), server: true, bundled: true}
		if m.c.GOOS == "linux" {
			s.env = []string{"LD_LIBRARY_PATH=" + m.engineDir()}
		}
		return s
	}
	for _, name := range []string{"whisper-server", "whisper-cli"} {
		if p := m.find(name); p != "" {
			return setup{bin: p, server: name == "whisper-server"}
		}
	}
	return setup{hint: hintHomebrew}
}

func (m *Manager) find(name string) string {
	if p, err := m.c.LookPath(name); err == nil {
		return p
	}
	for _, d := range m.c.SearchDirs {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func (m *Manager) engineInstalled() bool {
	b, err := os.ReadFile(filepath.Join(m.engineDir(), okFile))
	return err == nil && string(b) == EngineRelease
}

func (m *Manager) modelInstalled() bool { return m.installedSpec(m.spec()) }

func (m *Manager) installedSpec(s ModelSpec) bool {
	st, err := os.Stat(m.pathOf(s))
	return err == nil && st.Size() == s.Asset.Size
}

// removeOtherModels deletes the catalogue's model files other than the
// active one (one model on disk at a time). Failures are only logged.
func (m *Manager) removeOtherModels() {
	keep := m.spec().File
	for _, s := range m.c.Models {
		if s.File == keep {
			continue
		}
		if err := os.Remove(m.pathOf(s)); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("voice: remove unused model %s: %v", s.File, err)
		}
	}
}

// Status reports the current voice state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked()
}

func (m *Manager) statusLocked() Status {
	if m.installing {
		p := m.prog
		p.Model = m.cfg.Model
		return p
	}
	s := m.resolve()
	if s.hint != "" {
		return Status{State: "unsupported", Hint: s.hint, Error: m.lastErr, Model: m.cfg.Model}
	}
	st := Status{Engine: "system", Error: m.lastErr, Model: m.cfg.Model, SwitchError: m.swErr}
	if s.bundled {
		st.Engine = "bundled"
		if !m.engineInstalled() {
			st.Download += m.c.EngineAsset.Size
		}
	}
	if !m.modelInstalled() {
		st.Download += m.spec().Asset.Size
	}
	if st.Download > 0 {
		st.State = "absent"
	} else {
		st.State = "ready"
		st.Error = ""
	}
	if m.swTarget != "" {
		t, _ := LookupModel(m.c.Models, m.swTarget)
		st.Target, st.Phase, st.Done, st.Total = m.swTarget, "model", m.swDone, t.Asset.Size
	}
	return st
}

func (m *Manager) emit(force bool) {
	m.mu.Lock()
	if !force && time.Since(m.lastEmit) < statusEvery {
		m.mu.Unlock()
		return
	}
	m.lastEmit = time.Now()
	st := m.statusLocked()
	m.mu.Unlock()
	if m.c.OnStatus != nil {
		m.c.OnStatus(st)
	}
}

// Install starts the consented download of whatever is missing; progress
// arrives through OnStatus. It returns at once.
func (m *Manager) Install() error {
	m.mu.Lock()
	if m.installing {
		m.mu.Unlock()
		return nil
	}
	if m.swTarget != "" {
		m.mu.Unlock()
		return fmt.Errorf("voice: a speech model download is running")
	}
	st := m.statusLocked()
	if st.State != "absent" {
		m.mu.Unlock()
		return fmt.Errorf("voice: nothing to install (%s)", st.State)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.installing, m.cancel, m.lastErr = true, cancel, ""
	s := m.resolve()
	phase := "model"
	if s.bundled && !m.engineInstalled() {
		phase = "engine"
	}
	m.prog = Status{State: "installing", Phase: phase, Total: st.Download, Engine: st.Engine}
	m.mu.Unlock()
	m.emit(true)

	go func() {
		err := m.install(ctx, s)
		m.mu.Lock()
		m.installing, m.cancel = false, nil
		switch {
		case err == nil:
			m.lastErr = ""
			m.removeOtherModels()
		case errors.Is(err, context.Canceled):
			m.lastErr = ""
		default:
			m.lastErr = installErrText(err)
			log.Printf("voice: install: %v", err)
		}
		m.mu.Unlock()
		cancel()
		m.emit(true)
	}()
	return nil
}

func (m *Manager) install(ctx context.Context, s setup) error {
	var base int64 // bytes finished in earlier phases
	// progress reports bytes of the current phase; a new phase and the end
	// of a phase are always reported, the rest at most every 100 ms.
	progress := func(phase string, size int64) func(int64) {
		return func(done int64) {
			m.mu.Lock()
			force := m.prog.Phase != phase || done == size
			m.prog.Phase, m.prog.Done = phase, base+done
			m.mu.Unlock()
			m.emit(force)
		}
	}

	if s.bundled && !m.engineInstalled() {
		a := *m.c.EngineAsset
		report := progress("engine", a.Size)
		report(0)
		archive := filepath.Join(m.c.Dir, "engine-"+EngineRelease+"."+a.Archive)
		if err := os.MkdirAll(m.c.Dir, 0o755); err != nil {
			return err
		}
		if err := Download(ctx, m.c.Client, a, archive, report); err != nil {
			return err
		}
		dir := m.engineDir()
		_ = os.RemoveAll(dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		err := Extract(archive, a.Archive, dir, WhisperFile)
		os.Remove(archive)
		if err != nil {
			return fmt.Errorf("voice: unpack speech engine: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, okFile), []byte(EngineRelease), 0o644); err != nil {
			return err
		}
		base += a.Size
	}

	m.mu.Lock()
	spec := m.spec() // SetModel is refused while installing
	m.mu.Unlock()
	if !m.installedSpec(spec) {
		report := progress("model", spec.Asset.Size)
		report(0)
		if err := os.MkdirAll(m.modelsDir(), 0o755); err != nil {
			return err
		}
		if err := Download(ctx, m.c.Client, spec.Asset, m.pathOf(spec), report); err != nil {
			return err
		}
	}
	return nil
}

// installErrText is the install error as the capture box shows it.
func installErrText(err error) string {
	if errors.Is(err, ErrChecksum) {
		return "The download did not match its checksum and was discarded. Try again."
	}
	return "Download failed: " + strings.TrimPrefix(err.Error(), "voice: ")
}

// CancelInstall stops a running install; the state returns to "absent".
func (m *Manager) CancelInstall() {
	m.mu.Lock()
	cancel := m.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// engine returns the engine, creating it once voice is ready.
func (m *Manager) engine() (Engine, error) {
	m.mu.Lock()
	if st := m.statusLocked(); st.State != "ready" {
		m.mu.Unlock()
		return nil, fmt.Errorf("voice: not ready (%s)", st.State)
	}
	s := m.resolve()
	key := s.bin + "|" + m.modelPath()
	if m.eng != nil && m.engFor == key {
		e := m.eng
		m.mu.Unlock()
		return e, nil
	}
	old := m.eng
	m.eng = m.c.NewEngine(s.bin, s.server, EngineConfig{
		Bin:     s.bin,
		Model:   m.modelPath(),
		Threads: DefaultThreads(runtime.NumCPU()),
		Env:     s.env,
	})
	m.engFor = key
	e := m.eng
	m.mu.Unlock()
	if old != nil {
		// Outside mu: stopping a server can take seconds, and Status must
		// not wait on it.
		old.Close()
	}
	return e, nil
}

// Warm starts the engine while the user is still speaking, so the model is
// loaded by the time the recording ends.
func (m *Manager) Warm(ctx context.Context) error {
	e, err := m.engine()
	if err != nil {
		return err
	}
	return e.Warm(ctx)
}

// Transcribe turns base64 16 kHz mono Int16 PCM into one cleaned capture
// line ("" when nothing was said).
func (m *Manager) Transcribe(ctx context.Context, pcmB64 string) (string, error) {
	raw, err := m.transcribeRaw(ctx, pcmB64, "")
	if err != nil {
		return "", err
	}
	return DropPromptEcho(CleanTranscript(raw), -1), nil
}

// TranscribeSegment is Transcribe for one piece of a continuous dictation:
// recent (the text dictated so far) biases whisper, and a short piece
// (speechMs of speech) that is only a known silence hallucination comes
// back "".
func (m *Manager) TranscribeSegment(ctx context.Context, pcmB64, recent string, speechMs int) (string, error) {
	raw, err := m.transcribeRaw(ctx, pcmB64, recent)
	if err != nil {
		return "", err
	}
	return DropPromptEcho(DropHallucination(CleanTranscript(raw), speechMs), speechMs), nil
}

// transcribeRaw transcribes with BuildPrompt(words, recent).
func (m *Manager) transcribeRaw(ctx context.Context, pcmB64, recent string) (string, error) {
	pcm, err := DecodePCM(pcmB64)
	if err != nil {
		return "", err
	}
	e, err := m.engine()
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	prompt := BuildPrompt(m.cfg.Words, recent)
	m.mu.Unlock()
	start := time.Now()
	raw, err := e.Transcribe(ctx, EncodeWAV(pcm), prompt)
	if err != nil {
		return "", err
	}
	log.Printf("voice: transcribed %.1f s of audio in %v", float64(len(pcm))/SampleRate, time.Since(start).Round(time.Millisecond))
	return raw, nil
}

// Close cancels an install or model switch and stops the engine.
func (m *Manager) Close() {
	m.CancelInstall()
	m.CancelModelSwitch()
	m.mu.Lock()
	e := m.eng
	m.mu.Unlock()
	if e != nil {
		e.Close()
	}
}
