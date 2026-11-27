package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/image/webp"

	"awesome-reminder/internal/store"
)

// tinyWebP is a 1x1 lossless WebP.
func tinyWebP(t *testing.T) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := webp.DecodeConfig(bytes.NewReader(b)); err != nil {
		t.Fatalf("fixture invalid: %v", err)
	}
	return b
}

func attEnv(t *testing.T) (*env, string) {
	t.Helper()
	e := newEnv(t)
	dir := filepath.Join(t.TempDir(), "attachments")
	e.c.SetAttachmentDir(dir)
	return e, dir
}

func fileExists(dir, id string) bool {
	_, err := os.Stat(filepath.Join(dir, id+".webp"))
	return err == nil
}

func TestAddAttachmentStoresWebP(t *testing.T) {
	e, dir := attEnv(t)
	changes := 0
	e.c.OnChange(func() { changes++ })
	it := mustCapture(t, e, "a")
	data := tinyWebP(t)
	a, err := e.c.AddAttachment(it.ID, data)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if a.ItemID != it.ID || a.Mime != "image/webp" || a.Width != 1 || a.Height != 1 || a.Bytes != len(data) ||
		a.SHA256 != hex.EncodeToString(sum[:]) || a.UnreferencedSince == nil || a.UpdatedBy == "" {
		t.Fatalf("row %+v", a)
	}
	got, err := os.ReadFile(filepath.Join(dir, a.ID+".webp"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("file: %v", err)
	}
	if g, err := e.st.GetAttachment(a.ID); err != nil || g.ID != a.ID {
		t.Fatalf("stored %+v %v", g, err)
	}
	if changes != 2 { // capture + add
		t.Fatalf("changes = %d", changes)
	}
	d, err := e.c.ItemDetail(it.ID)
	if err != nil || len(d.Attachments) != 1 || d.Item.ID != it.ID {
		t.Fatalf("detail %+v %v", d, err)
	}
}

func TestAddAttachmentRejects(t *testing.T) {
	e, dir := attEnv(t)
	it := mustCapture(t, e, "a")
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if _, err := e.c.AddAttachment(it.ID, pngBuf.Bytes()); !errors.Is(err, ErrNotWebP) {
		t.Fatalf("png: %v", err)
	}
	big := append(tinyWebP(t), make([]byte, 2<<20)...)[:2<<20+1]
	if _, err := e.c.AddAttachment(it.ID, big); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("big: %v", err)
	}
	if _, err := e.c.AddAttachment("nope", tinyWebP(t)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown item: %v", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("rejected adds must not create the directory")
	}
}

func TestSetDescriptionTracksReferences(t *testing.T) {
	e, _ := attEnv(t)
	it := mustCapture(t, e, "a")
	a, err := e.c.AddAttachment(it.ID, tinyWebP(t))
	if err != nil {
		t.Fatal(err)
	}
	unref := func() *time.Time {
		g, _ := e.st.GetAttachment(a.ID)
		return g.UnreferencedSince
	}
	e.now = e.now.Add(time.Hour)
	got, err := e.c.SetDescription(it.ID, "see ![x](att:"+a.ID+")")
	if err != nil || got.Description == "" || unref() != nil {
		t.Fatalf("referenced: %v %v", err, unref())
	}
	if g, _ := e.st.GetAttachment(a.ID); !g.UpdatedAt.Equal(e.now.UTC()) || g.UpdatedBy == "" {
		t.Fatalf("stamp %+v", g)
	}
	e.now = e.now.Add(time.Hour)
	if _, err := e.c.SetDescription(it.ID, "gone"); err != nil {
		t.Fatal(err)
	}
	u := unref()
	if u == nil || !u.Equal(e.now.UTC()) {
		t.Fatalf("unreferenced: %v", u)
	}
	first := *u
	e.now = e.now.Add(time.Hour)
	mustDesc(t, e, it.ID, "still gone")
	if u := unref(); u == nil || !u.Equal(first) {
		t.Fatalf("since moved: %v", u)
	}
	mustDesc(t, e, it.ID, "att:"+a.ID)
	if unref() != nil {
		t.Fatal("re-add should clear")
	}
}

