package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadLocationRoundTrip(t *testing.T) {
	isolate(t)
	x, y := t.TempDir(), t.TempDir()
	if err := SaveLocation(Location{DataDir: x, PreviousDir: y}); err != nil {
		t.Fatal(err)
	}
	loc, found, err := LoadLocation()
	if err != nil || !found {
		t.Fatalf("load: found=%v err=%v", found, err)
	}
	if loc.Version != 1 || loc.DataDir != x || loc.PreviousDir != y {
		t.Errorf("loc = %+v", loc)
	}
	p, _ := PointerPath()
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tmp file left behind (err=%v)", err)
	}
}

func TestLoadLocationMissing(t *testing.T) {
	isolate(t)
	_, found, err := LoadLocation()
	if found || err != nil {
		t.Errorf("found=%v err=%v", found, err)
	}
}

func TestLoadLocationInvalid(t *testing.T) {
	isolate(t)
	p, _ := PointerPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{`, `{"version":2,"dataDir":"x"}`} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadLocation(); err == nil {
			t.Errorf("body %q: want error", body)
		}
	}
}
