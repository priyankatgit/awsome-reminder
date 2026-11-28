package paths

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestSameDirAndWithin(t *testing.T) {
	if runtime.GOOS == "windows" && !SameDir(`C:\A\B\`, `c:\a\b`) {
		t.Error("SameDir should ignore case and trailing slash on windows")
	}
	T := t.TempDir()
	if !Within(filepath.Join(T, "x"), T) {
		t.Error("child should be within parent")
	}
	if !Within(T, T) {
		t.Error("equal should be within")
	}
	if Within(T+"x", T) {
		t.Error("prefix-only sibling must not be within")
	}
}

func TestFreeSpace(t *testing.T) {
	n, err := FreeSpace(filepath.Join(t.TempDir(), "not", "yet"))
	if err != nil || n == 0 {
		t.Errorf("FreeSpace = %d, %v", n, err)
	}
}

func TestIsCloudSynced(t *testing.T) {
	T := t.TempDir()
	t.Setenv("OneDrive", T)
	if !IsCloudSynced(filepath.Join(T, "Docs")) {
		t.Error("under OneDrive env should be synced")
	}
	if !IsCloudSynced(filepath.Join(string(filepath.Separator), "x", "Dropbox", "y")) {
		t.Error("Dropbox segment should be synced")
	}
	t.Setenv("OneDrive", "")
	if IsCloudSynced(t.TempDir()) {
		t.Error("plain temp dir should not be synced")
	}
}

func TestIsNetworkUNC(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows only")
	}
	if !IsNetwork(`\\server\share\x`) {
		t.Error("UNC should be network")
	}
	if !IsNetwork(`\\?\UNC\server\share\x`) {
		t.Error("extended UNC should be network")
	}
	if IsNetwork(`\\?\C:\x`) || IsNetwork(`\\.\C:\x`) {
		t.Error("extended-length local path is not network")
	}
	if IsNetwork(t.TempDir()) {
		t.Error("temp dir should not be network")
	}
}
