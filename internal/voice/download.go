package voice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
)

// ErrChecksum means a download's SHA-256 did not match the pinned value.
var ErrChecksum = errors.New("voice: download checksum mismatch")

const progressStep = 64 << 10

// Download fetches a.URL into dest. It writes dest+".part" while hashing,
// checks the HTTP status, the size and the SHA-256, and only then renames
// the file to dest; on any failure the partial file is removed. progress
// (may be nil) gets the bytes received so far, at most every 64 KiB and
// once at the end.
func Download(ctx context.Context, c *http.Client, a Asset, dest string, progress func(done int64)) (err error) {
	part := dest + ".part"
	defer func() {
		if err != nil {
			os.Remove(part)
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", a.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", a.Name, resp.StatusCode)
	}

	f, err := os.Create(part)
	if err != nil {
		return err
	}
	h := sha256.New()
	pw := &progressWriter{report: progress}
	_, copyErr := io.Copy(io.MultiWriter(f, h, pw), resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("download %s: %w", a.Name, copyErr)
	}
	if closeErr != nil {
		return closeErr
	}
	pw.flush()

	if a.Size > 0 && pw.n != a.Size {
		return fmt.Errorf("download %s: got %d bytes, want %d", a.Name, pw.n, a.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != a.SHA256 {
		return fmt.Errorf("%w: %s sha256 %s, want %s", ErrChecksum, a.Name, got, a.SHA256)
	}
	return os.Rename(part, dest)
}

type progressWriter struct {
	n, reported int64
	report      func(int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.n += int64(len(b))
	if p.report != nil && p.n-p.reported >= progressStep {
		p.reported = p.n
		p.report(p.n)
	}
	return len(b), nil
}

func (p *progressWriter) flush() {
	if p.report != nil {
		p.reported = p.n
		p.report(p.n)
	}
}
