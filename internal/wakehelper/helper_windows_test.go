package wakehelper

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"awesome-reminder/internal/voice"
)

func TestMicErrorText(t *testing.T) {
	if s := micErrorText(mmsyserrBadDeviceID); !strings.Contains(strings.ToLower(s), "device") {
		t.Fatalf("micErrorText(MMSYSERR_BADDEVICEID) = %q, want it to mention the device", s)
	}
}

// The C API structs must match the v2.0.3 header's amd64 layout byte for
// byte (sizes and offsets worked out from the header by hand).
func TestCStructLayout(t *testing.T) {
	var c kwsConfig
	var r kwsResult
	checks := []struct {
		name      string
		got, want uintptr
	}{
		{"sizeof(SherpaOnnxOnlineModelConfig)", unsafe.Sizeof(c.Model), 136},
		{"offsetof(model_config.tokens)", unsafe.Offsetof(c.Model.Tokens), 48},
		{"offsetof(model_config.provider)", unsafe.Offsetof(c.Model.Provider), 64},
		{"offsetof(model_config.tokens_buf_size)", unsafe.Offsetof(c.Model.TokensBufSize), 112},
		{"offsetof(model_config.t_one_ctc)", unsafe.Offsetof(c.Model.TOneCtc), 128},
		{"offsetof(model_config)", unsafe.Offsetof(c.Model), 8},
		{"offsetof(max_active_paths)", unsafe.Offsetof(c.MaxActivePaths), 144},
		{"offsetof(keywords_score)", unsafe.Offsetof(c.KeywordsScore), 152},
		{"offsetof(keywords_file)", unsafe.Offsetof(c.KeywordsFile), 160},
		{"offsetof(keywords_buf_size)", unsafe.Offsetof(c.KeywordsBufSize), 176},
		{"sizeof(SherpaOnnxKeywordSpotterConfig)", unsafe.Sizeof(c), 184},
		{"offsetof(result.count)", unsafe.Offsetof(r.Count), 24},
		{"offsetof(result.timestamps)", unsafe.Offsetof(r.Timestamps), 32},
		{"offsetof(result.start_time)", unsafe.Offsetof(r.StartTime), 40},
		{"offsetof(result.json)", unsafe.Offsetof(r.JSON), 48},
		{"sizeof(SherpaOnnxKeywordResult)", unsafe.Sizeof(r), 56},
		{"sizeof(WAVEHDR)", unsafe.Sizeof(waveHdr{}), 48},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

func TestReadWavRejectsOtherFormats(t *testing.T) {
	if _, err := readWav(filepath.Join("testdata", "hey-awesome.wav")); err != nil {
		t.Fatalf("readWav(hey-awesome.wav): %v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad.wav")
	if err := os.WriteFile(bad, []byte("RIFF\x04\x00\x00\x00WAVE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readWav(bad); err == nil {
		t.Fatal("readWav accepted a file without fmt/data chunks")
	}
}

func TestRunBadFlags(t *testing.T) {
	var errOut bytes.Buffer
	if code := run([]string{"--lib-dir", "x"}, nil, &errOut); code != 2 {
		t.Fatalf("missing model flags: exit %d, want 2 (stderr %q)", code, errOut.String())
	}
}

func TestRunModelLoadFailure(t *testing.T) {
	var errOut bytes.Buffer
	missing := filepath.Join(t.TempDir(), "nope")
	code := run([]string{"--lib-dir", missing, "--encoder=" + missing, "--decoder=" + missing,
		"--joiner=" + missing, "--tokens=" + missing, "--keywords-file=" + missing}, nil, &errOut)
	if code != 4 || !strings.HasPrefix(errOut.String(), "wake helper: model: ") {
		t.Fatalf("exit %d, stderr %q; want 4 and a 'wake helper: model:' line", code, errOut.String())
	}
}

// kwsEnv returns the engine and model folders, or skips.
func kwsEnv(t *testing.T) (lib, model string) {
	lib, model = os.Getenv("AR_KWS_LIB"), os.Getenv("AR_KWS_MODEL")
	if lib == "" || model == "" {
		t.Skip("set AR_KWS_LIB (folder with sherpa-onnx-c-api.dll) and AR_KWS_MODEL (KWS model folder)")
	}
	return lib, model
}

func helperArgs(lib, model string, extra ...string) []string {
	m := func(f string) string { return filepath.Join(model, f) }
	kw, _ := filepath.Abs(filepath.Join("..", "voice", "wake_keywords.txt"))
	return append([]string{"--lib-dir", lib,
		"--encoder=" + m("encoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx"),
		"--decoder=" + m("decoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx"),
		"--joiner=" + m("joiner-epoch-12-avg-2-chunk-16-left-64.int8.onnx"),
		"--tokens=" + m("tokens.txt"),
		"--keywords-file=" + kw,
		"--num-threads=1"}, extra...)
}

func detectionLines(out string) (n int) {
	for _, l := range strings.Split(out, "\n") {
		if voice.ParseDetection(l) {
			n++
		}
	}
	return n
}

func TestHelperDetectsFromWav(t *testing.T) {
	lib, model := kwsEnv(t)
	for _, tc := range []struct {
		file string
		want int
	}{{"hey-awesome.wav", 1}, {"negative.wav", 0}} {
		t.Run(tc.file, func(t *testing.T) {
			wav := filepath.Join("testdata", tc.file)
			samples, err := readWav(wav)
			if err != nil {
				t.Fatal(err)
			}
			var errOut bytes.Buffer
			cpu0, _ := procStats(t)
			start := time.Now()
			code := run(helperArgs(lib, model, "--wav", wav), nil, &errOut)
			wall := time.Since(start)
			cpu1, peak := procStats(t)
			if code != 0 {
				t.Fatalf("exit %d, stderr:\n%s", code, errOut.String())
			}
			t.Logf("stderr:\n%s", errOut.String())
			if got := detectionLines(errOut.String()); got != tc.want {
				t.Fatalf("%d detection lines, want %d", got, tc.want)
			}
			if tc.want > 0 && !strings.HasPrefix(errOut.String(), "0:{") {
				t.Fatalf("first detection does not start with 0:{ : %q", errOut.String())
			}
			audio := time.Duration(len(samples)) * time.Second / rate
			t.Logf("audio %v, wall %v (incl. model load), CPU %v, peak working set %.1f MB, %.1f s fed past the gate, %d gate resets",
				audio.Round(time.Millisecond), wall.Round(time.Millisecond), (cpu1 - cpu0).Round(time.Millisecond), float64(peak)/(1<<20),
				float64(lastStats.fedSamples)/rate, lastStats.resets)
		})
	}
}

// TestManualMic runs the real mic loop (AR_KWS_MIC=1) for AR_KWS_MIC_SECONDS
// (default 300) and samples this process's CPU and working set.
func TestManualMic(t *testing.T) {
	if os.Getenv("AR_KWS_MIC") != "1" {
		t.Skip("set AR_KWS_MIC=1 (and AR_KWS_LIB, AR_KWS_MODEL) to measure the mic loop")
	}
	lib, model := kwsEnv(t)
	secs := 300
	if s, err := strconv.Atoi(os.Getenv("AR_KWS_MIC_SECONDS")); err == nil && s > 0 {
		secs = s
	}
	if n := waveInGetNumDevs(); n == 0 {
		t.Skip("no capture device: waveInGetNumDevs() == 0")
	}
	stdinR, stdinW := io.Pipe()
	var errOut lockedBuffer
	done := make(chan int, 1)
	go func() { done <- run(helperArgs(lib, model), stdinR, &errOut) }()

	const settle = 5 * time.Second // model load and mic start
	select {
	case code := <-done:
		t.Skipf("helper exited %d before listening (no usable capture device?): %s", code, errOut.String())
	case <-time.After(settle):
	}
	cpu0, _ := procStats(t)
	ws0 := workingSet(t)
	wsMax := ws0
	start := time.Now()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for time.Since(start) < time.Duration(secs)*time.Second {
		select {
		case code := <-done:
			t.Fatalf("helper exited %d during the run: %s", code, errOut.String())
		case <-tick.C:
			wsMax = max(wsMax, workingSet(t))
		}
	}
	cpu1, peak := procStats(t)
	wall := time.Since(start)
	stdinW.Close() // the app's pipe closing: the helper exits 0
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d after stdin EOF: %s", code, errOut.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not exit after stdin EOF")
	}
	st := lastStats
	t.Logf("mic loop %v: CPU %v = %.2f %% of one core; working set start %.1f MB, max %.1f MB, peak %.1f MB; "+
		"%d blocks (%.1f s of audio), %.1f s fed past the gate, %d gate resets, detections %d",
		wall.Round(time.Second), (cpu1 - cpu0).Round(time.Millisecond),
		100*float64(cpu1-cpu0)/float64(wall), float64(ws0)/(1<<20), float64(wsMax)/(1<<20), float64(peak)/(1<<20),
		st.blocks, float64(st.blocks)/10, float64(st.fedSamples)/rate, st.resets, detectionLines(errOut.String()))
}

// lockedBuffer is a bytes.Buffer the helper's goroutine writes while the
// test may read it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// procStats returns this process's CPU time (user + kernel) and peak
// working set.
func procStats(t *testing.T) (time.Duration, uint64) {
	t.Helper()
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &c, &e, &k, &u); err != nil {
		t.Fatal(err)
	}
	ft := func(f windows.Filetime) time.Duration {
		return time.Duration(uint64(f.HighDateTime)<<32|uint64(f.LowDateTime)) * 100
	}
	return ft(k) + ft(u), memCounters(t).PeakWorkingSetSize
}

func workingSet(t *testing.T) uint64 { return memCounters(t).WorkingSetSize }

type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uint64
	WorkingSetSize             uint64
	QuotaPeakPagedPoolUsage    uint64
	QuotaPagedPoolUsage        uint64
	QuotaPeakNonPagedPoolUsage uint64
	QuotaNonPagedPoolUsage     uint64
	PagefileUsage              uint64
	PeakPagefileUsage          uint64
}

var procGetProcessMemoryInfo = windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

func memCounters(t *testing.T) processMemoryCounters {
	t.Helper()
	var m processMemoryCounters
	m.CB = uint32(unsafe.Sizeof(m))
	if r, _, err := procGetProcessMemoryInfo.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&m)), uintptr(m.CB)); r == 0 {
		t.Fatal(err)
	}
	return m
}
