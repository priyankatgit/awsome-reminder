package voice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain doubles as a fake whisper.cpp: when FAKE_WHISPER is set the test
// binary behaves like whisper-server ("server"), whisper-cli ("cli") or a
// binary that dies at once ("crash"), so engine tests never need the real
// engine.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_KWS"); mode != "" {
		fakeKWS(mode)
		os.Exit(0)
	}
	switch os.Getenv("FAKE_WHISPER") {
	case "":
		os.Exit(m.Run())
	case "server":
		fakeServer()
	case "cli":
		fakeCLI()
	case "crash":
		fmt.Fprintln(os.Stderr, "error: failed to load model")
		os.Exit(3)
	}
	os.Exit(0)
}

// fakeDetectionJSON is a detection as the stock spotter prints it (Task 0
// notes, section 10).
const fakeDetectionJSON = `{"start_time":0.00, "keyword": "HEY_AWESOME", "timestamps": [3.28, 3.56, 3.84, 3.96, 4.00, 4.04, 4.08], "tokens":[" HE", "Y", " A", "W", "ES", "OM", "E"]}`

// fakeKWSStartup is the stock microphone spotter's start-up output (stderr).
const fakeKWSStartup = "/project/sherpa-onnx/csrc/parse-options.cc:Read:379 'sherpa-onnx-keyword-spotter-microphone --tokens=tokens.txt'\n" +
	"\n" +
	"KeywordSpotterConfig(feat_config=FeatureExtractorConfig(sampling_rate=16000, feature_dim=80), max_active_paths=4, num_trailing_blanks=1, keywords_score=1, keywords_threshold=0.25)\n" +
	"Num devices: 1\n" +
	" * 0 default\n" +
	"Use device: 0\n" +
	"  Name: default\n" +
	"  Max input channels: 2\n"

// fakeKWS is the fake wake helper. "idle" prints the start-up lines and runs
// until stdin closes; "detect" also writes one detection to stderr, in the
// Linux form (no trailing newline), per line read from stdin; "die" prints
// $FAKE_STDERR and exits 1 at once.
func fakeKWS(mode string) {
	switch mode {
	case "die":
		fmt.Fprintln(os.Stderr, os.Getenv("FAKE_STDERR"))
		os.Exit(1)
	case "idle":
		fmt.Fprint(os.Stderr, fakeKWSStartup)
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "detect":
		fmt.Fprint(os.Stderr, fakeKWSStartup)
		sc := bufio.NewScanner(os.Stdin)
		for n := 0; sc.Scan(); n++ {
			if n == 0 {
				fmt.Fprintf(os.Stderr, "\r%d:%s", n, fakeDetectionJSON)
			} else {
				fmt.Fprintf(os.Stderr, "\n\r%d:%s", n, fakeDetectionJSON)
			}
		}
	}
}

func argAfter(flag string) string {
	for i, a := range os.Args {
		if a == flag && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
	}
	return ""
}

func fakeServer() {
	if f := os.Getenv("FAKE_PIDFILE"); f != "" {
		pf, _ := os.OpenFile(f, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		fmt.Fprintln(pf, os.Getpid())
		pf.Close()
	}
	if ms := os.Getenv("FAKE_LOAD_MS"); ms != "" {
		var n int
		fmt.Sscan(ms, &n)
		time.Sleep(time.Duration(n) * time.Millisecond)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/inference", func(w http.ResponseWriter, r *http.Request) {
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, `{"error":"no file"}`, http.StatusBadRequest)
			return
		}
		head := make([]byte, 4)
		_, _ = io.ReadFull(f, head)
		if string(head) != "RIFF" || r.FormValue("response_format") != "json" {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		if os.Getenv("FAKE_ECHO_PROMPT") == "1" {
			_ = json.NewEncoder(w).Encode(map[string]string{"text": r.FormValue("prompt")})
			return
		}
		if os.Getenv("FAKE_ECHO_PARAMS") == "1" {
			var fields []string
			for _, k := range []string{"language", "beam_size", "best_of", "temperature", "temperature_inc", "no_speech_thold", "suppress_nst"} {
				fields = append(fields, k+"="+r.FormValue(k))
			}
			launch := []string{"-l=" + argAfter("-l"), "-bs=" + argAfter("-bs"), "-bo=" + argAfter("-bo")}
			for _, a := range os.Args {
				if a == "-sns" {
					launch = append(launch, "-sns")
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"text": strings.Join(fields, " ") + " | " + strings.Join(launch, " ")})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"text": " hello world"})
	})
	_ = http.ListenAndServe(argAfter("--host")+":"+argAfter("--port"), mux)
}

