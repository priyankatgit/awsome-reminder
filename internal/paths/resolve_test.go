package paths

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ptrJSON(dir, pending string) string {
	b, _ := json.Marshal(Location{Version: 1, DataDir: dir, PendingMove: pending})
	return string(b)
}

func TestResolve(t *testing.T) {
	type env struct{ std, root string }
	touchDB := func(t *testing.T, dir string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, DBFile), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rows := []struct {
		name   string
		setup  func(t *testing.T, e env) (raw string, has bool)
		want   State
		root   func(e env) string
		reason string
	}{
		{"fresh", func(t *testing.T, e env) (string, bool) { return "", false }, StateFirstRunFresh, func(e env) string { return e.std }, ""},
		{"existing", func(t *testing.T, e env) (string, bool) { touchDB(t, e.std); return "", false }, StateFirstRunExisting, func(e env) string { return e.std }, ""},
		{"ready", func(t *testing.T, e env) (string, bool) { touchDB(t, e.root); return ptrJSON(e.root, ""), true }, StateReady, func(e env) string { return e.root }, ""},
		{"missing folder", func(t *testing.T, e env) (string, bool) { return ptrJSON(filepath.Join(e.root, "gone"), ""), true }, StateUnavailable, nil, "not found"},
		{"no db", func(t *testing.T, e env) (string, bool) { return ptrJSON(e.root, ""), true }, StateUnavailable, nil, "awesome.db is missing"},
		{"is a file", func(t *testing.T, e env) (string, bool) {
			f := filepath.Join(e.root, "afile")
			if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			return ptrJSON(f, ""), true
		}, StateUnavailable, nil, "Not a folder"},
		{"relative", func(t *testing.T, e env) (string, bool) { return ptrJSON("rel/path", ""), true }, StateUnavailable, nil, "absolute"},
		{"invalid json", func(t *testing.T, e env) (string, bool) { return "{", true }, StateUnavailable, nil, "location.json"},
		{"move pending", func(t *testing.T, e env) (string, bool) {
			touchDB(t, e.root)
			return ptrJSON(e.root, filepath.Join(e.root, "T")), true
		}, StateMovePending, func(e env) string { return e.root }, ""},
		{"move pending no source", func(t *testing.T, e env) (string, bool) {
			return ptrJSON(filepath.Join(e.root, "gone"), filepath.Join(e.root, "T")), true
		}, StateUnavailable, nil, ""},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			isolate(t)
			std, _ := StandardRoot()
			e := env{std: std, root: t.TempDir()}
			raw, has := r.setup(t, e)
			p, _ := PointerPath()
			if has {
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(raw), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := Resolve()
			if got.State != r.want {
				t.Fatalf("State = %q (%s), want %q", got.State, got.Reason, r.want)
			}
			if (got.Reason != "") != (r.want == StateUnavailable) {
				t.Errorf("Reason = %q for state %q", got.Reason, got.State)
			}
			if r.reason != "" && !strings.Contains(got.Reason, r.reason) {
				t.Errorf("Reason = %q, want contains %q", got.Reason, r.reason)
			}
			if r.root != nil && !SameDir(got.Root, r.root(e)) {
				t.Errorf("Root = %q, want %q", got.Root, r.root(e))
			}
			if has {
				after, err := os.ReadFile(p)
				if err != nil || !bytes.Equal(after, []byte(raw)) {
					t.Errorf("pointer changed: %q (err=%v)", after, err)
				}
			}
		})
	}
}
