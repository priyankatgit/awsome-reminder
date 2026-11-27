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
	"awesome-reminder/internal/cloudsync/memsync"
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

func TestSyncControllerLogsOpenFailureStatus(t *testing.T) {
	l := &ctlLog{}
	f := newSyncFixture(t, func(d *syncDeps) { d.Logf = l.logf })
	f.sec.Set(uriA)
	f.openFn = func(context.Context, string, int) error {
		return errors.Join(memsync.ErrNetwork, errors.New("dial "+uriA))
	}
	f.ctl.Start(context.Background())
	f.waitState(cloudsync.StateOffline)
	got := l.all()
	if !strings.Contains(got, "sync: status offline") {
		t.Errorf("log lacks the offline status:\n%s", got)
	}
	if strings.Contains(got, "S3cretPa55") {
		t.Errorf("log leaks credentials:\n%s", got)
	}
}
