package voice

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	if !waitFor(t, 5*time.Second, w.helperRunning) {
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
	if err := w.SetEnabled(true); err != nil || !waitFor(t, 5*time.Second, w.helperRunning) {
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

// --- helper supervision (Task 4) ---

func TestParseDetection(t *testing.T) {
	other := strings.Replace(fakeDetectionJSON, "HEY_AWESOME", "OTHER", 1)
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"0:" + fakeDetectionJSON, true},
		{"\r3:" + fakeDetectionJSON, true},
		{"0:" + fakeDetectionJSON + "\r\n", true},
		{fakeDetectionJSON, true},
		{"0:" + other, false},
		{"Invalid device index: 3", false},
		{`0:{"start_time":0.00, "keyword": "HEY_AWESOME"`, false},
		{"", false},
	} {
		if got := parseDetection(c.in); got != c.want {
			t.Errorf("parseDetection(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestScanHelperOutput(t *testing.T) {
	// Linux form through a pipe: the detection must arrive without a
	// trailing newline, before EOF.
	pr, pw := io.Pipe()
	detected := make(chan string, 8)
	var linesMu sync.Mutex
	var lines []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanHelperOutput(pr, func(k string) { detected <- k }, func(l string) {
			linesMu.Lock()
			lines = append(lines, l)
			linesMu.Unlock()
		})
	}()
	write := func(s string) {
		t.Helper()
		if _, err := pw.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	wantDetect := func(what string) {
		t.Helper()
		select {
		case k := <-detected:
			if k != "HEY_AWESOME" {
				t.Errorf("%s: keyword %q", what, k)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: no detection before end of stream", what)
		}
	}
	write(fakeKWSStartup)
	write("\r0:" + fakeDetectionJSON)
	wantDetect("first")
	write("\n\r1:" + fakeDetectionJSON)
	wantDetect("second")
	write("\nInvalid device index: 3\r\n")
	pw.Close()
	<-done
	select {
	case k := <-detected:
		t.Errorf("extra detection %q", k)
	default:
	}
	want := []string{}
	for _, l := range strings.Split(fakeKWSStartup, "\n") {
		if l != "" {
			want = append(want, l)
		}
	}
	want = append(want, "Invalid device index: 3")
	linesMu.Lock()
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("lines =\n%q\nwant\n%q", lines, want)
	}
	linesMu.Unlock()

	// Windows form: one detection per line.
	var got, other []string
	scanHelperOutput(strings.NewReader("0:"+fakeDetectionJSON+"\r\n1:"+fakeDetectionJSON+"\n"),
		func(k string) { got = append(got, k) }, func(l string) { other = append(other, l) })
	if len(got) != 2 || got[0] != "HEY_AWESOME" || len(other) != 0 {
		t.Errorf("windows form: detections %q, lines %q", got, other)
	}

	// Another keyword is not a HEY_AWESOME detection.
	got = nil
	scanHelperOutput(strings.NewReader("0:"+strings.Replace(fakeDetectionJSON, "HEY_AWESOME", "OTHER", 1)+"\n"),
		func(k string) { got = append(got, k) }, func(string) {})
	for _, k := range got {
		if k == "HEY_AWESOME" {
			t.Error("other keyword reported as HEY_AWESOME")
		}
	}
}

func TestWakeHelperCommand(t *testing.T) {
	want := []string{
		"--tokens=/m/tokens.txt",
		"--encoder=/m/encoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx",
		"--decoder=/m/decoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx",
		"--joiner=/m/joiner-epoch-12-avg-2-chunk-16-left-64.int8.onnx",
		"--keywords-file=/m/wake_keywords.txt",
		"--num-threads=1",
		"--provider=cpu",
	}
	for _, goos := range []string{"linux", "darwin"} {
		bin, args := helperCommand(goos, "/app/awesome", "/e", "/m", "/m/wake_keywords.txt")
		if bin != "/e/sherpa-onnx-keyword-spotter-microphone" || strings.Join(args, " ") != strings.Join(want, " ") {
			t.Errorf("%s: %s %q", goos, bin, args)
		}
	}
	e, md := `C:\v\wake-v2.0.3`, `C:\v\models\kws`
	bin, args := helperCommand("windows", `C:\app\awesome.exe`, e, md, md+`\wake_keywords.txt`)
	wantWin := []string{
		"--wake-helper", "--lib-dir", e,
		`--encoder=C:\v\models\kws\encoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx`,
		`--decoder=C:\v\models\kws\decoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx`,
		`--joiner=C:\v\models\kws\joiner-epoch-12-avg-2-chunk-16-left-64.int8.onnx`,
		`--tokens=C:\v\models\kws\tokens.txt`,
		`--keywords-file=C:\v\models\kws\wake_keywords.txt`,
		"--num-threads=1",
	}
	if bin != `C:\app\awesome.exe` || strings.Join(args, " ") != strings.Join(wantWin, " ") {
		t.Errorf("windows: %s %q", bin, args)
	}
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// installWakeFiles puts a complete (fake) engine and model in dir.
func installWakeFiles(t *testing.T, dir string) {
	t.Helper()
	eng := filepath.Join(dir, "wake-"+WakeRelease)
	mdir := filepath.Join(dir, "models", "kws-gigaspeech-3.3M")
	for _, d := range []string{eng, mdir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{filepath.Join(eng, ".ok"): WakeRelease, filepath.Join(mdir, "wake_keywords.txt"): string(wakeKeywords)}
	for _, f := range wakeModelFiles {
		files[filepath.Join(mdir, f)] = "x"
	}
	for p, s := range files {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// newSupFixture is an installed wake word whose helper is the fake in the
// given mode, with millisecond backoff and a fake wall clock. The counter
// counts spawn attempts (Bin is asked once per attempt).
func newSupFixture(t *testing.T, mode string) (*wakeFixture, *fakeClock, *atomic.Int32) {
	t.Helper()
	f := newWakeFixture(t, nil)
	t.Setenv("FAKE_KWS", mode)
	spawns := new(atomic.Int32)
	f.cfg.Bin = func(string) string { spawns.Add(1); return os.Args[0] }
	clk := &fakeClock{t: time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC)}
	f.cfg.Now = clk.Now
	f.cfg.Backoff = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond}
	f.cfg.Tick = 20 * time.Millisecond
	installWakeFiles(t, f.cfg.Dir)
	return f, clk, spawns
}

// waitProc waits for a running helper whose PID is not notPID.
func waitProc(t *testing.T, w *Wake, notPID int) *wakeProc {
	t.Helper()
	var p *wakeProc
	ok := waitFor(t, 5*time.Second, func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		p = nil
		if w.sup != nil && w.sup.proc != nil && w.sup.proc.cmd.Process.Pid != notPID {
			p = w.sup.proc
		}
		return p != nil
	})
	if !ok {
		t.Fatalf("no helper running (status %+v)", w.Status())
	}
	return p
}

func gaveUp(w *Wake) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sup != nil && w.sup.gaveUp
}

// say makes the fake "detect" helper report one detection.
func say(t *testing.T, p *wakeProc) {
	t.Helper()
	if _, err := p.stdin.Write([]byte("hey awesome\n")); err != nil {
		t.Fatal(err)
	}
}

// exited reports whether the helper process has exited and been reaped.
func exited(p *wakeProc) bool {
	select {
	case <-p.done:
		return p.cmd.ProcessState != nil
	default:
		return false
	}
}

func TestWakeReportsDetection(t *testing.T) {
	f, clk, _ := newSupFixture(t, "detect")
	var wakes atomic.Int32
	f.cfg.OnWake = func() { wakes.Add(1) }
	w := NewWake(f.cfg)
	defer w.Close()
	if err := w.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	p := waitProc(t, w, 0)
	if st := w.Status(); st.State != "starting" {
		t.Errorf("state before 2 s = %q, want starting", st.State)
	}
	say(t, p)
	if !waitFor(t, 5*time.Second, func() bool { return wakes.Load() == 1 }) {
		t.Fatalf("OnWake calls = %d, want 1", wakes.Load())
	}
	clk.Advance(2500 * time.Millisecond)
	st := waitWake(t, w, func(s WakeStatus) bool { return s.State == "listening" })
	if st.Error != "" {
		t.Errorf("error = %q", st.Error)
	}
	time.Sleep(50 * time.Millisecond)
	if n := wakes.Load(); n != 1 {
		t.Errorf("OnWake calls = %d, want 1", n)
	}
}

func TestWakeSuppressed(t *testing.T) {
	f, _, _ := newSupFixture(t, "detect")
	var wakes, asked atomic.Int32
	f.cfg.OnWake = func() { wakes.Add(1) }
	f.cfg.Suppress = func() bool { asked.Add(1); return true }
	w := NewWake(f.cfg)
	defer w.Close()
	if err := w.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	say(t, waitProc(t, w, 0))
	if !waitFor(t, 5*time.Second, func() bool { return asked.Load() == 1 }) {
		t.Fatal("Suppress was never asked")
	}
	time.Sleep(50 * time.Millisecond)
	if n := wakes.Load(); n != 0 {
		t.Errorf("OnWake calls = %d, want 0", n)
	}
}

func TestWakeCooldowns(t *testing.T) {
	f, clk, _ := newSupFixture(t, "detect")
	// Large steps of the fake clock would look like a resume between 20 ms
	// ticks; this test is about cooldowns only.
	f.cfg.Tick = time.Hour
	var wakes, seen atomic.Int32
	f.cfg.OnWake = func() { wakes.Add(1) }
	f.cfg.Suppress = func() bool { seen.Add(1); return false } // counts detections
	w := NewWake(f.cfg)
	defer w.Close()
	if err := w.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	p := waitProc(t, w, 0)
	detect := func(n, wantWakes int32) {
		t.Helper()
		say(t, p)
		if !waitFor(t, 5*time.Second, func() bool { return seen.Load() == n }) {
			t.Fatalf("detection %d never seen", n)
		}
		time.Sleep(30 * time.Millisecond)
		if got := wakes.Load(); got != wantWakes {
			t.Fatalf("after detection %d: OnWake calls = %d, want %d", n, got, wantWakes)
		}
	}
	detect(1, 1)
	clk.Advance(time.Second)
	detect(2, 1) // 1 s after the last wake
	clk.Advance(5 * time.Second)
	w.SessionEnded()
	clk.Advance(time.Second)
	detect(3, 1) // 1 s after a session ended
	clk.Advance(3 * time.Second)
	detect(4, 2) // 4 s after it
}

// recordStates collects the states and errors pushed to OnStatus.
func recordStates(cfg *WakeConfig) func() []WakeStatus {
	var mu sync.Mutex
	var seen []WakeStatus
	cfg.OnStatus = func(s WakeStatus) { mu.Lock(); seen = append(seen, s); mu.Unlock() }
	return func() []WakeStatus {
		mu.Lock()
		defer mu.Unlock()
		return append([]WakeStatus(nil), seen...)
	}
}

func TestWakeRestartsWithBackoff(t *testing.T) {
	f, clk, _ := newSupFixture(t, "idle")
	f.cfg.Backoff = []time.Duration{200 * time.Millisecond, time.Second, time.Second}
	states := recordStates(&f.cfg)
	w := NewWake(f.cfg)
	defer w.Close()
	if err := w.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	p := waitProc(t, w, 0)
	killed := time.Now()
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	// While the restart is pending the state is "starting", without error.
	if !waitFor(t, 5*time.Second, func() bool { return !w.helperRunning() }) {
		t.Fatal("helper exit not noticed")
	}
	if st := w.Status(); st.State != "starting" || st.Error != "" {
		t.Errorf("during backoff: %+v", st)
	}
	p2 := waitProc(t, w, p.cmd.Process.Pid)
	if d := time.Since(killed); d < 200*time.Millisecond {
		t.Errorf("restarted after %v, want the 200 ms backoff first", d)
	}
	if !exited(p) {
		t.Error("old helper not reaped")
	}
	clk.Advance(2500 * time.Millisecond)
	waitWake(t, w, func(s WakeStatus) bool { return s.State == "listening" && s.Error == "" })
	if !w.helperRunning() || exited(p2) {
		t.Error("restarted helper should be running")
	}
	for _, s := range states() {
		if s.State == "error" || s.Error != "" {
			t.Errorf("emitted %+v before any give-up", s)
		}
	}
}

func TestWakeGivesUpAfterThreeExits(t *testing.T) {
	f, _, spawns := newSupFixture(t, "die")
	t.Setenv("FAKE_STDERR", "PortAudio error: Invalid device")
	states := recordStates(&f.cfg)
	w := NewWake(f.cfg)
	defer w.Close()
	if err := w.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, 5*time.Second, func() bool { return gaveUp(w) }) {
		t.Fatalf("never gave up; spawns %d, status %+v", spawns.Load(), w.Status())
	}
	st := w.Status()
	if st.State != "error" || !strings.Contains(st.Error, "Invalid device") || !st.Enabled {
		t.Errorf("status = %+v", st)
	}
	time.Sleep(200 * time.Millisecond)
	if n := spawns.Load(); n != 3 {
		t.Errorf("spawned %d helpers, want 3", n)
	}
	if w.helperRunning() {
		t.Error("no helper after giving up")
	}
	// "error" is emitted only once given up: nothing else follows it.
	seen := states()
	if len(seen) == 0 || seen[len(seen)-1].State != "error" || !strings.Contains(seen[len(seen)-1].Error, "Invalid device") {
		t.Fatalf("emitted %+v, want error last", seen)
	}
	first := len(seen) - 1
	for first > 0 && seen[first-1].State == "error" {
		first--
	}
	for _, s := range seen[:first] {
		if s.Error != "" {
			t.Errorf("emitted %+v before giving up", s)
		}
	}
}

func TestWakeBackoffSteps(t *testing.T) {
	f, clk, _ := newSupFixture(t, "idle")
	f.cfg.Tick = time.Hour // the fake clock moves in minutes; no resume here
	w := NewWake(f.cfg)
	defer w.Close()
	if err := w.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	// Exits 6 min apart: never 3 within 10 min, so it never gives up, and
	// the backoff still walks 1st, 2nd, 3rd step, then stays at the last.
	want := f.cfg.Backoff
	want = append(want, want[2], want[2])
	p := waitProc(t, w, 0)
	for i, d := range want {
		clk.Advance(6 * time.Minute)
		if err := p.cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		p = waitProc(t, w, p.cmd.Process.Pid)
		w.mu.Lock()
		got, up := w.sup.lastBackoff, w.sup.gaveUp
		w.mu.Unlock()
		if got != d || up {
			t.Fatalf("exit %d: backoff %v (gave up %v), want %v", i+1, got, up, d)
		}
	}
}

func TestWakeResumeAfterGiveUp(t *testing.T) {
	f, clk, spawns := newSupFixture(t, "die")
	t.Setenv("FAKE_STDERR", "PortAudio error: Invalid device")
	w := NewWake(f.cfg)
	defer w.Close()
	if err := w.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, 5*time.Second, func() bool { return gaveUp(w) }) {
		t.Fatalf("never gave up: %+v", w.Status())
	}
	clk.Advance(10 * time.Second) // resume
	if !waitFor(t, 5*time.Second, func() bool { return spawns.Load() >= 4 }) {
		t.Fatal("no spawn after a resume")
	}
	// A fresh period: three more exits before giving up again.
	if !waitFor(t, 5*time.Second, func() bool { return spawns.Load() == 6 && gaveUp(w) }) {
		t.Fatalf("spawns %d, gave up %v", spawns.Load(), gaveUp(w))
	}
	time.Sleep(100 * time.Millisecond)
	if n := spawns.Load(); n != 6 {
		t.Errorf("spawned %d, want 6", n)
	}
	if st := w.Status(); st.State != "error" || !strings.Contains(st.Error, "Invalid device") {
		t.Errorf("status = %+v", st)
	}
}

func TestWakeEnableStartFailure(t *testing.T) {
	f, _, _ := newSupFixture(t, "idle")
	var bin atomic.Value
	var spawns atomic.Int32
	bin.Store(filepath.Join(t.TempDir(), "missing-helper"))
	f.cfg.Bin = func(string) string { spawns.Add(1); return bin.Load().(string) }
	w := NewWake(f.cfg)
	defer w.Close()
	if err := w.SetEnabled(true); err != nil {
		t.Fatalf("SetEnabled(true) = %v, want nil (failure goes to the state)", err)
	}
	b, _ := os.ReadFile(filepath.Join(f.cfg.Dir, "wake.json"))
	if !strings.Contains(string(b), `"enabled":true`) {
		t.Errorf("wake.json = %s", b)
	}
	if !waitFor(t, 5*time.Second, func() bool { return gaveUp(w) }) {
		t.Fatalf("never gave up: %+v", w.Status())
	}
	if st := w.Status(); st.State != "error" || !strings.Contains(st.Error, "Couldn't start the wake word") {
		t.Errorf("status = %+v", st)
	}
	if n := spawns.Load(); n != 3 {
		t.Errorf("%d start attempts, want 3", n)
	}
	// Off and on again starts afresh.
	bin.Store(os.Args[0])
	if err := w.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if err := w.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	waitProc(t, w, 0)
	if st := w.Status(); st.State != "starting" || st.Error != "" {
		t.Errorf("after off/on: %+v", st)
	}
	if n := spawns.Load(); n != 4 {
		t.Errorf("%d start attempts, want 4", n)
	}
}

func TestWakeOffKillsHelper(t *testing.T) {
	f, _, _ := newSupFixture(t, "idle")
	w := NewWake(f.cfg)
	defer w.Close()
	if err := w.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	p := waitProc(t, w, 0)
	if err := w.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if !exited(p) {
		t.Error("helper still running when SetEnabled(false) returned")
	}
	if st := w.Status(); st.State != "off" || w.helperRunning() {
		t.Errorf("status = %+v running=%v", st, w.helperRunning())
	}
}

func TestWakeRestartsAfterClockJump(t *testing.T) {
	f, clk, spawns := newSupFixture(t, "idle")
	w := NewWake(f.cfg)
	defer w.Close()
	if err := w.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	p := waitProc(t, w, 0)
	clk.Advance(10 * time.Second)
	waitProc(t, w, p.cmd.Process.Pid)
	if !exited(p) {
		t.Error("old helper still running")
	}
	if n := spawns.Load(); n != 2 {
		t.Errorf("spawned %d, want 2", n)
	}
	if st := w.Status(); st.State == "error" {
		t.Errorf("a resume restart is not a failure: %+v", st)
	}
}

func TestWakeCloseKillsHelper(t *testing.T) {
	f, _, spawns := newSupFixture(t, "idle")
	w := NewWake(f.cfg)
	if err := w.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	p := waitProc(t, w, 0)
	w.Close()
	if !exited(p) || w.helperRunning() {
		t.Error("Close must kill and reap the helper")
	}
	time.Sleep(100 * time.Millisecond)
	if n := spawns.Load(); n != 1 || w.helperRunning() {
		t.Errorf("spawned %d after Close, want 1", n)
	}
	_ = w.SetEnabled(true)
	time.Sleep(50 * time.Millisecond)
	if w.helperRunning() {
		t.Error("SetEnabled after Close started a helper")
	}
}

// A helper whose mic delivers only zeros exits with the microphone line; after
// three exits the error state carries the Windows privacy hint.
func TestWakeSilentMicGivesUpWithHint(t *testing.T) {
	f, _, _ := newSupFixture(t, "die")
	line := "wake helper: microphone: the microphone delivers no sound (blocked in Settings › Privacy & security › Microphone, or muted)"
	t.Setenv("FAKE_STDERR", line)
	w := NewWake(f.cfg)
	defer w.Close()
	if err := w.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, 5*time.Second, func() bool { return gaveUp(w) }) {
		t.Fatalf("never gave up; status %+v", w.Status())
	}
	st := w.Status()
	if st.State != "error" || st.Error != line || st.Hint != hintWakeMicWindows {
		t.Errorf("status = %+v", st)
	}
	if hintWakeMicWindows != "Microphone access is blocked. On Windows: Settings › Privacy & security › Microphone › let desktop apps use it." {
		t.Errorf("hint %q differs from dictation's mic hint", hintWakeMicWindows)
	}
}