func fakeCLI() {
	b, err := os.ReadFile(argAfter("-f"))
	if err != nil || !bytes.HasPrefix(b, []byte("RIFF")) || argAfter("-m") == "" {
		fmt.Fprintln(os.Stderr, "bad input")
		os.Exit(2)
	}
	if os.Getenv("FAKE_ECHO_PROMPT") == "1" {
		fmt.Println(argAfter("--prompt"))
		return
	}
	fmt.Println(" hello world")
}

func fakeConfig(t *testing.T, mode string, extraEnv ...string) (EngineConfig, string) {
	t.Helper()
	pidfile := filepath.Join(t.TempDir(), "pids")
	return EngineConfig{
		Bin:     os.Args[0],
		Model:   "model.bin",
		Threads: 2,
		Env:     append([]string{"FAKE_WHISPER=" + mode, "FAKE_PIDFILE=" + pidfile}, extraEnv...),
	}, pidfile
}

func pids(t *testing.T, pidfile string) []string {
	t.Helper()
	b, _ := os.ReadFile(pidfile)
	return strings.Fields(string(b))
}

var testWAV = EncodeWAV(make([]int16, MinSamples))

func TestServerEngineTranscribes(t *testing.T) {
	c, _ := fakeConfig(t, "server")
	e := NewServerEngine(c)
	defer e.Close()
	got, err := e.Transcribe(context.Background(), testWAV, "")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if got != " hello world" {
		t.Errorf("text = %q", got)
	}
	if !e.Running() {
		t.Error("server should keep running until idle")
	}
}

