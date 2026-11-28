package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"awesome-reminder/internal/datamove"
	"awesome-reminder/internal/paths"
)

type setupFx struct {
	svc      *SetupService
	quits    int
	adopts   []string
	fresh    []string
	freshClr []bool
	keeps    int
	mu       sync.Mutex
	events   []string
	doneCh   chan SetupDone
}

func newSetupFx(t *testing.T, state paths.State) *setupFx {
	t.Helper()
	restartRequested.Store(false)
	cfg := t.TempDir()
	cache := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", cfg)
		t.Setenv("LOCALAPPDATA", cache)
	case "darwin":
		t.Setenv("HOME", cfg)
	default:
		t.Setenv("XDG_CONFIG_HOME", cfg)
		t.Setenv("XDG_CACHE_HOME", cache)
		t.Setenv("HOME", cfg)
	}
	std, err := paths.StandardRoot()
	if err != nil {
		t.Fatal(err)
	}
	fx := &setupFx{doneCh: make(chan SetupDone, 4)}
	fx.svc = &SetupService{
		resolve: func() paths.Resolution { return paths.Resolution{State: paths.StateReady, Root: std} },
		exe:     func() (string, error) { return filepath.Join(t.TempDir(), "ar.exe"), nil },
		adopt: func(target string, _ *paths.Location) error {
			fx.adopts = append(fx.adopts, target)
			return nil
		},
		startFresh: func(target string, _ *paths.Location, clr bool) error {
			fx.fresh = append(fx.fresh, target)
			fx.freshClr = append(fx.freshClr, clr)
			return nil
		},
		keep: func() error { fx.keeps++; return nil },
		move: func(context.Context, paths.Layout, paths.Layout, func(datamove.Progress)) error { return nil },
		quit: func() { fx.quits++ },
		emit: func(name string, data any) {
			fx.mu.Lock()
			fx.events = append(fx.events, name)
			fx.mu.Unlock()
			if d, ok := data.(SetupDone); ok {
				fx.doneCh <- d
			}
		},
		res: paths.Resolution{State: state, Root: std},
	}
	return fx
}

func (fx *setupFx) waitDone(t *testing.T) SetupDone {
	t.Helper()
	select {
	case d := <-fx.doneCh:
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("no setup:done")
		return SetupDone{}
	}
}

// dbFolder makes a folder holding a database file.
func dbFolder(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, paths.DBFile), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSetupInfoExistingLabelsKeep(t *testing.T) {
	fx := newSetupFx(t, paths.StateFirstRunExisting)
	info := fx.svc.Info()
	std, _ := paths.StandardRoot()
	if len(info.Options) == 0 || info.Options[0].Kind != "standard" || info.Options[0].Label != "Keep it where it is" {
		t.Fatalf("options = %+v", info.Options)
	}
	if info.ExistingDir != std {
		t.Fatalf("ExistingDir = %q, want %q", info.ExistingDir, std)
	}
}

func TestSetupInfoFreshLabelsStandard(t *testing.T) {
	fx := newSetupFx(t, paths.StateFirstRunFresh)
	if got := fx.svc.Info().Options[0].Label; got != "Standard location" {
		t.Fatalf("label = %q", got)
	}
}

func TestSetupInfoBesideHiddenUnderProgramFiles(t *testing.T) {
	fx := newSetupFx(t, paths.StateFirstRunFresh)
	pf := t.TempDir()
	t.Setenv("ProgramFiles", pf)
	if err := os.MkdirAll(filepath.Join(pf, "AR"), 0o755); err != nil {
		t.Fatal(err)
	}
	fx.svc.exe = func() (string, error) { return filepath.Join(pf, "AR", "ar.exe"), nil }
	for _, o := range fx.svc.Info().Options {
		if o.Kind == "beside" {
			t.Fatalf("beside offered under Program Files: %+v", o)
		}
	}
	tmp := t.TempDir()
	fx.svc.exe = func() (string, error) { return filepath.Join(tmp, "ar.exe"), nil }
	var beside *FolderOption
	for _, o := range fx.svc.Info().Options {
		if o.Kind == "beside" {
			beside = &o
		}
	}
	if beside == nil || beside.Path != filepath.Join(tmp, "Data") {
		t.Fatalf("beside = %+v", beside)
	}
}

func TestChooseExistingKeep(t *testing.T) {
	fx := newSetupFx(t, paths.StateFirstRunExisting)
	if err := fx.svc.Choose("standard", "", false); err != nil {
		t.Fatal(err)
	}
	if fx.keeps != 1 || !RestartRequested() || fx.quits != 1 {
		t.Fatalf("keeps=%d restart=%v quits=%d", fx.keeps, RestartRequested(), fx.quits)
	}
}

