package voice

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func writeZip(t *testing.T, files map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "a.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return p
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func TestExtractZipKeepsEngineFilesOnly(t *testing.T) {
	arc := writeZip(t, map[string]string{
		"Release/whisper-server.exe":   "server",
		"Release/whisper.dll":          "whisper",
		"Release/ggml-cpu-haswell.dll": "cpu",
		"Release/llama.dll":            "llama",
		"Release/SDL2.dll":             "sdl",
		"Release/whisper-cli.exe":      "cli",
	})
	dir := t.TempDir()
	if err := Extract(arc, "zip", dir, WhisperFile); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	got := listDir(t, dir)
	want := []string{"ggml-cpu-haswell.dll", "whisper-server.exe", "whisper.dll"}
	if len(got) != len(want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("files = %v, want %v", got, want)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "whisper-server.exe"))
	if string(b) != "server" {
		t.Errorf("content = %q", b)
	}
}

func TestExtractZipSlip(t *testing.T) {
	root := t.TempDir()
	arc := writeZip(t, map[string]string{"../../ggml-evil.dll": "x"})
	dir := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Extract(arc, "zip", dir, WhisperFile); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "ggml-evil.dll")); !os.IsNotExist(err) {
		t.Error("file escaped the target dir")
	}
	if _, err := os.Stat(filepath.Join(dir, "ggml-evil.dll")); err != nil {
		t.Errorf("want file flattened into dir: %v", err)
	}
}

func TestExtractTarGzWithSymlinks(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(h *tar.Header, body string) {
		h.Size = int64(len(body))
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	add(&tar.Header{Name: "x/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	add(&tar.Header{Name: "x/libwhisper.so.1", Typeflag: tar.TypeSymlink, Linkname: "libwhisper.so.1.9.5"}, "")
	add(&tar.Header{Name: "x/libwhisper.so.1.9.5", Typeflag: tar.TypeReg, Mode: 0o755}, "libbody")
	add(&tar.Header{Name: "x/whisper-server", Typeflag: tar.TypeReg, Mode: 0o755}, "server")
	add(&tar.Header{Name: "x/whisper-cli", Typeflag: tar.TypeReg, Mode: 0o755}, "cli")
	add(&tar.Header{Name: "x/libggml.so", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}, "")
	tw.Close()
	gz.Close()
	arc := filepath.Join(t.TempDir(), "a.tar.gz")
	if err := os.WriteFile(arc, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := Extract(arc, "tar.gz", dir, WhisperFile); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "libwhisper.so.1"))
	if err != nil || string(b) != "libbody" {
		t.Errorf("symlink copy = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "whisper-cli")); !os.IsNotExist(err) {
		t.Error("whisper-cli should be skipped")
	}
	if _, err := os.Stat(filepath.Join(dir, "libggml.so")); !os.IsNotExist(err) {
		t.Error("a symlink pointing outside the archive must be skipped")
	}
	st, err := os.Stat(filepath.Join(dir, "whisper-server"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o100 == 0 {
		t.Errorf("whisper-server mode = %v, want executable", st.Mode())
	}
}

func keepBinLib(p string) bool {
	return strings.Contains(p, "/bin/") || strings.Contains(p, "/lib/")
}

func TestExtractTarBz2(t *testing.T) {
	dir := t.TempDir()
	if err := Extract(filepath.Join("testdata", "tiny.tar.bz2"), "tar.bz2", dir, keepBinLib); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	got := listDir(t, dir)
	want := []string{"kws-mic", "libonnxruntime.so.1"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("files = %v, want %v", got, want)
	}
	st, err := os.Stat(filepath.Join(dir, "kws-mic"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o100 == 0 {
		t.Errorf("kws-mic mode = %v, want executable", st.Mode())
	}
}

func TestExtractKeepFilterReceivesPath(t *testing.T) {
	var seen []string
	keep := func(p string) bool { seen = append(seen, p); return false }
	if err := Extract(filepath.Join("testdata", "tiny.tar.bz2"), "tar.bz2", t.TempDir(), keep); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	found := false
	for _, p := range seen {
		if p == "x/lib/libonnxruntime.so.1" {
			found = true
		}
	}
	if !found {
		t.Errorf("keep saw %v, want x/lib/libonnxruntime.so.1", seen)
	}
}
