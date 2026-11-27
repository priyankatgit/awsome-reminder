package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"awesome-reminder/internal/core"
)

type fakePather struct{ paths map[string]string }

func (f fakePather) AttachmentPath(id string) (string, error) {
	if p, ok := f.paths[id]; ok {
		return p, nil
	}
	return "", core.ErrBadAttachmentID
}

func TestAttRouteServesAndRejects(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a.webp")
	want := []byte("RIFFfakewebp")
	if err := os.WriteFile(file, want, 0o600); err != nil {
		t.Fatal(err)
	}
	nextHit := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextHit = true
		w.WriteHeader(http.StatusTeapot)
	})
	h := attMiddleware(fakePather{paths: map[string]string{"01ABC": file, "gone": file + ".missing"}})(next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/att/01ABC", nil))
	if rec.Code != 200 || rec.Body.String() != string(want) {
		t.Fatalf("valid: %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/webp" {
		t.Errorf("content-type %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, max-age=31536000, immutable" {
		t.Errorf("cache-control %q", cc)
	}

	for _, p := range []string{"/att/../x", "/att/nope", "/att/", "/att/a/b", "/att/gone"} {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != 404 {
			t.Errorf("%s: code %d, want 404", p, rec.Code)
		}
	}
	if nextHit {
		t.Fatal("next called for /att/ paths")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/other", nil))
	if !nextHit || rec.Code != http.StatusTeapot {
		t.Fatalf("/other not passed through: %d", rec.Code)
	}
}

func TestOpenURLAllowList(t *testing.T) {
	var opened []string
	svc := NewService(Deps{OpenURL: func(u string) error { opened = append(opened, u); return nil }})
	for _, u := range []string{"http://a.com", "https://a.com/x?y=1", "mailto:a@b.com", "HTTPS://A.COM", "Mailto:a@b.com"} {
		if err := svc.OpenURL(u); err != nil {
			t.Errorf("%q rejected: %v", u, err)
		}
	}
	if len(opened) != 5 {
		t.Fatalf("opened %v", opened)
	}
	for _, u := range []string{"javascript:alert(1)", "file:///c:/x", "", "ftp://a", "//a.com", " https://a.com", "data:text/html,x"} {
		err := svc.OpenURL(u)
		if err == nil || err.Error() != "unsupported link" {
			t.Errorf("%q: err %v", u, err)
		}
	}
	if len(opened) != 5 {
		t.Fatalf("rejected links reached the browser: %v", opened)
	}
	failing := NewService(Deps{OpenURL: func(string) error { return errors.New("boom") }})
	if err := failing.OpenURL("https://a.com"); err == nil {
		t.Error("opener error swallowed")
	}
}
