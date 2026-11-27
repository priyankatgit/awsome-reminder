package voice

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type stubEngine struct {
	mu     sync.Mutex
	text   string
	calls  int
	warmed int
}

func (s *stubEngine) Warm(context.Context) error {
	s.mu.Lock()
	s.warmed++
	s.mu.Unlock()
	return nil
}

func (s *stubEngine) Transcribe(context.Context, []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.text, nil
}
func (s *stubEngine) Running() bool { return false }
func (s *stubEngine) Close()        {}

type fixture struct {
	cfg      Config
	engine   *stubEngine
	server   *bool
	mu       sync.Mutex
	statuses []Status
}

func (f *fixture) seen() []Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Status(nil), f.statuses...)
}

// newFixture serves a small engine zip at /engine and modelBody at /model.
func newFixture(t *testing.T, goos string, modelBody []byte, servedModel []byte) *fixture {
	t.Helper()
	zipPath := writeZip(t, map[string]string{
		"Release/whisper-server.exe": "server",
		"Release/whisper.dll":        "dll",
	})
	zipBytes, _ := os.ReadFile(zipPath)
	if servedModel == nil {
		servedModel = modelBody
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engine":
			_, _ = w.Write(zipBytes)
		case "/model":
			_, _ = w.Write(servedModel)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	f := &fixture{engine: &stubEngine{text: " Fix it hashtag ops.\n"}, server: new(bool)}
	f.cfg = Config{
		Dir:            t.TempDir(),
		GOOS:           goos,
		GOARCH:         "amd64",
		Client:         srv.Client(),
		LookPath:       func(string) (string, error) { return "", errors.New("not found") },
		RuntimeMissing: func() []string { return nil },
		NewEngine: func(bin string, server bool, c EngineConfig) Engine {
			*f.server = server
			return f.engine
		},
		OnStatus: func(s Status) {
			f.mu.Lock()
			f.statuses = append(f.statuses, s)
			f.mu.Unlock()
		},
		Model: &Asset{Name: "English model", URL: srv.URL + "/model", SHA256: sha(modelBody), Size: int64(len(modelBody))},
	}
	if goos == "windows" {
		f.cfg.EngineAsset = &Asset{Name: "speech engine", URL: srv.URL + "/engine", SHA256: sha(zipBytes), Size: int64(len(zipBytes)), Archive: "zip"}
	}
	return f
}

var modelBody = []byte(strings.Repeat("model!", 50_000))

func waitState(t *testing.T, m *Manager, state string) Status {
	t.Helper()
	var st Status
	ok := waitFor(t, 5*time.Second, func() bool { st = m.Status(); return st.State == state })
	if !ok {
		t.Fatalf("state = %+v, want %s", st, state)
	}
	return st
}

func TestStatusAbsentOffersDownload(t *testing.T) {
	f := newFixture(t, "windows", modelBody, nil)
	m := NewManager(f.cfg)
	st := m.Status()
	want := f.cfg.EngineAsset.Size + f.cfg.Model.Size
	if st.State != "absent" || st.Download != want || st.Engine != "bundled" {
		t.Errorf("status = %+v, want absent/bundled with download %d", st, want)
	}
}

func TestInstallThenReady(t *testing.T) {
	f := newFixture(t, "windows", modelBody, nil)
	m := NewManager(f.cfg)
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "ready")

	var phases []string
	var lastDone int64 = -1
	var final Status
	for _, s := range f.seen() {
		if s.State != "installing" {
			final = s
			continue
		}
		if len(phases) == 0 || phases[len(phases)-1] != s.Phase {
			phases = append(phases, s.Phase)
		}
		if s.Done < lastDone {
			t.Errorf("progress went back: %d after %d", s.Done, lastDone)
		}
		lastDone = s.Done
		if s.Total != f.cfg.EngineAsset.Size+f.cfg.Model.Size {
			t.Errorf("total = %d", s.Total)
		}
	}
	if strings.Join(phases, ",") != "engine,model" {
		t.Errorf("phases = %v, want engine,model", phases)
	}
	if lastDone != f.cfg.EngineAsset.Size+f.cfg.Model.Size {
		t.Errorf("last done = %d, want total", lastDone)
	}
	if final.State != "ready" {
		t.Errorf("last status = %+v, want ready", final)
	}
	for _, p := range []string{"engine-b5454/.ok", "engine-b5454/whisper-server.exe", "models/ggml-base.en.bin"} {
		if _, err := os.Stat(filepath.Join(f.cfg.Dir, p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.cfg.Dir, "engine-b5454.zip")); !os.IsNotExist(err) {
		t.Error("engine archive should be removed after extraction")
	}
}

func TestInstallFailureReportsError(t *testing.T) {
	f := newFixture(t, "windows", modelBody, []byte(strings.Repeat("tampered", 37_500)))
	m := NewManager(f.cfg)
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return m.Status().Error != "" })
	st := m.Status()
	if st.State != "absent" || !strings.Contains(st.Error, "checksum") {
		t.Errorf("status = %+v, want absent with a checksum error", st)
	}
	if _, err := os.Stat(filepath.Join(f.cfg.Dir, "models", "ggml-base.en.bin")); !os.IsNotExist(err) {
		t.Error("no model file may be left after a failed download")
	}
	if st.Download != f.cfg.Model.Size {
		t.Errorf("download = %d, want only the model left (%d)", st.Download, f.cfg.Model.Size)
	}
}

