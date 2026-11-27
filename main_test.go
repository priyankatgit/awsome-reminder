package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

// Wails logs binding arguments at Debug, and SyncConnect's argument is the
// Atlas connection string: the logger must never be at Debug.
func TestLoggerLevelAboveDebug(t *testing.T) {
	if logLevel <= slog.LevelDebug {
		t.Fatalf("logLevel %v must be above Debug", logLevel)
	}
	if newLogger(io.Discard).Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("the logger records Debug lines")
	}
}
