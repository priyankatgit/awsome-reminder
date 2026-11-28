package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Engine turns a 16 kHz mono WAV into raw whisper text.
type Engine interface {
	// Warm starts the engine ahead of a Transcribe (no-op for engines that
	// cannot stay loaded).
	Warm(ctx context.Context) error
	// Transcribe returns the raw text of wav. A non-empty prompt (recent text)
	// biases whisper towards the same words and style.
	Transcribe(ctx context.Context, wav []byte, prompt string) (string, error)
	// Running reports whether an engine process is alive.
	Running() bool
	// Close stops any engine process now.
	Close()
}

// EngineConfig configures an engine subprocess.
type EngineConfig struct {
	Bin, Model   string
	Threads      int
	Env          []string      // added to the app's environment
	Idle         time.Duration // ServerEngine: exit after this long unused (default 30 s)
	StartTimeout time.Duration // ServerEngine: give up loading after this (default 60 s)
}

const (
	defaultIdle         = 30 * time.Second
	defaultStartTimeout = 60 * time.Second
	healthPoll          = 100 * time.Millisecond
	// statusDLLNotFound is Windows' STATUS_DLL_NOT_FOUND exit status.
	statusDLLNotFound = 0xC0000135
)

// ErrRuntimeMissing means the engine could not load a system library (on
// Windows: the Visual C++ runtime).
var ErrRuntimeMissing = errors.New("voice: the speech engine is missing a system library; install the Microsoft Visual C++ Redistributable (x64)")

// DefaultThreads is half the cores, at least 1 and at most 8 (whisper gains
// little beyond that and the user's real work keeps the rest).
func DefaultThreads(ncpu int) int {
	return min(max(ncpu/2, 1), 8)
}

func newCmd(ctx context.Context, c EngineConfig, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	cmd.Dir = filepath.Dir(c.Bin)
	cmd.Env = append(os.Environ(), c.Env...)
	hideAndContain(cmd)
	return cmd
}

// ServerEngine runs whisper-server, which keeps the model loaded between
// requests. It starts on Warm or Transcribe and exits after Idle without
// use, so the model is in memory only around a dictation.
type ServerEngine struct {
	c EngineConfig

	mu      sync.Mutex
	proc    *serverProc
	busy    int // callers between acquire and release
	idle    *time.Timer
	idleGen int
	base    string // last server URL (tests)
}

// serverProc is one whisper-server process.
type serverProc struct {
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	base     string
	ready    chan struct{} // closed once /health answers 200
	done     chan struct{} // closed once the process has exited
	stderr   *tail
	waitErr  error // valid after done
	timedOut atomic.Bool
}

func (p *serverProc) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// failure explains why the process exited before it was ready.
func (p *serverProc) failure() error {
	var ee *exec.ExitError
	if errors.As(p.waitErr, &ee) && uint32(ee.ExitCode()) == statusDLLNotFound {
		return ErrRuntimeMissing
	}
	msg := strings.TrimSpace(p.stderr.String())
	if lines := strings.Split(msg, "\n"); len(lines) > 3 {
		msg = strings.Join(lines[len(lines)-3:], "\n")
	}
	if p.timedOut.Load() {
		return fmt.Errorf("voice: the speech engine did not start in time")
	}
	if msg != "" {
		return fmt.Errorf("voice: the speech engine stopped: %s", msg)
	}
	return fmt.Errorf("voice: the speech engine stopped: %v", p.waitErr)
}

// NewServerEngine returns a stopped engine.
func NewServerEngine(c EngineConfig) *ServerEngine {
	if c.Idle == 0 {
		c.Idle = defaultIdle
	}
	if c.StartTimeout == 0 {
		c.StartTimeout = defaultStartTimeout
	}
	return &ServerEngine{c: c}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// startLocked launches a process and the goroutines that reap it and wait
// for it to become healthy. Called with mu held.
func (s *ServerEngine) startLocked() (*serverProc, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := newCmd(ctx, s.c,
		"-m", s.c.Model,
		"-t", strconv.Itoa(s.c.Threads),
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"-l", "en",
		"-nt",
		"--no-gpu",
	)
	p := &serverProc{
		cmd:    cmd,
		cancel: cancel,
		base:   "http://127.0.0.1:" + strconv.Itoa(port),
		ready:  make(chan struct{}),
		done:   make(chan struct{}),
		stderr: &tail{max: 4096},
	}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("voice: start speech engine: %w", err)
	}
	release := afterStart(cmd.Process)
	go func() {
		p.waitErr = cmd.Wait()
		release()
		close(p.done)
	}()
	go s.awaitHealthy(p)
	s.base = p.base
	return p, nil
}