func TestWakeErrorHint(t *testing.T) {
	for _, c := range []struct {
		goos, err, hint string
	}{
		{"windows", "wake helper: microphone: the microphone stopped delivering audio", hintWakeMicWindows},
		{"windows", "wake helper: microphone: no microphone found", hintWakeMicWindows},
		{"windows", "wake helper: model: LoadLibrary failed", ""},
		{"linux", "PortAudio error: no microphone", ""},
	} {
		f, _, _ := newSupFixture(t, "idle")
		f.cfg.GOOS = c.goos
		w := NewWake(f.cfg)
		w.mu.Lock()
		w.cfg.Enabled = true
		w.sup = &wakeSup{gaveUp: true, errText: c.err}
		st := w.statusLocked()
		w.sup = nil
		w.mu.Unlock()
		if st.State != "error" || st.Error != c.err || st.Hint != c.hint {
			t.Errorf("%s %q: status = %+v, want hint %q", c.goos, c.err, st, c.hint)
		}
	}
}

// A keyword line retuned after an install reaches it when the helper starts.
func TestWakeRewritesStaleKeywords(t *testing.T) {
	f, _, _ := newSupFixture(t, "idle")
	kw := filepath.Join(f.cfg.Dir, "models", wakeModelDir, wakeKeywordsF)
	if err := os.WriteFile(kw, []byte("▁HE Y ▁A W E SO ME @HEY_AWESOME :1.5 #0.10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := NewWake(f.cfg)
	defer w.Close()
	if err := w.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	waitProc(t, w, 0)
	if b, _ := os.ReadFile(kw); string(b) != string(wakeKeywords) {
		t.Errorf("keywords = %q, want the embedded %q", b, wakeKeywords)
	}
}

func TestSyncKeywords(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, wakeKeywordsF)
	if err := syncKeywords(dir); err != nil { // missing: written
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != string(wakeKeywords) {
		t.Fatalf("missing file: got %q", b)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	if err := syncKeywords(dir); err != nil { // same: left alone
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); !st.ModTime().Equal(old) {
		t.Error("an up-to-date file was rewritten")
	}
	if err := os.WriteFile(p, []byte("old line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syncKeywords(dir); err != nil { // different: rewritten
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != string(wakeKeywords) {
		t.Errorf("stale file: got %q", b)
	}
}
