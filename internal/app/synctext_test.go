package app

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"awesome-reminder/internal/badge"
	"awesome-reminder/internal/cloudsync"
)

func TestRelTime(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		ago  time.Duration
		want string
	}{
		{0, "just now"},
		{59 * time.Second, "just now"},
		{61 * time.Second, "1 min ago"},
		{59 * time.Minute, "59 min ago"},
		{3 * time.Hour, "3 h ago"},
		{48 * time.Hour, "on 26 Nov"},
	} {
		if got := relTime(now, now.Add(-tc.ago)); got != tc.want {
			t.Errorf("relTime(-%v) = %q, want %q", tc.ago, got, tc.want)
		}
	}
}

func TestSyncTooltip(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	two := now.Add(-2 * time.Minute)
	for _, tc := range []struct {
		st   cloudsync.Status
		want string
	}{
		{cloudsync.Status{State: cloudsync.StateOff}, ""},
		{cloudsync.Status{}, ""},
		{cloudsync.Status{State: cloudsync.StateSyncing}, "Syncing…"},
		{cloudsync.Status{State: cloudsync.StateSynced, LastSyncAt: &two}, "Synced · 2 min ago"},
		{cloudsync.Status{State: cloudsync.StateSynced}, "Synced"},
		{cloudsync.Status{State: cloudsync.StateOffline, Pending: 4}, "Offline · 4 changes waiting"},
		{cloudsync.Status{State: cloudsync.StateOffline, Pending: 1}, "Offline · 1 change waiting"},
		{cloudsync.Status{State: cloudsync.StateOffline}, "Offline"},
		{cloudsync.Status{State: cloudsync.StateError, Error: "authentication failed"}, "Sync error"},
		{cloudsync.Status{State: cloudsync.StateError}, "Sync error"},
	} {
		if got := syncTooltip(tc.st, now); got != tc.want {
			t.Errorf("syncTooltip(%+v) = %q, want %q", tc.st, got, tc.want)
		}
	}
}

func TestDotFor(t *testing.T) {
	for _, tc := range []struct {
		st    cloudsync.State
		frame bool
		want  badge.DotStyle
	}{
		{cloudsync.StateOff, false, badge.DotNone},
		{cloudsync.StateSynced, true, badge.DotSynced},
		{cloudsync.StateSyncing, false, badge.DotSyncingA},
		{cloudsync.StateSyncing, true, badge.DotSyncingB},
		{cloudsync.StateOffline, false, badge.DotOffline},
		{cloudsync.StateError, false, badge.DotError},
	} {
		if got := dotFor(cloudsync.Status{State: tc.st}, tc.frame); got != tc.want {
			t.Errorf("dotFor(%s,%v) = %d, want %d", tc.st, tc.frame, got, tc.want)
		}
	}
}

func TestPendingTooltipCap(t *testing.T) {
	// Short: the usual order, untouched.
	if got := pendingTooltip(3, "Sync error", "taken", ""); got != "Awesome Reminder — 3 pending — Sync error — hotkey error: taken" {
		t.Errorf("short tooltip %q", got)
	}

	// Too long: the hotkey error comes before the sync text, and the cut
	// ends in an ellipsis at exactly the Windows limit.
	long := strings.Repeat("x", 60) // fits alone, not with the sync text
	got := pendingTooltip(12, "Offline · 3 changes waiting", long, "")
	if n := utf16Len(got); n > 127 {
		t.Errorf("tooltip is %d UTF-16 units, over 127: %q", n, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("no ellipsis: %q", got)
	}
	want := "Awesome Reminder — 12 pending — hotkey error: " + long
	if !strings.HasPrefix(got, want) {
		t.Errorf("hotkey error not kept ahead of the sync text:\n%q", got)
	}

	// Both hotkey errors stay ahead of the sync text, capture before reply.
	mid := strings.Repeat("x", 40)
	got = pendingTooltip(12, "Offline · 3 changes waiting", mid, "busy")
	if n := utf16Len(got); n > 127 {
		t.Errorf("two-error tooltip is %d units: %q", n, got)
	}
	want = "Awesome Reminder — 12 pending — hotkey error: " + mid + " — reply hotkey error: busy — "
	if !strings.HasPrefix(got, want) || !strings.HasSuffix(got, "…") {
		t.Errorf("hotkey errors not kept ahead of the sync text:\n%q", got)
	}

	// Astral characters count as two units and are never split.
	emoji := strings.Repeat("\U0001F600", 70)
	got = pendingTooltip(-1, "", emoji, "")
	if n := utf16Len(got); n > 127 {
		t.Errorf("emoji tooltip is %d units", n)
	}
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "\U0001F600…") {
		t.Errorf("emoji tooltip split a character: %q", got)
	}
}