func TestChooseFreshStandardStartsFresh(t *testing.T) {
	fx := newSetupFx(t, paths.StateFirstRunFresh)
	if err := fx.svc.Choose("standard", "", false); err != nil {
		t.Fatal(err)
	}
	std, _ := paths.StandardRoot()
	if len(fx.fresh) != 1 || fx.fresh[0] != std || fx.freshClr[0] || !RestartRequested() || fx.quits != 1 {
		t.Fatalf("fresh=%v clr=%v", fx.fresh, fx.freshClr)
	}
}

func TestChooseAdoptNeedsConfirm(t *testing.T) {
	fx := newSetupFx(t, paths.StateFirstRunFresh)
	target := dbFolder(t)
	err := fx.svc.Choose("other", target, false)
	if err == nil || err.Error() != "confirm-adopt" || len(fx.adopts) != 0 {
		t.Fatalf("err=%v adopts=%v", err, fx.adopts)
	}
	if err := fx.svc.Choose("other", target, true); err != nil {
		t.Fatal(err)
	}
	if len(fx.adopts) != 1 || !RestartRequested() || fx.quits != 1 {
		t.Fatalf("adopts=%v", fx.adopts)
	}
}

func TestChooseFreshEmptyTargetStartsFresh(t *testing.T) {
	fx := newSetupFx(t, paths.StateFirstRunFresh)
	if err := fx.svc.Choose("other", t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	if len(fx.fresh) != 1 || fx.freshClr[0] {
		t.Fatalf("fresh=%v clr=%v", fx.fresh, fx.freshClr)
	}
}

func TestChooseUnavailableEmptyNeedsConfirm(t *testing.T) {
	fx := newSetupFx(t, paths.StateUnavailable)
	target := t.TempDir()
	err := fx.svc.Choose("other", target, false)
	if err == nil || err.Error() != "confirm-fresh" || len(fx.fresh) != 0 {
		t.Fatalf("err=%v fresh=%v", err, fx.fresh)
	}
	if err := fx.svc.Choose("other", target, true); err != nil {
		t.Fatal(err)
	}
	if len(fx.fresh) != 1 || !fx.freshClr[0] || !RestartRequested() || fx.quits != 1 {
		t.Fatalf("fresh=%v clr=%v", fx.fresh, fx.freshClr)
	}
}

func TestChooseUnavailableDataAdopts(t *testing.T) {
	fx := newSetupFx(t, paths.StateUnavailable)
	target := dbFolder(t)
	if err := fx.svc.Choose("other", target, false); err == nil || err.Error() != "confirm-adopt" {
		t.Fatalf("err=%v", err)
	}
	if err := fx.svc.Choose("other", target, true); err != nil || len(fx.adopts) != 1 {
		t.Fatalf("err=%v adopts=%v", err, fx.adopts)
	}
}

func TestChooseMoveEmitsProgressThenDone(t *testing.T) {
	fx := newSetupFx(t, paths.StateFirstRunExisting)
	fx.svc.move = func(_ context.Context, _, _ paths.Layout, on func(datamove.Progress)) error {
		on(datamove.Progress{Step: "database", Done: 1, Total: 2})
		on(datamove.Progress{Step: "database", Done: 2, Total: 2})
		return nil
	}
	if err := fx.svc.Choose("other", t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	if d := fx.waitDone(t); !d.OK {
		t.Fatalf("done = %+v", d)
	}
	fx.mu.Lock()
	events := append([]string(nil), fx.events...)
	fx.mu.Unlock()
	want := []string{EventSetupProgress, EventSetupProgress, EventSetupDone}
	if len(events) != 3 || events[0] != want[0] || events[1] != want[1] || events[2] != want[2] {
		t.Fatalf("events = %v", events)
	}
	// finish runs right after the done event is emitted.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !RestartRequested() {
		time.Sleep(5 * time.Millisecond)
	}
	if !RestartRequested() {
		t.Fatal("restart not requested")
	}
}

func TestChooseMoveFailureNoRestart(t *testing.T) {
	fx := newSetupFx(t, paths.StateFirstRunExisting)
	fx.svc.move = func(context.Context, paths.Layout, paths.Layout, func(datamove.Progress)) error {
		return errors.New("disk full")
	}
	if err := fx.svc.Choose("other", t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	if d := fx.waitDone(t); d.OK || d.Error != "disk full" {
		t.Fatalf("done = %+v", d)
	}
	time.Sleep(20 * time.Millisecond)
	if RestartRequested() || fx.quits != 0 {
		t.Fatalf("restart=%v quits=%d", RestartRequested(), fx.quits)
	}
}

func TestChooseWhileMovingRefused(t *testing.T) {
	fx := newSetupFx(t, paths.StateFirstRunExisting)
	fx.svc.move = func(ctx context.Context, _, _ paths.Layout, _ func(datamove.Progress)) error {
		<-ctx.Done()
		return ctx.Err()
	}
	if err := fx.svc.Choose("other", t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.Choose("other", t.TempDir(), false); err == nil || err.Error() != "A move is already running." {
		t.Fatalf("err = %v", err)
	}
	fx.svc.CancelMove()
	if d := fx.waitDone(t); d.OK {
		t.Fatalf("done = %+v", d)
	}
}

func TestRunPendingMove(t *testing.T) {
	fx := newSetupFx(t, paths.StateMovePending)
	to := t.TempDir()
	fx.svc.res.Loc = paths.Location{DataDir: fx.svc.res.Root, PendingMove: to}
	var gotTo string
	fx.svc.move = func(_ context.Context, _, dst paths.Layout, _ func(datamove.Progress)) error {
		gotTo = dst.Root
		return nil
	}
	if err := fx.svc.RunPendingMove(); err != nil {
		t.Fatal(err)
	}
	if d := fx.waitDone(t); !d.OK || !paths.SameDir(gotTo, to) {
		t.Fatalf("done=%+v to=%q", d, gotTo)
	}
}

func TestAbandonMoveClearsPending(t *testing.T) {
	fx := newSetupFx(t, paths.StateMovePending)
	std, _ := paths.StandardRoot()
	if err := paths.SaveLocation(paths.Location{DataDir: std, PendingMove: "gone"}); err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.AbandonMove(); err != nil {
		t.Fatal(err)
	}
	loc, found, err := paths.LoadLocation()
	if err != nil || !found || loc.PendingMove != "" || loc.DataDir != std {
		t.Fatalf("loc=%+v found=%v err=%v", loc, found, err)
	}
	if !RestartRequested() || fx.quits != 1 {
		t.Fatalf("restart=%v quits=%d", RestartRequested(), fx.quits)
	}
}

func TestRetryReadyRestarts(t *testing.T) {
	fx := newSetupFx(t, paths.StateUnavailable)
	fx.svc.Retry()
	if !RestartRequested() || fx.quits != 1 {
		t.Fatalf("restart=%v quits=%d", RestartRequested(), fx.quits)
	}
}

func TestRetryStillUnavailableStays(t *testing.T) {
	fx := newSetupFx(t, paths.StateUnavailable)
	fx.svc.resolve = func() paths.Resolution {
		return paths.Resolution{State: paths.StateUnavailable, Reason: "Folder not found: X"}
	}
	info := fx.svc.Retry()
	if RestartRequested() || fx.quits != 0 || info.Reason != "Folder not found: X" {
		t.Fatalf("restart=%v quits=%d info=%+v", RestartRequested(), fx.quits, info)
	}
}

func TestAbandonMoveRefusedWhileMoving(t *testing.T) {
	fx := newSetupFx(t, paths.StateMovePending)
	fx.svc.res.Loc = paths.Location{DataDir: fx.svc.res.Root, PendingMove: t.TempDir()}
	fx.svc.move = func(ctx context.Context, _, _ paths.Layout, _ func(datamove.Progress)) error {
		<-ctx.Done()
		return ctx.Err()
	}
	if err := fx.svc.RunPendingMove(); err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.AbandonMove(); err == nil || err.Error() != errMoveRunning {
		t.Fatalf("err = %v", err)
	}
	if RestartRequested() || fx.quits != 0 {
		t.Fatalf("restart=%v quits=%d", RestartRequested(), fx.quits)
	}
	fx.svc.CancelMove()
	fx.waitDone(t)
}

func TestAbandonMoveRefusedOutsideMoveState(t *testing.T) {
	fx := newSetupFx(t, paths.StateUnavailable)
	std, _ := paths.StandardRoot()
	if err := paths.SaveLocation(paths.Location{DataDir: std, PendingMove: "keep"}); err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.AbandonMove(); err == nil {
		t.Fatal("want error")
	}
	loc, _, _ := paths.LoadLocation()
	if loc.PendingMove != "keep" || RestartRequested() || fx.quits != 0 {
		t.Fatalf("loc=%+v restart=%v quits=%d", loc, RestartRequested(), fx.quits)
	}
}

func TestQuitWaitsForMoveRollback(t *testing.T) {
	fx := newSetupFx(t, paths.StateFirstRunExisting)
	rolledBack := false
	fx.svc.move = func(ctx context.Context, _, _ paths.Layout, _ func(datamove.Progress)) error {
		<-ctx.Done()
		time.Sleep(100 * time.Millisecond) // rollback
		rolledBack = true
		return ctx.Err()
	}
	fx.svc.quit = func() {
		if !rolledBack {
			t.Error("quit before the move rolled back")
		}
		fx.quits++
	}
	if err := fx.svc.Choose("other", t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	fx.svc.Quit()
	if fx.quits != 1 || RestartRequested() {
		t.Fatalf("quits=%d restart=%v", fx.quits, RestartRequested())
	}
}

func TestSetupConcurrentRetryAndInfo(t *testing.T) {
	fx := newSetupFx(t, paths.StateUnavailable)
	fx.svc.resolve = func() paths.Resolution {
		return paths.Resolution{State: paths.StateUnavailable, Reason: "x"}
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); fx.svc.Retry() }()
		go func() { defer wg.Done(); fx.svc.Info(); _ = fx.svc.Choose("other", "", false) }()
	}
	wg.Wait()
}

func TestChooseUnavailableSameFolderRetries(t *testing.T) {
	for _, confirm := range []bool{false, true} {
		fx := newSetupFx(t, paths.StateUnavailable)
		root := dbFolder(t) // the drive came back
		fx.svc.res.Root = root
		fx.svc.resolve = func() paths.Resolution { return paths.Resolution{State: paths.StateReady, Root: root} }
		if err := fx.svc.Choose("other", root, confirm); err != nil {
			t.Fatalf("confirm=%v: %v", confirm, err)
		}
		if len(fx.adopts) != 0 || len(fx.fresh) != 0 || !RestartRequested() || fx.quits != 1 {
			t.Fatalf("confirm=%v: adopts=%v fresh=%v restart=%v quits=%d", confirm, fx.adopts, fx.fresh, RestartRequested(), fx.quits)
		}
	}
}

func TestChooseUnavailableSameFolderStillMissing(t *testing.T) {
	fx := newSetupFx(t, paths.StateUnavailable)
	root := filepath.Join(t.TempDir(), "gone")
	fx.svc.res.Root = root
	fx.svc.resolve = func() paths.Resolution {
		return paths.Resolution{State: paths.StateUnavailable, Root: root, Reason: "Folder not found: " + root}
	}
	err := fx.svc.Choose("other", root, true)
	if err == nil || err.Error() != "Folder not found: "+root {
		t.Fatalf("err = %v", err)
	}
	if len(fx.adopts) != 0 || len(fx.fresh) != 0 || RestartRequested() || fx.quits != 0 {
		t.Fatalf("adopts=%v fresh=%v restart=%v quits=%d", fx.adopts, fx.fresh, RestartRequested(), fx.quits)
	}
}

func TestChooseUnavailableStandardUsesStandardRoot(t *testing.T) {
	fx := newSetupFx(t, paths.StateUnavailable)
	fx.svc.res.Root = filepath.Join(t.TempDir(), "gone")
	std, _ := paths.StandardRoot()
	// The standard folder holds the pointer and the setup log, and data.
	if err := paths.SaveLocation(paths.Location{DataDir: fx.svc.res.Root}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"setup.log", paths.DBFile} {
		if err := os.WriteFile(filepath.Join(std, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := fx.svc.Choose("standard", "", false); err == nil || err.Error() != errConfirmAdopt {
		t.Fatalf("err = %v", err)
	}
	if err := fx.svc.Choose("standard", "", true); err != nil {
		t.Fatal(err)
	}
	if len(fx.adopts) != 1 || !paths.SameDir(fx.adopts[0], std) {
		t.Fatalf("adopts = %v, want %q", fx.adopts, std)
	}
}

func TestChooseUnavailableStandardWithoutDataStartsFresh(t *testing.T) {
	fx := newSetupFx(t, paths.StateUnavailable)
	fx.svc.res.Root = filepath.Join(t.TempDir(), "gone")
	std, _ := paths.StandardRoot()
	if err := paths.SaveLocation(paths.Location{DataDir: fx.svc.res.Root}); err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.Choose("standard", "", false); err == nil || err.Error() != errConfirmFresh {
		t.Fatalf("err = %v", err)
	}
	if err := fx.svc.Choose("standard", "", true); err != nil {
		t.Fatal(err)
	}
	if len(fx.fresh) != 1 || !paths.SameDir(fx.fresh[0], std) || !fx.freshClr[0] {
		t.Fatalf("fresh = %v clr=%v, want %q", fx.fresh, fx.freshClr, std)
	}
}
