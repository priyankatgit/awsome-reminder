package app

import (
	"strconv"
	"time"

	"awesome-reminder/internal/badge"
	"awesome-reminder/internal/cloudsync"
)

// relTime says how long ago t was: "just now" (under a minute), "N min ago",
// "N h ago" (under a day), else "on 26 Nov".
func relTime(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + " min ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + " h ago"
	default:
		return "on " + t.Format("2 Jan")
	}
}

// syncTooltip is the sync part of the tray tooltip; "" when sync is off.
func syncTooltip(st cloudsync.Status, now time.Time) string {
	switch st.State {
	case cloudsync.StateSyncing:
		return "Syncing…"
	case cloudsync.StateSynced:
		if st.LastSyncAt == nil {
			return "Synced"
		}
		return "Synced · " + relTime(now, *st.LastSyncAt)
	case cloudsync.StateOffline:
		switch {
		case st.Pending == 1:
			return "Offline · 1 change waiting"
		case st.Pending > 1:
			return "Offline · " + strconv.Itoa(st.Pending) + " changes waiting"
		}
		return "Offline"
	case cloudsync.StateError:
		if st.Error == "" {
			return "Sync error"
		}
		return "Sync error: " + st.Error
	}
	return ""
}

// dotFor is the tray dot for a status; frame picks the syncing animation
// frame.
func dotFor(st cloudsync.Status, frame bool) badge.DotStyle {
	switch st.State {
	case cloudsync.StateSyncing:
		if frame {
			return badge.DotSyncingB
		}
		return badge.DotSyncingA
	case cloudsync.StateSynced:
		return badge.DotSynced
	case cloudsync.StateOffline:
		return badge.DotOffline
	case cloudsync.StateError:
		return badge.DotError
	}
	return badge.DotNone
}