func (s *ServerEngine) awaitHealthy(p *serverProc) {
	client := &http.Client{Timeout: time.Second}
	deadline := time.NewTimer(s.c.StartTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(healthPoll)
	defer tick.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-deadline.C:
			p.timedOut.Store(true)
			p.cancel()
			return
		case <-tick.C:
			resp, err := client.Get(p.base + "/health")
			if err != nil {
				continue
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				close(p.ready)
				return
			}
		}
	}
}

// acquire returns a ready process, starting one if needed. Every
// successful acquire must be paired with release.
func (s *ServerEngine) acquire(ctx context.Context) (*serverProc, error) {
	s.mu.Lock()
	s.busy++
	s.stopIdleLocked()
	if s.proc != nil && s.proc.exited() {
		s.proc = nil
	}
	if s.proc == nil {
		p, err := s.startLocked()
		if err != nil {
			s.busy--
			s.mu.Unlock()
			return nil, err
		}
		s.proc = p
	}
	p := s.proc
	s.mu.Unlock()

	select {
	case <-p.ready:
		return p, nil
	case <-p.done:
		s.release(p)
		return nil, p.failure()
	case <-ctx.Done():
		s.release(p)
		return nil, ctx.Err()
	}
}

// release ends a use of p and arms the idle timer when nobody else uses it.
func (s *ServerEngine) release(p *serverProc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy--
	if s.proc == p && p.exited() {
		s.proc = nil
	}
	if s.busy == 0 && s.proc != nil {
		s.idleGen++
		gen := s.idleGen
		s.idle = time.AfterFunc(s.c.Idle, func() { s.idleStop(gen) })
	}
}

func (s *ServerEngine) stopIdleLocked() {
	s.idleGen++
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
}

func (s *ServerEngine) idleStop(gen int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.idleGen || s.busy > 0 {
		return
	}
	s.killLocked()
}

// killLocked stops the process and waits (briefly) until it has exited.
func (s *ServerEngine) killLocked() {
	p := s.proc
	s.proc = nil
	if p == nil {
		return
	}
	p.cancel()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
	}
}

// Warm starts the server (if needed) and waits until the model is loaded.
func (s *ServerEngine) Warm(ctx context.Context) error {
	p, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	s.release(p)
	return nil
}

// Transcribe sends wav to the server and returns its text.
func (s *ServerEngine) Transcribe(ctx context.Context, wav []byte, prompt string) (string, error) {
	p, err := s.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer s.release(p)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(wav); err != nil {
		return "", err
	}
	_ = mw.WriteField("response_format", "json")
	_ = mw.WriteField("temperature", "0")
	if prompt != "" {
		_ = mw.WriteField("prompt", prompt)
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/inference", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("voice: speech engine request failed: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("voice: speech engine error (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", fmt.Errorf("voice: unexpected speech engine reply: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("voice: speech engine error: %s", out.Error)
	}
	return out.Text, nil
}

// Running reports whether a server process is alive.
func (s *ServerEngine) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.proc != nil && !s.proc.exited()
}

// Close stops the server now. The engine can start again later.
func (s *ServerEngine) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopIdleLocked()
	s.killLocked()
}

func (s *ServerEngine) lastBase() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base
}

// killForTest kills the process behind the engine's back, as a crash would.
func (s *ServerEngine) killForTest() {
	s.mu.Lock()
	p := s.proc
	s.mu.Unlock()
	if p != nil {
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

// tail keeps the last max bytes written to it.
type tail struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = t.buf[over:]
	}
	return len(b), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
