package voice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var dlBody = bytes.Repeat([]byte("ab"), 100_000)

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func serveBytes(t *testing.T, b []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func assertGone(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should not exist (err=%v)", p, err)
		}
	}
}

func TestDownloadVerifiesAndRenames(t *testing.T) {
	srv := serveBytes(t, dlBody)
	dest := filepath.Join(t.TempDir(), "model.bin")
	var last int64
	a := Asset{Name: "m", URL: srv.URL, SHA256: sha(dlBody), Size: int64(len(dlBody))}
	if err := Download(context.Background(), srv.Client(), a, dest, func(d int64) { last = d }); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, dlBody) {
		t.Fatalf("dest content mismatch (err=%v, len=%d)", err, len(got))
	}
	assertGone(t, dest+".part")
	if last != int64(len(dlBody)) {
		t.Errorf("last progress = %d, want %d", last, len(dlBody))
	}
}

func TestDownloadChecksumMismatchLeavesNothing(t *testing.T) {
	srv := serveBytes(t, dlBody)
	dest := filepath.Join(t.TempDir(), "model.bin")
	a := Asset{URL: srv.URL, SHA256: strings.Repeat("0", 64), Size: int64(len(dlBody))}
	err := Download(context.Background(), srv.Client(), a, dest, nil)
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("err = %v, want ErrChecksum", err)
	}
	assertGone(t, dest, dest+".part")
}

func TestDownloadSizeMismatch(t *testing.T) {
	srv := serveBytes(t, dlBody)
	dest := filepath.Join(t.TempDir(), "model.bin")
	a := Asset{URL: srv.URL, SHA256: sha(dlBody), Size: int64(len(dlBody)) + 1}
	if err := Download(context.Background(), srv.Client(), a, dest, nil); err == nil {
		t.Fatal("want error for size mismatch")
	}
	assertGone(t, dest, dest+".part")
}

func TestDownloadHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	dest := filepath.Join(t.TempDir(), "model.bin")
	err := Download(context.Background(), srv.Client(), Asset{URL: srv.URL, SHA256: sha(nil)}, dest, nil)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want 404", err)
	}
	assertGone(t, dest, dest+".part")
}

func TestDownloadCancelled(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "200000")
		_, _ = w.Write(dlBody[:100_000])
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	dest := filepath.Join(t.TempDir(), "model.bin")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	err := Download(ctx, srv.Client(), Asset{URL: srv.URL, SHA256: sha(dlBody), Size: int64(len(dlBody))}, dest, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	assertGone(t, dest, dest+".part")
}

func TestPinnedAssets(t *testing.T) {
	win, ok := EngineAsset("windows", "amd64")
	if !ok {
		t.Fatal("windows/amd64 engine should be pinned")
	}
	if win.URL != "https://github.com/ggml-org/whisper.cpp/releases/download/b5454/whisper-bin-x64.zip" ||
		win.Size != 8928640 ||
		win.SHA256 != "6ba69e3482d7826214f90a6a9c84ca07782aec1e1d0c6a7c30c994fd5d816ccb" ||
		win.Archive != "zip" {
		t.Errorf("windows asset = %+v", win)
	}
	for _, p := range [][2]string{{"linux", "amd64"}, {"linux", "arm64"}} {
		a, ok := EngineAsset(p[0], p[1])
		if !ok || a.Archive != "tar.gz" {
			t.Errorf("%s/%s engine = %+v, %v", p[0], p[1], a, ok)
		}
	}
	if _, ok := EngineAsset("darwin", "arm64"); ok {
		t.Error("darwin has no official prebuilt engine")
	}
	if ModelAsset.Size != 147964211 || ModelAsset.SHA256 != "a03779c86df3323075f5e796cb2ce5029f00ec8869eee3fdfb897afe36c6d002" {
		t.Errorf("model asset = %+v", ModelAsset)
	}
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	all := []Asset{ModelAsset}
	for _, p := range [][2]string{{"windows", "amd64"}, {"linux", "amd64"}, {"linux", "arm64"}} {
		a, _ := EngineAsset(p[0], p[1])
		all = append(all, a)
	}
	for _, a := range all {
		if !hex64.MatchString(a.SHA256) {
			t.Errorf("%s: bad sha256 %q", a.Name, a.SHA256)
		}
		if !strings.HasPrefix(a.URL, "https://") {
			t.Errorf("%s: url %q is not https", a.Name, a.URL)
		}
	}
}
