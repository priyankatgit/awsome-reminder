package app

import (
	"os"
	"slices"
	"testing"

	"awesome-reminder/internal/paths"
)

func TestSetupAppOptionsQuitOnCloseAndOwnProfile(t *testing.T) {
	o := setupAppOptions(SetupOptions{Assets: os.DirFS(t.TempDir())}, func() {})
	if !o.Mac.ApplicationShouldTerminateAfterLastWindowClosed {
		t.Fatal("macOS must quit when the setup window closes")
	}
	if o.Windows.DisableQuitOnLastWindowClosed || o.Linux.DisableQuitOnLastWindowClosed {
		t.Fatal("Windows and Linux must quit when the setup window closes")
	}
	if !slices.Equal(o.Windows.AdditionalBrowserArgs, webviewBrowserArgs()) {
		t.Fatalf("browser args = %v", o.Windows.AdditionalBrowserArgs)
	}
	dir := o.Windows.WebviewUserDataPath
	if dir == "" || !paths.Within(dir, os.TempDir()) {
		t.Fatalf("webview profile %q, want one under %q", dir, os.TempDir())
	}
	if o.SingleInstance == nil || o.SingleInstance.UniqueID != "dev.awesome-reminder.app" {
		t.Fatalf("single instance = %+v", o.SingleInstance)
	}
}
