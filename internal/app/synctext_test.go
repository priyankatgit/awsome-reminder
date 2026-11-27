package app

import (
	"testing"
	"time"

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
		{cloudsync.Status{State: cloudsync.StateError, Error: "authentication failed"}, "Sync error: authentication failed"},
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
