package app

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

type ctlLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *ctlLog) logf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *ctlLog) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func (l *ctlLog) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

func TestSyncControllerLogsConnectStatusAndDisconnect(t *testing.T) {
	l := &ctlLog{}
	f := newSyncFixture(t, func(d *syncDeps) {
		d.Logf = l.logf
		d.Poll = 20 * time.Millisecond
	})
	if _, err := f.ctl.Connect(context.Background(), "not a uri"); err == nil {
		t.Fatal("bad uri connected")
	}
	if _, err := f.ctl.Connect(context.Background(), uriA); err != nil {
		t.Fatal(err)
	}
	f.waitState(cloudsync.StateSynced)
	time.Sleep(150 * time.Millisecond) // several quiet polls
	if err := f.ctl.Disconnect(); err != nil {
		t.Fatal(err)
	}
	got := l.all()
	for _, want := range []string{
		"sync: connect failed:",
		"sync: connected to " + hostA,
		"sync: status synced",
		"sync: disconnected from " + hostA,
		"sync: status off",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("log lacks %q:\n%s", want, got)
		}
	}
	if n := l.count("sync: status synced"); n != 1 {
		t.Errorf("synced logged %d times over quiet polls, want 1:\n%s", n, got)
	}
	if strings.Contains(got, "S3cretPa55") || strings.Contains(got, "alice") {
		t.Errorf("log leaks credentials:\n%s", got)
	}
}

// A failing open whose error carries the connection string logs it redacted,
// once over repeated retries.
func TestSyncControllerLogsOpenFailureStatus(t *testing.T) {
	l := &ctlLog{}
	f := newSyncFixture(t, func(d *syncDeps) {
		d.Logf = l.logf
		d.ErrorRetry = 2 * time.Millisecond
	})
	f.sec.Set(uriA)
	f.openFn = func(context.Context, string, int) error {
		return errors.New("server said no to " + uriA) // the generic Classify branch
	}
	f.ctl.Start(context.Background())
	f.waitState(cloudsync.StateError)
	deadline := time.Now().Add(5 * time.Second)
	for f.openCount() < 4 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	got := l.all()
	if !strings.Contains(got, "sync: status error") || !strings.Contains(got, "mongodb+srv://***@"+hostA) {
		t.Errorf("log lacks the redacted error status:\n%s", got)
	}
	if strings.Contains(got, "S3cretPa55") || strings.Contains(got, "alice") {
		t.Errorf("log leaks credentials:\n%s", got)
	}
	if n := l.count("sync: connect to"); n != 1 {
		t.Errorf("%d connect-failure lines over %d opens, want 1:\n%s", n, f.openCount(), got)
	}
}
