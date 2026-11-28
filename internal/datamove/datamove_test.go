package datamove

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"awesome-reminder/internal/model"
	"awesome-reminder/internal/paths"
	"awesome-reminder/internal/store"
)

// isolate points the config and cache dirs at fresh temp dirs.
func isolate(t *testing.T) {
	t.Helper()
	cfg, cache := t.TempDir(), t.TempDir()
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
}

func write(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func putItems(t *testing.T, dbPath string, n int) {
	t.Helper()
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		id := strings.Repeat(string(rune('A'+i)), 26)
		if err := s.Put(model.Item{ID: id, Kind: model.KindTodo, Text: "item " + id, TZ: "UTC",
			Status: model.StatusOpen, CreatedAt: now, UpdatedAt: now, UpdatedBy: "t"}); err != nil {
			t.Fatal(err)
		}
	}
}

func countItems(t *testing.T, dbPath string) int {
	t.Helper()
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	items, err := s.ListOpen()
	if err != nil {
		t.Fatal(err)
	}
	return len(items)
}

var syncKey = bytes.Repeat([]byte{7, 9, 1, 3}, 8)

func newSource(t *testing.T) paths.Layout {
	t.Helper()
	l, err := paths.NewLayout(filepath.Join(t.TempDir(), "src"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(l.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	putItems(t, l.DB(), 3)
	write(t, filepath.Join(l.Attachments(), "a.webp"), bytes.Repeat([]byte("x"), 200<<10))
	write(t, l.SyncKey(), syncKey)
	write(t, filepath.Join(l.Voice(), "models", "m.bin"), []byte("model"))
	return l
}

func newDest(t *testing.T, name string) paths.Layout {
	t.Helper()
	l, err := paths.NewLayout(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func pointerBytes(t *testing.T) []byte {
	t.Helper()
	p, _ := paths.PointerPath()
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestTarget(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "empty")
	os.MkdirAll(empty, 0o755)
	withDB := filepath.Join(root, "db")
	write(t, filepath.Join(withDB, "awesome.db"), []byte("x"))
	withFile := filepath.Join(root, "file")
	write(t, filepath.Join(withFile, "x.txt"), []byte("x"))
	blocked := filepath.Join(root, "blocked")
	write(t, filepath.Join(blocked, "x.txt"), []byte("x"))
	write(t, filepath.Join(blocked, SubfolderName, "y.txt"), []byte("y"))

	for name, tc := range map[string]struct{ in, want string }{
		"empty":   {empty, empty},
		"missing": {filepath.Join(root, "nope"), filepath.Join(root, "nope")},
		"db":      {withDB, withDB},
		"file":    {withFile, filepath.Join(withFile, SubfolderName)},
	} {
		got, err := Target(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v want %q", name, got, err, tc.want)
		}
	}
	if _, err := Target(blocked); err == nil || !strings.Contains(err.Error(), "isn't empty") {
		t.Fatalf("blocked: err = %v", err)
	}
}

func TestCheckTargetSameAsCurrent(t *testing.T) {
	isolate(t)
	src := newSource(t)
	in := src.Root + string(filepath.Separator)
	if runtime.GOOS == "windows" {
		in = strings.ToUpper(src.Root) + `\`
	}
	c := CheckTarget(in, &src)
	if !c.Same || c.Err != "" {
		t.Fatalf("got %+v", c)
	}
}

func TestCheckTargetInsideCurrent(t *testing.T) {
	isolate(t)
	src := newSource(t)
	c := CheckTarget(src.Root+"/sub", &src)
	if c.Err != "Choose a folder outside the current data folder." {
		t.Fatalf("got %+v", c)
	}
	if _, err := os.Stat(filepath.Join(src.Root, "sub")); err == nil {
		t.Fatal("check left the folder behind")
	}
}

func TestCheckTargetCurrentInsideTarget(t *testing.T) {
	isolate(t)
	src := newSource(t)
	// Parent holds only the src folder, so Target picks parent/Awesome Reminder;
	// pointing at src's parent's content must not slip through either way.
	c := CheckTarget(filepath.Dir(src.Root), &src)
	if c.Err == "" && !strings.HasSuffix(c.Path, SubfolderName) {
		t.Fatalf("got %+v", c)
	}
}

func TestCheckTargetHasData(t *testing.T) {
	isolate(t)
	src := newSource(t)
	other := newDest(t, "other")
	os.MkdirAll(other.Root, 0o755)
	putItems(t, other.DB(), 1)
	c := CheckTarget(other.Root, &src)
	if !c.HasData || c.Err != "" {
		t.Fatalf("got %+v", c)
	}
}

func TestCheckTargetCleansUpAndNeed(t *testing.T) {
	isolate(t)
	src := newSource(t)
	dst := newDest(t, "a/b/c")
	c := CheckTarget(dst.Root, &src)
	if c.Err != "" || c.Need < SpaceMargin+200<<10 || c.Free == 0 {
		t.Fatalf("got %+v", c)
	}
	if _, err := os.Stat(filepath.Dir(filepath.Dir(filepath.Dir(dst.Root)))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(filepath.Dir(dst.Root))); err == nil {
		t.Fatal("check left parent folders behind")
	}
}

func TestFormatBytes(t *testing.T) {
	for in, want := range map[int64]string{500: "500 B", 84 << 20: "84 MB", 1288490189: "1.2 GB"} {
		if got := FormatBytes(in); got != want {
			t.Errorf("%d: %q want %q", in, got, want)
		}
	}
}

func TestMoveCopiesAndSwitches(t *testing.T) {
	isolate(t)
	src := newSource(t)
	dst := newDest(t, "dst")
	var seen []string
	var last Progress
	err := Move(context.Background(), src, dst, func(p Progress) {
		if len(seen) == 0 || seen[len(seen)-1] != p.Step {
			seen = append(seen, p.Step)
		}
		last = p
	})
	if err != nil {
		t.Fatal(err)
	}
	if countItems(t, dst.DB()) != 3 {
		t.Fatal("items missing in copy")
	}
	if b, _ := os.ReadFile(dst.SyncKey()); !bytes.Equal(b, syncKey) {
		t.Fatal("sync.key differs")
	}
	if fileSize(filepath.Join(dst.Attachments(), "a.webp")) != 200<<10 {
		t.Fatal("attachment missing")
	}
	if b, _ := os.ReadFile(filepath.Join(dst.Voice(), "models", "m.bin")); string(b) != "model" {
		t.Fatal("voice file missing")
	}
	loc, found, err := paths.LoadLocation()
	if err != nil || !found || loc.DataDir != dst.Root || loc.PreviousDir != src.Root {
		t.Fatalf("pointer %+v %v %v", loc, found, err)
	}
	if !exists(src.DB()+".moved") || exists(src.DB()) {
		t.Fatal("old database not renamed")
	}
	if exists(src.Voice()) {
		t.Fatal("old voice folder not removed")
	}
	if strings.Join(seen, ",") != "database,attachments,voice,checking" {
		t.Fatalf("steps %v", seen)
	}
	if last.Done != last.Total || last.Total == 0 {
		t.Fatalf("final progress %+v", last)
	}
	for _, p := range []string{dst.DB() + ".tmp", dst.DB() + ".tmp-wal", dst.DB() + "-wal", filepath.Join(dst.Root, moveMarker)} {
		if exists(p) {
			t.Fatalf("leftover %s", p)
		}
	}
}

func TestMoveQuotedPath(t *testing.T) {
	isolate(t)
	src := newSource(t)
	dst, _ := paths.NewLayout(filepath.Join(t.TempDir(), "Priyank's data", "Données"))
	if err := Move(context.Background(), src, dst, nil); err != nil {
		t.Fatal(err)
	}
	if countItems(t, dst.DB()) != 3 {
		t.Fatal("items missing")
	}
}

// assertRolledBack checks the source and pointer are untouched and the target
// holds only what it held before.
func assertRolledBack(t *testing.T, src, dst paths.Layout, pointerBefore []byte) {
	t.Helper()
	if !bytes.Equal(pointerBytes(t), pointerBefore) {
		t.Fatal("pointer changed")
	}
	if !exists(src.DB()) || exists(src.DB()+".moved") || !exists(src.Voice()) {
		t.Fatal("source was touched")
	}
	entries, err := os.ReadDir(dst.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "keep.txt" {
		t.Fatalf("target holds %v", entries)
	}
}

func TestMoveFailureLeavesSourceAndPointer(t *testing.T) {
	isolate(t)
	src := newSource(t)
	dst := newDest(t, "dst")
	write(t, filepath.Join(dst.Root, "keep.txt"), []byte("k"))
	if err := paths.SaveLocation(paths.Location{DataDir: src.Root, PendingMove: dst.Root}); err != nil {
		t.Fatal(err)
	}
	before := pointerBytes(t)
	afterCopy = func(step string) error {
		if step == "voice" {
			return errors.New("boom")
		}
		return nil
	}
	t.Cleanup(func() { afterCopy = func(string) error { return nil } })
	err := Move(context.Background(), src, dst, nil)
	if err == nil || !strings.Contains(err.Error(), "voice") {
		t.Fatalf("err = %v", err)
	}
	assertRolledBack(t, src, dst, before)
}

func TestMoveRetryAfterLeftovers(t *testing.T) {
	isolate(t)
	src := newSource(t)
	dst := newDest(t, "dst")
	write(t, dst.DB()+".tmp", []byte("garbage"))
	write(t, filepath.Join(dst.Attachments(), "a.webp.tmp"), []byte("garbage"))
	if err := Move(context.Background(), src, dst, nil); err != nil {
		t.Fatal(err)
	}
	if countItems(t, dst.DB()) != 3 {
		t.Fatal("items missing")
	}
}

func TestMoveCancelled(t *testing.T) {
	isolate(t)
	src := newSource(t)
	dst := newDest(t, "dst")
	write(t, filepath.Join(dst.Root, "keep.txt"), []byte("k"))
	before := pointerBytes(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Move(ctx, src, dst, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	assertRolledBack(t, src, dst, before)
}

func TestMoveCancelledMidway(t *testing.T) {
	isolate(t)
	src := newSource(t)
	dst := newDest(t, "dst")
	write(t, filepath.Join(dst.Root, "keep.txt"), []byte("k"))
	before := pointerBytes(t)
	ctx, cancel := context.WithCancel(context.Background())
	afterCopy = func(step string) error {
		if step == "attachments" {
			cancel()
		}
		return nil
	}
	t.Cleanup(func() { afterCopy = func(string) error { return nil } })
	err := Move(ctx, src, dst, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	assertRolledBack(t, src, dst, before)
}

func TestMoveRefusesExistingDatabase(t *testing.T) {
	isolate(t)
	src := newSource(t)
	dst := newDest(t, "dst")
	os.MkdirAll(dst.Root, 0o755)
	putItems(t, dst.DB(), 1)
	if err := Move(context.Background(), src, dst, nil); err == nil {
		t.Fatal("expected refusal")
	}
	if countItems(t, dst.DB()) != 1 {
		t.Fatal("target data was changed")
	}
}

func stubKeychain(t *testing.T, err error) *int {
	t.Helper()
	calls := 0
	old := clearKeychain
	clearKeychain = func() error { calls++; return err }
	t.Cleanup(func() { clearKeychain = old })
	return &calls
}

func TestAdoptKeepsOtherDataLive(t *testing.T) {
	isolate(t)
	calls := stubKeychain(t, nil)
	target := newDest(t, "other")
	os.MkdirAll(target.Root, 0o755)
	putItems(t, target.DB(), 2)
	before, _ := os.ReadFile(target.DB())
	if err := Adopt(target.Root, &paths.Location{PreviousDir: "P"}); err != nil {
		t.Fatal(err)
	}
	loc, _, _ := paths.LoadLocation()
	if loc.DataDir != target.Root || loc.PreviousDir != "P" {
		t.Fatalf("pointer %+v", loc)
	}
	after, _ := os.ReadFile(target.DB())
	if !bytes.Equal(before, after) {
		t.Fatal("adopted database was modified")
	}
	if *calls != 1 {
		t.Fatalf("keychain calls %d", *calls)
	}
}

func TestAdoptKeychainErrorLeavesPointer(t *testing.T) {
	isolate(t)
	boom := errors.New("keychain locked")
	stubKeychain(t, boom)
	target := newDest(t, "other")
	os.MkdirAll(target.Root, 0o755)
	putItems(t, target.DB(), 1)
	if err := paths.SaveLocation(paths.Location{DataDir: "D"}); err != nil {
		t.Fatal(err)
	}
	before := pointerBytes(t)
	if err := Adopt(target.Root, nil); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if !bytes.Equal(pointerBytes(t), before) {
		t.Fatal("pointer changed")
	}
}

func TestStartFreshCreatesDB(t *testing.T) {
	isolate(t)
	calls := stubKeychain(t, nil)
	target := newDest(t, "fresh")
	if err := StartFresh(target.Root, nil, false); err != nil {
		t.Fatal(err)
	}
	if countItems(t, target.DB()) != 0 || *calls != 0 {
		t.Fatalf("calls %d", *calls)
	}
	target2 := newDest(t, "fresh2")
	if err := StartFresh(target2.Root, &paths.Location{PreviousDir: "P"}, true); err != nil {
		t.Fatal(err)
	}
	loc, _, _ := paths.LoadLocation()
	if *calls != 1 || loc.DataDir != target2.Root || loc.PreviousDir != "P" {
		t.Fatalf("calls %d loc %+v", *calls, loc)
	}
}

func TestRequestMoveAndKeepStandard(t *testing.T) {
	isolate(t)
	std, _ := paths.StandardRoot()
	if err := RequestMove("X"); err != nil {
		t.Fatal(err)
	}
	loc, _, _ := paths.LoadLocation()
	if loc.PendingMove != "X" || loc.DataDir != std {
		t.Fatalf("%+v", loc)
	}
	if err := KeepStandard(); err != nil {
		t.Fatal(err)
	}
	loc, _, _ = paths.LoadLocation()
	if loc.PendingMove != "" || loc.DataDir != std {
		t.Fatalf("%+v", loc)
	}
}

func TestRemoveOldCopyOnlyOwnedFiles(t *testing.T) {
	isolate(t)
	prev := filepath.Join(t.TempDir(), "old")
	for _, f := range []string{"awesome.db.moved", "awesome.db-wal.moved", "sync.key", "awesome.log", "setup.log", "notes.txt"} {
		write(t, filepath.Join(prev, f), []byte("x"))
	}
	write(t, filepath.Join(prev, "attachments", "a.webp"), []byte("x"))
	loc := paths.Location{DataDir: filepath.Join(t.TempDir(), "new"), PreviousDir: prev}
	if err := paths.SaveLocation(loc); err != nil {
		t.Fatal(err)
	}
	if OldCopySize(prev) != 6 {
		t.Fatalf("size %d", OldCopySize(prev))
	}
	if err := RemoveOldCopy(loc); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(prev)
	if len(entries) != 1 || entries[0].Name() != "notes.txt" {
		t.Fatalf("left %v", entries)
	}
	got, _, _ := paths.LoadLocation()
	if got.PreviousDir != "" || got.DataDir != loc.DataDir {
		t.Fatalf("pointer %+v", got)
	}
}

func TestRemoveOldCopyRemovesEmptyDir(t *testing.T) {
	isolate(t)
	prev := filepath.Join(t.TempDir(), "old")
	write(t, filepath.Join(prev, "awesome.db.moved"), []byte("x"))
	if err := RemoveOldCopy(paths.Location{DataDir: "D", PreviousDir: prev}); err != nil {
		t.Fatal(err)
	}
	if exists(prev) {
		t.Fatal("empty folder kept")
	}
}

func TestRemoveOldCopyRefusesLive(t *testing.T) {
	isolate(t)
	dir := filepath.Join(t.TempDir(), "live")
	write(t, filepath.Join(dir, "awesome.db"), []byte("x"))
	if err := RemoveOldCopy(paths.Location{DataDir: dir, PreviousDir: dir}); err == nil {
		t.Fatal("expected refusal")
	}
	if !exists(filepath.Join(dir, "awesome.db")) {
		t.Fatal("live database removed")
	}
}

func TestRemoveOldCopyRemovesLegacyProfile(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows only")
	}
	isolate(t)
	std, _ := paths.StandardRoot()
	write(t, filepath.Join(std, "awesome.db.moved"), []byte("x"))
	profile := filepath.Join(os.Getenv("APPDATA"), "awesome-reminder.exe")
	write(t, filepath.Join(profile, "EBWebView", "x"), []byte("x"))
	loc := paths.Location{DataDir: filepath.Join(t.TempDir(), "new"), PreviousDir: std}
	if err := paths.SaveLocation(loc); err != nil {
		t.Fatal(err)
	}
	if err := RemoveOldCopy(loc); err != nil {
		t.Fatal(err)
	}
	if exists(profile) {
		t.Fatal("legacy profile kept")
	}
	got, _, _ := paths.LoadLocation()
	if got.PreviousDir != "" {
		t.Fatalf("pointer %+v", got)
	}
}

func TestRemoveOldCopyKeepsLiveProfile(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows only")
	}
	isolate(t)
	std, _ := paths.StandardRoot()
	prev := filepath.Join(t.TempDir(), "old")
	write(t, filepath.Join(prev, "awesome.db.moved"), []byte("x"))
	profile := filepath.Join(os.Getenv("APPDATA"), "awesome-reminder.exe")
	write(t, filepath.Join(profile, "EBWebView", "x"), []byte("x"))
	loc := paths.Location{DataDir: std, PreviousDir: prev}
	if err := paths.SaveLocation(loc); err != nil {
		t.Fatal(err)
	}
	if err := RemoveOldCopy(loc); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(profile, "EBWebView", "x")) {
		t.Fatal("live profile removed")
	}
}

func TestRemoveOldCopyRefusesStaleArgument(t *testing.T) {
	isolate(t)
	dir := filepath.Join(t.TempDir(), "live")
	write(t, filepath.Join(dir, "awesome.db"), []byte("x"))
	// The pointer on disk says dir is live; the caller's copy is stale.
	if err := paths.SaveLocation(paths.Location{DataDir: dir, PreviousDir: dir}); err != nil {
		t.Fatal(err)
	}
	stale := paths.Location{DataDir: filepath.Join(t.TempDir(), "elsewhere"), PreviousDir: dir}
	if err := RemoveOldCopy(stale); err == nil {
		t.Fatal("expected refusal")
	}
	if !exists(filepath.Join(dir, "awesome.db")) {
		t.Fatal("live database removed")
	}
	// Differing previousDir is refused too.
	loc := paths.Location{DataDir: dir, PreviousDir: "other"}
	if err := RemoveOldCopy(loc); err == nil {
		t.Fatal("expected refusal")
	}
}

func TestRemoveOldCopyRefusesSameFileViaLink(t *testing.T) {
	isolate(t)
	live := filepath.Join(t.TempDir(), "live")
	write(t, filepath.Join(live, "awesome.db"), []byte("x"))
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(live, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	loc := paths.Location{DataDir: live, PreviousDir: link}
	if err := paths.SaveLocation(loc); err != nil {
		t.Fatal(err)
	}
	if err := RemoveOldCopy(loc); err == nil {
		t.Fatal("expected refusal")
	}
	if !exists(filepath.Join(live, "awesome.db")) {
		t.Fatal("live database removed")
	}
}

func TestMoveAndCheckRefuseTargetInsideSourceVoice(t *testing.T) {
	isolate(t)
	std, _ := paths.StandardRoot()
	src, _ := paths.NewLayout(std)
	os.MkdirAll(src.Root, 0o755)
	putItems(t, src.DB(), 1)
	write(t, filepath.Join(src.Voice(), "m.bin"), []byte("m"))
	dst, _ := paths.NewLayout(filepath.Join(src.Voice(), "inner"))
	if c := CheckTarget(dst.Root, &src); c.Err != outsideMsg {
		t.Fatalf("check: %+v", c)
	}
	if err := Move(context.Background(), src, dst, nil); err == nil {
		t.Fatal("expected refusal")
	}
	if !exists(filepath.Join(src.Voice(), "m.bin")) || !exists(src.DB()) {
		t.Fatal("source touched")
	}
}

func TestMoveRetryAfterRenameBeforeSwitch(t *testing.T) {
	isolate(t)
	src := newSource(t)
	dst := newDest(t, "dst")
	// State left by a run killed after tmp->awesome.db, before the pointer:
	// the marker names the source, and the half-done copy is live.
	write(t, filepath.Join(dst.Root, moveMarker), []byte(src.Root))
	write(t, dst.DB(), []byte("half-done copy"))
	write(t, dst.DB()+"-wal", []byte("wal"))
	if err := paths.SaveLocation(paths.Location{DataDir: src.Root, PendingMove: dst.Root}); err != nil {
		t.Fatal(err)
	}
	if err := Move(context.Background(), src, dst, nil); err != nil {
		t.Fatal(err)
	}
	if countItems(t, dst.DB()) != 3 {
		t.Fatal("items missing")
	}
	loc, _, _ := paths.LoadLocation()
	if loc.DataDir != dst.Root || loc.PendingMove != "" {
		t.Fatalf("pointer %+v", loc)
	}
	// The leftovers are set aside, not deleted.
	var db, wal bool
	entries, _ := os.ReadDir(dst.Root)
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "awesome.db.leftover-") {
			b, _ := os.ReadFile(filepath.Join(dst.Root, n))
			db = string(b) == "half-done copy"
		}
		if strings.HasPrefix(n, "awesome.db-wal.leftover-") {
			wal = true
		}
	}
	if !db || !wal {
		t.Fatalf("leftovers not set aside: %v", entries)
	}
	if exists(filepath.Join(dst.Root, moveMarker)) {
		t.Fatal("marker left behind")
	}
}

func TestMoveRefusesTargetDatabaseWithoutMarker(t *testing.T) {
	isolate(t)
	src := newSource(t)
	for name, marker := range map[string]string{"no marker": "", "other source": "elsewhere"} {
		dst := newDest(t, "dst")
		os.MkdirAll(dst.Root, 0o755)
		putItems(t, dst.DB(), 1)
		if marker != "" {
			write(t, filepath.Join(dst.Root, moveMarker), []byte(filepath.Join(t.TempDir(), marker)))
		}
		// The pointer asks for this move, yet the database is not ours.
		if err := paths.SaveLocation(paths.Location{DataDir: src.Root, PendingMove: dst.Root}); err != nil {
			t.Fatal(err)
		}
		before := pointerBytes(t)
		err := Move(context.Background(), src, dst, nil)
		if err == nil || !strings.Contains(err.Error(), "already holds a database") {
			t.Fatalf("%s: err = %v", name, err)
		}
		if countItems(t, dst.DB()) != 1 {
			t.Fatalf("%s: target data was changed", name)
		}
		entries, _ := os.ReadDir(dst.Root)
		for _, e := range entries {
			if strings.Contains(e.Name(), "leftover") {
				t.Fatalf("%s: target database set aside: %v", name, entries)
			}
		}
		if !bytes.Equal(pointerBytes(t), before) || !exists(src.DB()) {
			t.Fatalf("%s: pointer or source changed", name)
		}
	}
}

func TestMoveRefusedWhileOldCopyExists(t *testing.T) {
	isolate(t)
	src := newSource(t)
	dst := newDest(t, "dst")
	old := filepath.Join(t.TempDir(), "old")
	write(t, filepath.Join(old, "awesome.db.moved"), []byte("x"))
	if err := paths.SaveLocation(paths.Location{DataDir: src.Root, PreviousDir: old, PendingMove: dst.Root}); err != nil {
		t.Fatal(err)
	}
	before := pointerBytes(t)
	err := Move(context.Background(), src, dst, nil)
	if err == nil || err.Error() != oldCopyMsg {
		t.Fatalf("err = %v", err)
	}
	if exists(dst.Root) || !bytes.Equal(pointerBytes(t), before) || !exists(src.DB()) {
		t.Fatal("move started")
	}
	// Once the old folder is gone the move runs.
	os.RemoveAll(old)
	if err := Move(context.Background(), src, dst, nil); err != nil {
		t.Fatal(err)
	}
	loc, _, _ := paths.LoadLocation()
	if loc.DataDir != dst.Root || loc.PreviousDir != src.Root {
		t.Fatalf("pointer %+v", loc)
	}
}

func TestRequestMoveRefusedWhileOldCopyExists(t *testing.T) {
	isolate(t)
	cur := t.TempDir()
	old := t.TempDir()
	if err := paths.SaveLocation(paths.Location{DataDir: cur, PreviousDir: old}); err != nil {
		t.Fatal(err)
	}
	before := pointerBytes(t)
	if err := RequestMove("X"); err == nil || err.Error() != oldCopyMsg {
		t.Fatalf("err = %v", err)
	}
	if !bytes.Equal(pointerBytes(t), before) {
		t.Fatal("pointer changed")
	}
	// A previous folder that no longer exists does not block.
	if err := paths.SaveLocation(paths.Location{DataDir: cur, PreviousDir: filepath.Join(old, "gone")}); err != nil {
		t.Fatal(err)
	}
	if err := RequestMove("X"); err != nil {
		t.Fatal(err)
	}
	if loc, _, _ := paths.LoadLocation(); loc.PendingMove != "X" {
		t.Fatalf("pointer %+v", loc)
	}
}

func TestTargetIgnoresPointerAndSetupLog(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "cfg")
	for _, f := range []string{"location.json", "location.json.tmp", "setup.log"} {
		write(t, filepath.Join(cfg, f), []byte("x"))
	}
	if got, err := Target(cfg); err != nil || got != cfg {
		t.Fatalf("got %q, %v want %q", got, err, cfg)
	}
	withDB := filepath.Join(root, "withdb")
	write(t, filepath.Join(withDB, "location.json"), []byte("x"))
	write(t, filepath.Join(withDB, "awesome.db"), []byte("x"))
	if got, err := Target(withDB); err != nil || got != withDB {
		t.Fatalf("got %q, %v want %q", got, err, withDB)
	}
	other := filepath.Join(root, "other")
	write(t, filepath.Join(other, "location.json"), []byte("x"))
	write(t, filepath.Join(other, "notes.txt"), []byte("x"))
	if got, err := Target(other); err != nil || got != filepath.Join(other, SubfolderName) {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestMkdirTrackedReturnsStatErrors(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	write(t, f, []byte("x"))
	// A path under a regular file cannot be created and must not be reported
	// as created folders.
	if created, err := mkdirTracked(filepath.Join(f, "sub")); err == nil || len(created) != 0 {
		t.Fatalf("created=%v err=%v", created, err)
	}
}
