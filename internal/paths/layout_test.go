package paths

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestLayoutStandard(t *testing.T) {
	_, cache := isolate(t)
	std, _ := StandardRoot()
	l, err := NewLayout(std)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Standard {
		t.Error("Standard = false")
	}
	if want := filepath.Join(cache, "awesome-reminder", "voice"); l.Voice() != want {
		t.Errorf("Voice = %q, want %q", l.Voice(), want)
	}
	if l.Webview() != "" {
		t.Errorf("Webview = %q, want empty", l.Webview())
	}
}

func TestLayoutCustom(t *testing.T) {
	isolate(t)
	T := t.TempDir()
	l, err := NewLayout(T)
	if err != nil {
		t.Fatal(err)
	}
	if l.Standard {
		t.Error("Standard = true")
	}
	wv := ""
	if runtime.GOOS == "windows" {
		wv = filepath.Join(T, "webview")
	}
	checks := map[string][2]string{
		"DB":          {l.DB(), filepath.Join(T, "awesome.db")},
		"Attachments": {l.Attachments(), filepath.Join(T, "attachments")},
		"SyncKey":     {l.SyncKey(), filepath.Join(T, "sync.key")},
		"PushKey":     {l.PushKey(), filepath.Join(T, "push.key")},
		"Log":         {l.Log(), filepath.Join(T, "awesome.log")},
		"Voice":       {l.Voice(), filepath.Join(T, "voice")},
		"Webview":     {l.Webview(), wv},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", name, c[0], c[1])
		}
	}
}