func TestSweep(t *testing.T) {
	e, dir := attEnv(t)
	day := 24 * time.Hour
	base := e.now
	add := func(item string) string {
		a, err := e.c.AddAttachment(item, tinyWebP(t))
		if err != nil {
			t.Fatal(err)
		}
		return a.ID
	}
	it := mustCapture(t, e, "a")
	old := add(it.ID) // unreferenced since base
	ref := add(it.ID) // referenced, created long ago
	mustDesc(t, e, it.ID, "att:"+ref)
	e.now = base.Add(2 * day)
	recent := add(it.ID) // unreferenced since base+2d

	e.now = base.Add(8 * day) // old: 8d, recent: 6d
	if n, err := e.c.SweepAttachments(); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if fileExists(dir, old) || !fileExists(dir, recent) || !fileExists(dir, ref) {
		t.Fatal("wrong files after sweep")
	}
	if g, _ := e.st.GetAttachment(old); !g.Deleted {
		t.Fatal("old not tombstoned")
	}
	if n, _ := e.c.SweepAttachments(); n != 0 {
		t.Fatalf("not idempotent: %d", n)
	}
	e.now = base.Add(300 * day) // referenced stays whatever its age
	if n, _ := e.c.SweepAttachments(); n != 1 || !fileExists(dir, ref) || fileExists(dir, recent) {
		t.Fatalf("second sweep n=%d", n)
	}
}

func TestSweepDeletedItem(t *testing.T) {
	e, dir := attEnv(t)
	it := mustCapture(t, e, "a")
	a, err := e.c.AddAttachment(it.ID, tinyWebP(t))
	if err != nil {
		t.Fatal(err)
	}
	mustDesc(t, e, it.ID, "att:"+a.ID)
	if err := e.c.Delete(it.ID); err != nil {
		t.Fatal(err)
	}
	deletedAt := e.now
	e.now = deletedAt.Add(29 * 24 * time.Hour)
	if n, _ := e.c.SweepAttachments(); n != 0 {
		t.Fatalf("29d swept %d", n)
	}
	// undo (Undelete, the Undo toast) within the window keeps the image
	if err := e.c.Undelete(it.ID); err != nil {
		t.Fatal(err)
	}
	e.now = deletedAt.Add(60 * 24 * time.Hour)
	if n, _ := e.c.SweepAttachments(); n != 0 || !fileExists(dir, a.ID) {
		t.Fatalf("undeleted item swept %d", n)
	}
	if err := e.c.Delete(it.ID); err != nil {
		t.Fatal(err)
	}
	deletedAt = e.now
	e.now = deletedAt.Add(31 * 24 * time.Hour)
	if n, _ := e.c.SweepAttachments(); n != 1 || fileExists(dir, a.ID) {
		t.Fatalf("31d swept %d", n)
	}
}

func TestAttachmentPathValidates(t *testing.T) {
	e, dir := attEnv(t)
	it := mustCapture(t, e, "a")
	a, err := e.c.AddAttachment(it.ID, tinyWebP(t))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.c.AttachmentPath(a.ID)
	if err != nil || p != filepath.Join(dir, a.ID+".webp") {
		t.Fatalf("path %q %v", p, err)
	}
	for _, bad := range []string{"../x", "ABC", "", a.ID + "/..", "01ARZ3NDEKTSV4RRFFQ69G5FAI"} { // I is not Crockford
		if _, err := e.c.AttachmentPath(bad); !errors.Is(err, ErrBadAttachmentID) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if _, err := e.c.AttachmentPath("01ARZ3NDEKTSV4RRFFQ69G5FAV"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	a2, err := e.c.AddAttachment(it.ID, tinyWebP(t))
	if err != nil {
		t.Fatal(err)
	}
	a2.Deleted = true
	if err := e.st.PutAttachment(a2); err != nil {
		t.Fatal(err)
	}
	if _, err := e.c.AttachmentPath(a2.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
	os.Remove(p)
	if _, err := e.c.AttachmentPath(a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing file: %v", err)
	}
}

func mustDesc(t *testing.T, e *env, id, md string) {
	t.Helper()
	if _, err := e.c.SetDescription(id, md); err != nil {
		t.Fatal(err)
	}
}

func TestSweepRemoveFailureKeepsRowAndContinues(t *testing.T) {
	e, dir := attEnv(t)
	it := mustCapture(t, e, "a")
	bad, err := e.c.AddAttachment(it.ID, tinyWebP(t))
	if err != nil {
		t.Fatal(err)
	}
	good, err := e.c.AddAttachment(it.ID, tinyWebP(t))
	if err != nil {
		t.Fatal(err)
	}
	// Make bad's path a non-empty directory so os.Remove fails.
	p := filepath.Join(dir, bad.ID+".webp")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(8 * 24 * time.Hour)
	n, err := e.c.SweepAttachments()
	if err == nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if g, _ := e.st.GetAttachment(bad.ID); g.Deleted {
		t.Fatal("failed attachment was tombstoned")
	}
	if g, _ := e.st.GetAttachment(good.ID); !g.Deleted || fileExists(dir, good.ID) {
		t.Fatal("other attachment not swept")
	}
	// a later sweep retries once the obstruction is gone
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
	if n, err := e.c.SweepAttachments(); err != nil || n != 1 {
		t.Fatalf("retry n=%d err=%v", n, err)
	}
}
