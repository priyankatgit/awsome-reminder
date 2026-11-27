package cloudsync_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"awesome-reminder/internal/cloudsync"
)

// logSink collects an engine's log lines.
type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) logf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

// take returns the lines logged since the last take.
func (l *logSink) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.lines
	l.lines = nil
	return out
}

func hasLine(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func TestLogsPushAndPullCountsButNotIdlePolls(t *testing.T) {
	ms, clk := setup()
	la, lb := &logSink{}, &logSink{}
	a := newDeviceOpts(t, ms, clk, cloudsync.Options{Logf: la.logf})
	b := newDeviceOpts(t, ms, clk, cloudsync.Options{Logf: lb.logf})
	a.sync() // the fresh store's own settings seed
	b.sync()
	la.take()
	lb.take()

	a.capture("first")
	a.capture("second")
	a.sync()
	if got := la.take(); !hasLine(got, "sync: pushed 2 change(s)") {
		t.Errorf("push lines %q, want a pushed-2 line", got)
	}
	b.sync()
	if got := lb.take(); !hasLine(got, "sync: pulled 2 item(s), applied 2") {
		t.Errorf("pull lines %q, want a pulled-2 line", got)
	}

	// The overlap re-read and the quiet polls after it log nothing.
	for range 10 {
		clk.Add(30 * time.Second)
		a.sync()
		b.sync()
	}
	if got := append(la.take(), lb.take()...); len(got) != 0 {
		t.Errorf("idle polls logged %q, want nothing", got)
	}
}

func TestLogsCycleErrorWithoutCredentials(t *testing.T) {
	ms, clk := setup()
	l := &logSink{}
	d := newDeviceOpts(t, ms, clk, cloudsync.Options{Logf: l.logf, Poll: time.Hour})
	d.capture("queued")
	leak := errors.New(`dial mongodb+srv://alice:hunter2@cluster0.example.mongodb.net/?retryWrites=true: refused`)
	ms.FailNext(leak, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.e.Start(ctx)
	waitFor(t, "a failed cycle", func() bool { return hasLineNow(l, "sync: cycle failed") })
	d.e.Stop()
	got := strings.Join(l.take(), "\n")
	if strings.Contains(got, "hunter2") || strings.Contains(got, "alice") {
		t.Errorf("log leaks credentials:\n%s", got)
	}
	if !strings.Contains(got, "mongodb+srv://***@cluster0.example.mongodb.net") {
		t.Errorf("log %q, want the redacted URI", got)
	}
}

func hasLineNow(l *logSink, sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return hasLine(l.lines, sub)
}

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"mongodb://u:p@h1:27017,h2/db":          "mongodb://***@h1:27017,h2/db",
		"x mongodb+srv://u:p%40ss@h/ y":         "x mongodb+srv://***@h/ y",
		"no uri here":                           "no uri here",
		"mongodb+srv://host.example.net/":       "mongodb+srv://host.example.net/",
		"mongodb+srv://u:pa@ss@host.example/db": "mongodb+srv://***@host.example/db",
	} {
		if got := cloudsync.Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}
