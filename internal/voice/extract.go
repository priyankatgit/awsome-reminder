package voice

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// engineFile reports whether an archive entry's base name is part of the
// engine we run: the server and the whisper/ggml shared libraries. The
// release archives also carry examples, tests, llama, SDL2 and parakeet,
// which are left out.
func engineFile(base string) bool {
	switch {
	case base == "whisper-server", base == "whisper-server.exe", base == "whisper.dll":
		return true
	case strings.HasPrefix(base, "ggml") && strings.HasSuffix(base, ".dll"):
		return true
	case strings.HasPrefix(base, "libwhisper.so"), strings.HasPrefix(base, "libggml") && strings.Contains(base, ".so"):
		return true
	}
	return false
}

// Extract writes the engine files of a release archive ("zip" or "tar.gz")
// flat into dir, by base name only, so no entry can land outside dir. Tar
// symlinks between kept files become copies of their target; a symlink that
// points outside the archive is skipped.
func Extract(archive, kind, dir string) error {
	switch kind {
	case "zip":
		return extractZip(archive, dir)
	case "tar.gz":
		return extractTarGz(archive, dir)
	}
	return fmt.Errorf("extract: unknown archive kind %q", kind)
}

func baseName(name string) string {
	return path.Base(strings.ReplaceAll(name, "\\", "/"))
}

func writeFile(dir, base string, r io.Reader) error {
	f, err := os.OpenFile(filepath.Join(dir, base), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func extractZip(archive, dir string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		base := baseName(f.Name)
		if f.FileInfo().IsDir() || !engineFile(base) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeFile(dir, base, rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	links := map[string]string{} // kept symlink base -> target base
	written := map[string]bool{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		base := baseName(h.Name)
		if !engineFile(base) {
			continue
		}
		switch h.Typeflag {
		case tar.TypeReg:
			if err := writeFile(dir, base, tr); err != nil {
				return err
			}
			written[base] = true
		case tar.TypeSymlink:
			// Only same-directory relative links ("libwhisper.so.1.9.5").
			if t := h.Linkname; t != "" && !strings.ContainsAny(t, "/\\") && t != ".." {
				links[base] = t
			}
		}
	}

	for name := range links {
		target := name
		for hops := 0; hops < 8; hops++ {
			next, ok := links[target]
			if !ok {
				break
			}
			target = next
		}
		if !written[target] {
			continue
		}
		src, err := os.Open(filepath.Join(dir, target))
		if err != nil {
			return err
		}
		err = writeFile(dir, name, src)
		src.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
