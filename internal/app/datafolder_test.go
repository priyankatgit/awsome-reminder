package app

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"awesome-reminder/internal/paths"
)

type dataFolderFx struct {
	svc      *Service
	cur      string
	restarts int
}

// newDataFolderFx points the config dir at a temp folder and writes a pointer
// naming cur as the data folder.
func newDataFolderFx(t *testing.T, prev string) *dataFolderFx {
	t.Helper()
	cfg := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", cfg)
		t.Setenv("LOCALAPPDATA", t.TempDir())
	case "darwin":
		t.Setenv("HOME", cfg)
	default:
		t.Setenv("XDG_CONFIG_HOME", cfg)
		t.Setenv("XDG_CACHE_HOME", t.TempDir())
		t.Setenv("HOME", cfg)
	}
	fx := &dataFolderFx{cur: t.TempDir()}
	if err := os.WriteFile(filepath.Join(fx.cur, paths.DBFile), []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := paths.SaveLocation(paths.Location{DataDir: fx.cur, PreviousDir: prev}); err != nil {
		t.Fatal(err)
	}
	fx.svc = NewService(Deps{DataDir: fx.cur, Restart: func() { fx.restarts++ }})
	return fx
}

func TestChangeDataFolderMoveRequestsRestart(t *testing.T) {
	fx := newDataFolderFx(t, "")
	target := filepath.Join(t.TempDir(), "Data")
	if err := fx.svc.ChangeDataFolder(target, false); err != nil {
		t.Fatal(err)
	}
	loc, _, err := paths.LoadLocation()
	if err != nil {
		t.Fatal(err)
	}
	if loc.PendingMove != target || loc.DataDir != fx.cur {
		t.Fatalf("pointer = %+v, want pending %q and data dir %q", loc, target, fx.cur)
	}
	if fx.restarts != 1 {
		t.Fatalf("restarts = %d, want 1", fx.restarts)
	}
}

func TestChangeDataFolderSame(t *testing.T) {
	fx := newDataFolderFx(t, "")
	err := fx.svc.ChangeDataFolder(fx.cur, false)
	if err == nil || err.Error() != "This is already your data folder." {
		t.Fatalf("err = %v", err)
	}
	if fx.restarts != 0 {
		t.Fatalf("restarts = %d, want 0", fx.restarts)
	}
	if loc, _, _ := paths.LoadLocation(); loc.PendingMove != "" {
		t.Fatalf("pending move written: %+v", loc)
	}
}

func TestChangeDataFolderAdoptNeedsConfirm(t *testing.T) {
	fx := newDataFolderFx(t, "")
	other := dbFolder(t)
	err := fx.svc.ChangeDataFolder(other, false)
	if err == nil || err.Error() != errConfirmAdopt {
		t.Fatalf("err = %v, want %q", err, errConfirmAdopt)
	}
	if fx.restarts != 0 {
		t.Fatalf("restarts = %d, want 0", fx.restarts)
	}
	if loc, _, _ := paths.LoadLocation(); loc.DataDir != fx.cur || loc.PendingMove != "" {
		t.Fatalf("pointer changed: %+v", loc)
	}
}

func TestCheckDataFolderStandardRootIsUsedDirectly(t *testing.T) {
	fx := newDataFolderFx(t, "")
	std, _ := paths.StandardRoot()
	c := fx.svc.CheckDataFolder(std)
	if c.Err != "" || c.Path != std || c.Same {
		t.Fatalf("check = %+v, want the standard root itself", c)
	}
	// Once the standard folder is the data folder, picking it is the same folder.
	svc := NewService(Deps{DataDir: std})
	if c := svc.CheckDataFolder(std); !c.Same {
		t.Fatalf("check = %+v, want same", c)
	}
}

func TestRemoveOldCopyViaService(t *testing.T) {
	prev := t.TempDir()
	if err := os.WriteFile(filepath.Join(prev, paths.DBFile), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	fx := newDataFolderFx(t, prev)
	info, err := fx.svc.DataFolder()
	if err != nil {
		t.Fatal(err)
	}
	if info.PreviousDir != prev || info.PreviousText == "" {
		t.Fatalf("info = %+v", info)
	}
	if err := fx.svc.RemoveOldCopy(); err != nil {
		t.Fatal(err)
	}
	if loc, _, _ := paths.LoadLocation(); loc.PreviousDir != "" {
		t.Fatalf("previousDir = %q, want cleared", loc.PreviousDir)
	}
	if _, err := os.Stat(filepath.Join(prev, paths.DBFile)); !os.IsNotExist(err) {
		t.Fatalf("old database still there: %v", err)
	}
}

func TestDataFolderInfo(t *testing.T) {
	fx := newDataFolderFx(t, "")
	info, err := fx.svc.DataFolder()
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != fx.cur || info.FreeText == "" || info.UsedText == "" || info.PreviousDir != "" {
		t.Fatalf("info = %+v", info)
	}
}
