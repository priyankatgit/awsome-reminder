package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func caseInsensitive() bool { return runtime.GOOS == "windows" || runtime.GOOS == "darwin" }

func normalize(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	if caseInsensitive() {
		p = strings.ToLower(p)
	}
	return p
}

// SameDir reports whether a and b name the same folder, ignoring trailing
// separators and (on Windows and macOS) case.
func SameDir(a, b string) bool { return normalize(a) == normalize(b) }

// Within reports whether child equals parent or lies below it.
func Within(child, parent string) bool {
	c, p := normalize(child), normalize(parent)
	if c == p {
		return true
	}
	if !strings.HasSuffix(p, string(filepath.Separator)) {
		p += string(filepath.Separator)
	}
	return strings.HasPrefix(c, p)
}

// nearestExisting walks up from dir to the closest ancestor that exists.
func nearestExisting(dir string) string {
	d, err := filepath.Abs(dir)
	if err != nil {
		d = dir
	}
	for {
		if _, err := os.Stat(d); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return d
		}
		d = parent
	}
}

// FreeSpace returns the bytes available to the user on dir's volume, using
// the nearest existing ancestor when dir does not exist yet.
func FreeSpace(dir string) (uint64, error) { return freeSpace(nearestExisting(dir)) }

// IsNetwork reports whether dir is on a network share.
func IsNetwork(dir string) bool { return isNetwork(dir) }

// IsCloudSynced reports whether dir is inside a OneDrive, Dropbox, Google
// Drive or iCloud folder.
func IsCloudSynced(dir string) bool {
	for _, k := range []string{"OneDrive", "OneDriveConsumer", "OneDriveCommercial"} {
		if v := os.Getenv(k); v != "" && Within(dir, v) {
			return true
		}
	}
	for _, seg := range strings.FieldsFunc(filepath.Clean(dir), func(r rune) bool { return r == '/' || r == '\\' }) {
		l := strings.ToLower(seg)
		switch l {
		case "dropbox", "google drive", "icloud drive", "mobile documents":
			return true
		}
		if strings.HasPrefix(l, "onedrive") {
			return true
		}
	}
	return false
}