func TestServerEngineWarmReusesProcess(t *testing.T) {
	c, pidfile := fakeConfig(t, "server")
	e := NewServerEngine(c)
	defer e.Close()
	ctx := context.Background()
	if err := e.Warm(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Warm(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Transcribe(ctx, testWAV, ""); err != nil {
		t.Fatal(err)
	}
	if n := len(pids(t, pidfile)); n != 1 {
		t.Errorf("started %d processes, want 1", n)
	}
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

func TestServerEngineIdleExit(t *testing.T) {
	c, _ := fakeConfig(t, "server")
	c.Idle = 300 * time.Millisecond
	e := NewServerEngine(c)
	defer e.Close()
	if _, err := e.Transcribe(context.Background(), testWAV, ""); err != nil {
		t.Fatal(err)
	}
	base := e.lastBase()
	if !waitFor(t, 3*time.Second, func() bool { return !e.Running() }) {
		t.Fatal("server still running after idle")
	}
	if _, err := http.Get(base + "/health"); err == nil {
		t.Error("server process still answers after idle exit")
	}
}

func TestServerEngineRestartsAfterCrash(t *testing.T) {
	c, pidfile := fakeConfig(t, "server")
	e := NewServerEngine(c)
	defer e.Close()
	ctx := context.Background()
	if _, err := e.Transcribe(ctx, testWAV, ""); err != nil {
		t.Fatal(err)
	}
	e.killForTest()
	got, err := e.Transcribe(ctx, testWAV, "")
	if err != nil || got != " hello world" {
		t.Fatalf("after crash: %q, %v", got, err)
	}
	if p := pids(t, pidfile); len(p) != 2 || p[0] == p[1] {
		t.Errorf("pids = %v, want two different processes", p)
	}
}

func TestServerEngineStartFailure(t *testing.T) {
	c, _ := fakeConfig(t, "crash")
	e := NewServerEngine(c)
	defer e.Close()
	start := time.Now()
	err := e.Warm(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "failed to load model") {
		t.Errorf("error should carry stderr: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v, want an early failure", time.Since(start))
	}
	if e.Running() {
		t.Error("Running after failed start")
	}
}

func TestServerEngineStartTimeout(t *testing.T) {
	c, _ := fakeConfig(t, "server", "FAKE_LOAD_MS=5000")
	c.StartTimeout = 300 * time.Millisecond
	e := NewServerEngine(c)
	defer e.Close()
	if err := e.Warm(context.Background()); err == nil {
		t.Fatal("want timeout error")
	}
	if e.Running() {
		t.Error("process should be killed after the start timeout")
	}
}

func TestServerEngineClose(t *testing.T) {
	c, _ := fakeConfig(t, "server")
	e := NewServerEngine(c)
	if _, err := e.Transcribe(context.Background(), testWAV, ""); err != nil {
		t.Fatal(err)
	}
	e.Close()
	if e.Running() {
		t.Error("Running after Close")
	}
}

func TestCLIEngineTranscribes(t *testing.T) {
	c, _ := fakeConfig(t, "cli")
	tmp := t.TempDir()
	t.Setenv("TMP", tmp)
	t.Setenv("TMPDIR", tmp)
	e := NewCLIEngine(c)
	got, err := e.Transcribe(context.Background(), testWAV, "")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if got != " hello world\n" {
		t.Errorf("text = %q", got)
	}
	if left := listDir(t, tmp); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestThreads(t *testing.T) {
	for _, c := range [][2]int{{1, 1}, {2, 1}, {8, 4}, {32, 8}} {
		if got := DefaultThreads(c[0]); got != c[1] {
			t.Errorf("DefaultThreads(%d) = %d, want %d", c[0], got, c[1])
		}
	}
}

func TestServerEngineSendsPrompt(t *testing.T) {
	c, _ := fakeConfig(t, "server", "FAKE_ECHO_PROMPT=1")
	e := NewServerEngine(c)
	defer e.Close()
	got, err := e.Transcribe(context.Background(), testWAV, "new task")
	if err != nil || got != "new task" {
		t.Errorf("with prompt: %q, %v", got, err)
	}
	// No prompt: no prompt part at all.
	got, err = e.Transcribe(context.Background(), testWAV, "")
	if err != nil || got != "" {
		t.Errorf("empty prompt: %q, %v", got, err)
	}
	c2, _ := fakeConfig(t, "server")
	e2 := NewServerEngine(c2)
	defer e2.Close()
	got, err = e2.Transcribe(context.Background(), testWAV, "")
	if err != nil || got != " hello world" {
		t.Errorf("plain: %q, %v", got, err)
	}
}

func TestCLIEngineSendsPrompt(t *testing.T) {
	c, _ := fakeConfig(t, "cli", "FAKE_ECHO_PROMPT=1")
	e := NewCLIEngine(c)
	got, err := e.Transcribe(context.Background(), testWAV, "new task")
	if err != nil || strings.TrimSpace(got) != "new task" {
		t.Errorf("with prompt: %q, %v", got, err)
	}
	got, err = e.Transcribe(context.Background(), testWAV, "")
	if err != nil || strings.TrimSpace(got) != "" {
		t.Errorf("empty prompt: %q, %v", got, err)
	}
}

func TestServerEngineDecodingParams(t *testing.T) {
	c, _ := fakeConfig(t, "server", "FAKE_ECHO_PARAMS=1")
	e := NewServerEngine(c)
	defer e.Close()
	got, err := e.Transcribe(context.Background(), testWAV, "")
	want := "language=en beam_size=5 best_of=5 temperature=0 temperature_inc=0.2 no_speech_thold=0.6 suppress_nst=true | -l=en -bs=5 -bo=5 -sns"
	if err != nil || got != want {
		t.Errorf("params = %q, %v; want %q", got, err, want)
	}
}