func TestCancelInstall(t *testing.T) {
	f := newFixture(t, "windows", modelBody, nil)
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(block); srv.Close() })
	f.cfg.EngineAsset.URL = srv.URL
	m := NewManager(f.cfg)
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "installing")
	m.CancelInstall()
	st := waitState(t, m, "absent")
	if st.Error != "" {
		t.Errorf("cancel is not an error: %q", st.Error)
	}
}

func TestUnsupportedWithoutEngine(t *testing.T) {
	f := newFixture(t, "darwin", modelBody, nil)
	m := NewManager(f.cfg)
	st := m.Status()
	if st.State != "unsupported" || st.Hint != "Install the speech engine with Homebrew: brew install whisper.cpp" {
		t.Errorf("status = %+v", st)
	}
	if err := m.Install(); err == nil {
		t.Error("Install should refuse when unsupported")
	}
}

func TestSystemCLIEngine(t *testing.T) {
	f := newFixture(t, "darwin", modelBody, nil)
	f.cfg.LookPath = func(name string) (string, error) {
		if name == "whisper-cli" {
			return "/opt/homebrew/bin/whisper-cli", nil
		}
		return "", errors.New("not found")
	}
	m := NewManager(f.cfg)
	st := m.Status()
	if st.State != "absent" || st.Download != f.cfg.Model.Size || st.Engine != "system" {
		t.Fatalf("status = %+v", st)
	}
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "ready")
	if _, err := m.Transcribe(context.Background(), pcmB64(make([]int16, MinSamples))); err != nil {
		t.Fatal(err)
	}
	if *f.server {
		t.Error("whisper-cli must run as a CLI engine")
	}
}

func TestSystemServerPreferred(t *testing.T) {
	f := newFixture(t, "darwin", modelBody, nil)
	f.cfg.LookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }
	m := NewManager(f.cfg)
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "ready")
	if err := m.Warm(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !*f.server || f.engine.warmed != 1 {
		t.Errorf("server=%v warmed=%d, want whisper-server warmed once", *f.server, f.engine.warmed)
	}
}

func TestMissingRuntime(t *testing.T) {
	f := newFixture(t, "windows", modelBody, nil)
	f.cfg.RuntimeMissing = func() []string { return []string{"vcomp140.dll"} }
	st := NewManager(f.cfg).Status()
	want := "Install the Microsoft Visual C++ Redistributable (x64) from https://aka.ms/vs/17/release/vc_redist.x64.exe, then try again."
	if st.State != "unsupported" || st.Hint != want {
		t.Errorf("status = %+v", st)
	}
}

func TestTranscribeCleansText(t *testing.T) {
	f := newFixture(t, "windows", modelBody, nil)
	m := NewManager(f.cfg)
	if _, err := m.Transcribe(context.Background(), pcmB64(make([]int16, MinSamples))); err == nil {
		t.Error("Transcribe before install should fail")
	}
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "ready")
	got, err := m.Transcribe(context.Background(), pcmB64(make([]int16, MinSamples)))
	if err != nil || got != "Fix it #ops" {
		t.Errorf("Transcribe = %q, %v", got, err)
	}
	if _, err := m.Transcribe(context.Background(), pcmB64(make([]int16, 10))); !errors.Is(err, ErrTooShort) {
		t.Errorf("short audio: err = %v", err)
	}
	if f.engine.calls != 1 {
		t.Errorf("engine calls = %d, want 1", f.engine.calls)
	}
}
