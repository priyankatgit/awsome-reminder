package voice

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type wakeFixture struct {
	cfg    WakeConfig
	engine Asset
	model  Asset
}

func newWakeFixture(t *testing.T, servedModel []byte) *wakeFixture {
	t.Helper()
	engZip, _ := os.ReadFile(writeZip(t, map[string]string{
		"pkg/lib/sherpa-onnx-c-api.dll":            "capi",
		"pkg/lib/onnxruntime.dll":                  "ort",
		"pkg/lib/onnxruntime_providers_shared.dll": "prov",
		"pkg/lib/sherpa-onnx-cxx-api.dll":          "unused",
		"pkg/include/sherpa-onnx/c-api/c-api.h":    "header",
	}))
	modelZip, _ := os.ReadFile(writeZip(t, map[string]string{
		"m/tokens.txt": "tok",
		"m/encoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx": "enc",
		"m/decoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx": "dec",
		"m/joiner-epoch-12-avg-2-chunk-16-left-64.int8.onnx":  "join",
		"m/encoder-epoch-12-avg-2-chunk-16-left-64.onnx":      "fp32",
	}))
	if servedModel == nil {
		servedModel = modelZip
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engine":
			_, _ = w.Write(engZip)
		case "/model":
			_, _ = w.Write(servedModel)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	f := &wakeFixture{
		engine: Asset{Name: "wake engine", URL: srv.URL + "/engine", SHA256: sha(engZip), Size: int64(len(engZip)), Archive: "zip"},
		model:  Asset{Name: "wake model", URL: srv.URL + "/model", SHA256: sha(modelZip), Size: int64(len(servedModel)), Archive: "zip"},
	}
	f.cfg = WakeConfig{
		Dir:    t.TempDir(),
		GOOS:   "windows",
		GOARCH: "amd64",
		Client: srv.Client(),
		Engine: &f.engine,
		Model:  &f.model,
		Bin:    func(string) string { return os.Args[0] },
	}
	t.Setenv("FAKE_KWS", "idle")
	return f
}

func waitWake(t *testing.T, w *Wake, cond func(WakeStatus) bool) WakeStatus {
	t.Helper()
	var st WakeStatus
	if !waitFor(t, 5*time.Second, func() bool { st = w.Status(); return cond(st) }) {
		t.Fatalf("status = %+v", st)
	}
	return st
}

func TestWakeDefaultsOff(t *testing.T) {
	f := newWakeFixture(t, nil)
	w := NewWake(f.cfg)
	defer w.Close()
	st := w.Status()
	if st.State != "absent" || st.Enabled || !st.Chime {
		t.Errorf("status = %+v", st)
	}
	if want := f.engine.Size + f.model.Size; st.Download != want {
		t.Errorf("download = %d, want %d", st.Download, want)
	}
}

func TestWakeConfigCorruptFallsBack(t *testing.T) {
	f := newWakeFixture(t, nil)
	if err := os.WriteFile(filepath.Join(f.cfg.Dir, "wake.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := NewWake(f.cfg)
	defer w.Close()
	if st := w.Status(); st.Enabled || !st.Chime {
		t.Errorf("status = %+v, want defaults", st)
	}
}

func TestSetEnabledNeedsInstall(t *testing.T) {
	f := newWakeFixture(t, nil)
	w := NewWake(f.cfg)
	defer w.Close()
	if err := w.SetEnabled(true); err != ErrWakeNotInstalled {
		t.Errorf("err = %v, want ErrWakeNotInstalled", err)
	}
	if w.Status().Enabled {
		t.Error("must stay off")
	}
}

func TestWakeInstallThenEnabled(t *testing.T) {
	f := newWakeFixture(t, nil)
	var seenMu sync.Mutex
	var seen []WakeStatus
	f.cfg.OnStatus = func(s WakeStatus) { seenMu.Lock(); seen = append(seen, s); seenMu.Unlock() }
	w := NewWake(f.cfg)
	defer w.Close()
	if err := w.Install(); err != nil {
		t.Fatal(err)
	}
	st := waitWake(t, w, func(s WakeStatus) bool { return s.Enabled && (s.State == "starting" || s.State == "listening") })
	if st.Error != "" {
		t.Errorf("error = %q", st.Error)
	}
	b, _ := os.ReadFile(filepath.Join(f.cfg.Dir, "wake.json"))
	var disk struct{ Enabled, Chime bool }
	if err := json.Unmarshal(b, &disk); err != nil || !disk.Enabled || !disk.Chime {
		t.Errorf("wake.json = %s (%v)", b, err)
	}
	dir := filepath.Join(f.cfg.Dir, "wake-"+WakeRelease)
	mdir := filepath.Join(f.cfg.Dir, "models", "kws-gigaspeech-3.3M")
	for _, p := range []string{
		filepath.Join(dir, ".ok"), filepath.Join(dir, "sherpa-onnx-c-api.dll"), filepath.Join(dir, "onnxruntime.dll"),
		filepath.Join(dir, "onnxruntime_providers_shared.dll"),
		filepath.Join(mdir, "tokens.txt"), filepath.Join(mdir, "wake_keywords.txt"),
		filepath.Join(mdir, "encoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%v", err)
		}
	}
	assertGone(t, filepath.Join(dir, "sherpa-onnx-cxx-api.dll"), filepath.Join(mdir, "encoder-epoch-12-avg-2-chunk-16-left-64.onnx"))
	if ok, _ := os.ReadFile(filepath.Join(dir, ".ok")); string(ok) != WakeRelease {
		t.Errorf(".ok = %q", ok)
	}
	if kw, _ := os.ReadFile(filepath.Join(mdir, "wake_keywords.txt")); string(kw) != string(wakeKeywords) {
		t.Error("keywords file differs from the embedded one")
	}
	if !w.helperRunning() {
		t.Error("helper should be running")
	}
	sawInstalling := false
	seenMu.Lock()
	for _, s := range seen {
		sawInstalling = sawInstalling || s.State == "installing"
	}
	seenMu.Unlock()
	if !sawInstalling {
		t.Error("no installing status was emitted")
	}

	// Off stops the helper and persists; on again starts it.
	if err := w.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if w.helperRunning() || w.Status().State != "off" {
		t.Errorf("after off: %+v running=%v", w.Status(), w.helperRunning())
	}
	if err := w.SetEnabled(true); err != nil || !w.helperRunning() {
		t.Errorf("re-enable: %v running=%v", err, w.helperRunning())
	}
	w.Close()
	if w.helperRunning() {
		t.Error("Close must kill the helper")
	}
}

func TestWakeInstallChecksumFailure(t *testing.T) {
	f := newWakeFixture(t, []byte("tampered-model-bytes"))
	f.model.SHA256 = strings.Repeat("0", 64)
	w := NewWake(f.cfg)
	defer w.Close()
	if err := w.Install(); err != nil {
		t.Fatal(err)
	}
	waitWake(t, w, func(s WakeStatus) bool { return s.Error != "" })
	st := w.Status()
	if st.State != "absent" || !strings.Contains(st.Error, "checksum") || st.Enabled {
		t.Errorf("status = %+v", st)
	}
	mdir := filepath.Join(f.cfg.Dir, "models", "kws-gigaspeech-3.3M")
	if ents, _ := os.ReadDir(mdir); len(ents) != 0 {
		t.Errorf("model files left: %v", ents)
	}
	if w.helperRunning() {
		t.Error("no helper after a failed install")
	}
}

func TestWakeUnsupportedPlatform(t *testing.T) {
	f := newWakeFixture(t, nil)
	f.cfg.GOOS, f.cfg.Engine = "plan9", nil
	w := NewWake(f.cfg)
	defer w.Close()
	st := w.Status()
	if st.State != "unsupported" || st.Hint != "The wake word isn't available on this system yet." {
		t.Errorf("status = %+v", st)
	}
	if w.Install() == nil || w.SetEnabled(true) == nil {
		t.Error("install and enable must be refused")
	}
}

func TestSetChimePersists(t *testing.T) {
	f := newWakeFixture(t, nil)
	w := NewWake(f.cfg)
	if err := w.SetChime(false); err != nil {
		t.Fatal(err)
	}
	w.Close()
	w2 := NewWake(f.cfg)
	defer w2.Close()
	if w2.Status().Chime {
		t.Error("chime should persist as false")
	}
}

func TestWakeEngineAssetTable(t *testing.T) {
	for _, p := range [][2]string{{"windows", "amd64"}, {"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "arm64"}, {"darwin", "amd64"}} {
		a, ok := WakeEngineAsset(p[0], p[1])
		if !ok || a.Archive != "tar.bz2" || len(a.SHA256) != 64 || a.Size == 0 || !strings.Contains(a.URL, "/v2.0.3/") {
			t.Errorf("%v: %+v ok=%v", p, a, ok)
		}
	}
	if _, ok := WakeEngineAsset("windows", "arm64"); ok {
		t.Error("windows/arm64 has no asset")
	}
	if !wakeEngineFile("x/lib/onnxruntime.dll") || wakeEngineFile("x/lib/sherpa-onnx-cxx-api.dll") || !wakeEngineFile("x/lib/libsherpa-onnx-cxx-api.so") {
		t.Error("keep-list")
	}
	if wakeModelFile("m/encoder-epoch-12-avg-2-chunk-16-left-64.onnx") || !wakeModelFile("m/tokens.txt") {
		t.Error("model keep-list")
	}
}

func TestWakeCloseDuringInstallStartsNoHelper(t *testing.T) {
	f := newWakeFixture(t, nil)
	release := make(chan struct{})
	inModel := make(chan struct{}, 1)
	blocker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case inModel <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(blocker.Close)
	f.model.URL = blocker.URL
	w := NewWake(f.cfg)
	if err := w.Install(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-inModel:
	case <-time.After(5 * time.Second):
		t.Fatal("model download never started")
	}
	w.Close()
	close(release)
	time.Sleep(100 * time.Millisecond)
	if w.helperRunning() {
		t.Error("helper started after Close")
	}
	if st := w.Status(); st.State == "starting" || st.State == "listening" || st.Enabled {
		t.Errorf("status after Close = %+v", st)
	}
	_ = w.SetEnabled(true)
	if w.helperRunning() {
		t.Error("helper started by SetEnabled after Close")
	}
}
