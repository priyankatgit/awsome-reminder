package voice

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	wakeKeyword = "HEY_AWESOME"
	// The stock spotter prints no ready line, so a helper alive this long
	// counts as listening.
	wakeListenAfter     = 2 * time.Second
	wakeMaxExits        = 3 // unexpected exits within wakeExitWindow → error
	wakeExitWindow      = 10 * time.Minute
	wakeClockJump       = 5 * time.Second // a wall-clock jump this big between ticks is a resume
	wakeCooldown        = 3 * time.Second // since the last reported wake
	wakeSessionCooldown = 2 * time.Second // since a dictation session ended
	wakeErrMax          = 512             // bytes of the helper's stderr line kept for the error state
	helperLineMax       = 64 << 10        // longer output lines are cut
	stockSpotter        = "sherpa-onnx-keyword-spotter-microphone"
)

// helperCommand returns the wake helper program and its arguments. On Linux
// and macOS it is the stock microphone spotter in engineDir with the Task 0
// argument list; on Windows it is the app's own executable (exe) in helper
// mode, which loads the engine libraries from engineDir (Task 4b).
func helperCommand(goos, exe, engineDir, modelDir, keywords string) (bin string, args []string) {
	sep := "/"
	if goos == "windows" {
		sep = `\`
	}
	join := func(dir, f string) string { return strings.TrimRight(dir, `/\`) + sep + f }
	tokens := "--tokens=" + join(modelDir, "tokens.txt")
	encoder := "--encoder=" + join(modelDir, "encoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx")
	decoder := "--decoder=" + join(modelDir, "decoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx")
	joiner := "--joiner=" + join(modelDir, "joiner-epoch-12-avg-2-chunk-16-left-64.int8.onnx")
	kw := "--keywords-file=" + keywords
	if goos == "windows" {
		return exe, []string{"--wake-helper", "--lib-dir", engineDir,
			encoder, decoder, joiner, tokens, kw, "--num-threads=1"}
	}
	return join(engineDir, stockSpotter), []string{
		tokens, encoder, decoder, joiner, kw, "--num-threads=1", "--provider=cpu"}
}

// scanHelperOutput reads the helper's output as a byte stream. Each
// detection object ({"start_time": …, "keyword": …}, one line, no nested
// braces) goes to onDetect as soon as its closing brace arrives, because the
// stock program on Linux and macOS ends a detection with no newline (`\r0:{…}`,
// then `\n\r1:{…}`). The "<n>:" in front of it is dropped. Every other
// non-empty line (split on \n or \r) goes to onLine. It returns at EOF or on
// a read error.
func scanHelperOutput(r io.Reader, onDetect func(keyword string), onLine func(line string)) {
	var line []byte
	obj := -1 // index of the '{' that opened a possible detection object
	flush := func() {
		if strings.TrimSpace(string(line)) != "" {
			onLine(string(line))
		}
		line, obj = line[:0], -1
	}
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		for _, b := range buf[:n] {
			if b == '\n' || b == '\r' {
				flush()
				continue
			}
			if len(line) >= helperLineMax {
				continue // drop the rest of an overlong line
			}
			line = append(line, b)
			switch {
			case b == '{':
				obj = len(line) - 1
			case b == '}' && obj >= 0:
				if kw, ok := detectionKeyword(line[obj:]); ok {
					onDetect(kw)
					line = line[:0]
				}
				obj = -1
			}
		}
		if err != nil {
			flush()
			return
		}
	}
}

// detectionKeyword parses one detection object.
func detectionKeyword(b []byte) (string, bool) {
	var d struct {
		StartTime *float64 `json:"start_time"`
		Keyword   string   `json:"keyword"`
	}
	if json.Unmarshal(b, &d) != nil || d.StartTime == nil || d.Keyword == "" {
		return "", false
	}
	return d.Keyword, true
}

// parseDetection reports whether s holds a "Hey Awesome" detection object.
func parseDetection(s string) bool {
	found := false
	scanHelperOutput(strings.NewReader(s), func(k string) { found = found || k == wakeKeyword }, func(string) {})
	return found
}

// capLine trims a stderr line and cuts it to wakeErrMax bytes on a rune
// boundary.
func capLine(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= wakeErrMax {
		return s
	}
	i := wakeErrMax
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}

// wakeSup supervises the helper for one enabled period. stop and done are
// fixed at creation; the other fields are guarded by Wake.mu.
type wakeSup struct {
	stop chan struct{} // closed under Wake.mu, together with clearing Wake.sup
	done chan struct{} // closed when the supervisor returned and its helper is gone

	proc    *wakeProc // the running helper; nil between runs
	errText string    // the last failure: shown during backoff and after giving up
	gaveUp  bool
}

// wakeProc is one running helper process. The supervisor that started it
// is its only owner and kills and reaps it before returning.
type wakeProc struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	started  time.Time     // Wake.c.Now() at spawn
	done     chan struct{} // closed when the process has exited and been reaped
	lastLine string        // last non-empty stderr line, capped (Wake.mu)
}

func (p *wakeProc) kill() {
	_ = p.stdin.Close()
	_ = p.cmd.Process.Kill()
	<-p.done
}

// startSupervisorLocked starts supervision unless it is already running or
// Wake is closed. A supervisor that gave up is replaced, which resets the
// failure count. The new supervisor waits for the previous one to finish,
// so there is never more than one helper.
func (w *Wake) startSupervisorLocked() {
	if w.closed || (w.sup != nil && !w.sup.gaveUp) {
		return
	}
	prev := w.supDone
	s := &wakeSup{stop: make(chan struct{}), done: make(chan struct{})}
	w.sup, w.supDone = s, s.done
	go w.supervise(s, prev)
}

// stopSupervisorLocked ends supervision. The caller waits on the returned
// channel (if not nil) after unlocking: once it is closed, no helper runs.
func (w *Wake) stopSupervisorLocked() <-chan struct{} {
	if s := w.sup; s != nil {
		w.sup = nil
		close(s.stop)
	}
	return w.supDone
}

// helperStateLocked is the state and error while enabled and installed.
func (w *Wake) helperStateLocked() (state, errText string) {
	s := w.sup
	switch {
	case s == nil:
		return "off", "" // before Start, or after Close
	case s.gaveUp:
		return "error", s.errText
	case s.proc != nil:
		if w.c.Now().Sub(s.proc.started) >= wakeListenAfter {
			return "listening", ""
		}
		return "starting", ""
	case s.errText != "":
		return "error", s.errText // waiting out the backoff; a restart follows
	default:
		return "starting", "" // the first spawn is a moment away
	}
}

func (w *Wake) helperRunning() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sup != nil && w.sup.proc != nil
}

// supervise owns the helper for one enabled period: it spawns it, restarts
// it with backoff after unexpected exits, gives up after wakeMaxExits within
// wakeExitWindow, restarts it after a wall-clock jump (resume), and kills
// and reaps it before returning when s.stop closes.
func (w *Wake) supervise(s *wakeSup, prev <-chan struct{}) {
	defer close(s.done)
	if prev != nil {
		<-prev // the previous period's helper is gone
	}
	ticker := time.NewTicker(w.c.Tick)
	defer ticker.Stop()
	var (
		p      *wakeProc
		exits  []time.Time // unexpected exits within the window
		retry  *time.Timer
		retryC <-chan time.Time
		shown  [2]string // last state and error pushed to OnStatus
	)
	defer func() {
		if retry != nil {
			retry.Stop()
		}
	}()
	changed := func() {
		w.mu.Lock()
		st, e := w.helperStateLocked()
		w.mu.Unlock()
		if [2]string{st, e} != shown {
			shown = [2]string{st, e}
			w.emit(true)
		}
	}
	// failed records an unexpected exit or a failed start; true means give up.
	failed := func(reason string) bool {
		now := w.c.Now()
		kept := exits[:0]
		for _, t := range exits {
			if now.Sub(t) < wakeExitWindow {
				kept = append(kept, t)
			}
		}
		exits = append(kept, now)
		n := len(exits)
		w.mu.Lock()
		s.proc, s.errText, s.gaveUp = nil, reason, n >= wakeMaxExits
		w.mu.Unlock()
		if n >= wakeMaxExits {
			log.Printf("wake: helper gave up: %s", reason)
			return true
		}
		d := w.c.Backoff[min(n, len(w.c.Backoff))-1]
		log.Printf("wake: helper restarting in %v: %s", d, reason)
		retry = time.NewTimer(d)
		retryC = retry.C
		return false
	}
	start := func() bool {
		var reason string
		p, reason = w.spawn(s)
		return p == nil && reason != "" && failed(reason)
	}

	lastTick := w.c.Now().Round(0) // wall clock, no monotonic reading
	if start() {
		changed()
		return
	}
	for {
		changed()
		var exitC <-chan struct{}
		if p != nil {
			exitC = p.done
		}
		select {
		case <-s.stop:
			if p != nil {
				p.kill()
				w.mu.Lock()
				s.proc = nil
				w.mu.Unlock()
				log.Printf("wake: helper stopped")
			}
			return
		case <-exitC:
			w.mu.Lock()
			reason := p.lastLine
			w.mu.Unlock()
			if reason == "" {
				reason = capLine("The wake word helper stopped (" + p.cmd.ProcessState.String() + ").")
			}
			p = nil
			if failed(reason) {
				changed()
				return
			}
		case <-retryC:
			retryC = nil
			if start() {
				changed()
				return
			}
		case <-ticker.C:
			now := w.c.Now().Round(0)
			jumped := now.Sub(lastTick) > w.c.Tick+wakeClockJump
			lastTick = now
			if !jumped {
				continue
			}
			log.Printf("wake: helper restarting: clock jump (resume)")
			if p != nil {
				p.kill()
				w.mu.Lock()
				s.proc = nil
				w.mu.Unlock()
			}
			if retry != nil {
				retry.Stop()
				retryC = nil
			}
			if start() {
				changed()
				return
			}
		}
	}
}

// spawn starts a helper for s. It returns nil and the reason when the start
// failed, and nil and "" when s was stopped or Wake closed. It holds w.mu
// across the start, so no helper starts after stopSupervisorLocked.
func (w *Wake) spawn(s *wakeSup) (*wakeProc, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.sup != s {
		return nil, ""
	}
	fail := func(err error) (*wakeProc, string) {
		return nil, capLine("Couldn't start the wake word: " + err.Error())
	}
	exe := ""
	if w.c.GOOS == "windows" && w.c.Bin == nil {
		var err error
		if exe, err = os.Executable(); err != nil {
			return fail(err)
		}
	}
	bin, args := helperCommand(w.c.GOOS, exe, w.engineDir(), w.modelDir(), filepath.Join(w.modelDir(), wakeKeywordsF))
	if w.c.Bin != nil {
		bin = w.c.Bin(w.engineDir())
	}
	cmd := exec.Command(bin, args...)
	hideAndContain(cmd)
	// The stock program's libraries sit next to it (Extract flattens).
	switch w.c.GOOS {
	case "linux":
		cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+w.engineDir())
	case "darwin":
		cmd.Env = append(os.Environ(), "DYLD_LIBRARY_PATH="+w.engineDir())
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fail(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fail(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return fail(err)
	}
	if err := cmd.Start(); err != nil {
		return fail(err)
	}
	release := afterStart(cmd.Process)
	p := &wakeProc{cmd: cmd, stdin: stdin, started: w.c.Now(), done: make(chan struct{})}
	go func() {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			scanHelperOutput(stderr, func(kw string) { w.detected(s, kw) }, func(l string) {
				w.mu.Lock()
				p.lastLine = capLine(l)
				w.mu.Unlock()
			})
		}()
		go func() {
			defer wg.Done()
			// Nothing is expected on stdout; drain it so the helper never blocks.
			scanHelperOutput(stdout, func(string) {}, func(string) {})
		}()
		wg.Wait() // all output read before Wait closes the pipes
		_ = cmd.Wait()
		release()
		close(p.done)
	}()
	s.proc = p
	log.Printf("wake: helper started: pid %d", cmd.Process.Pid)
	return p, ""
}

// detected handles one detection: OnWake runs (on its own goroutine) unless
// Suppress says so, the last wake was under wakeCooldown ago, or a
// dictation session ended under wakeSessionCooldown ago.
func (w *Wake) detected(s *wakeSup, keyword string) {
	if keyword != wakeKeyword {
		return
	}
	if w.c.Suppress != nil && w.c.Suppress() {
		return
	}
	w.mu.Lock()
	now := w.c.Now()
	report := w.sup == s &&
		(w.lastWake.IsZero() || now.Sub(w.lastWake) >= wakeCooldown) &&
		(w.sessionEnd.IsZero() || now.Sub(w.sessionEnd) >= wakeSessionCooldown)
	if report {
		w.lastWake = now
	}
	w.mu.Unlock()
	if report && w.c.OnWake != nil {
		go w.c.OnWake()
	}
}
