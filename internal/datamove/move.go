package datamove

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"awesome-reminder/internal/paths"
	"awesome-reminder/internal/store"
)

// Progress reports a move. Step is "database", "attachments", "voice" or
// "checking".
type Progress struct {
	Step  string `json:"step"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
}

const progressEvery = 64 << 10

// afterCopy runs after each copy step; tests use it to inject a failure.
var afterCopy = func(step string) error { return nil }

type filePair struct{ src, dst string }

type mover struct {
	ctx        context.Context
	onProgress func(Progress)
	total      int64
	done       int64
	lastEmit   int64
	step       string

	files []string // created by this call, in order
	dirs  []string // created by this call, outermost first
	pairs []filePair
}

func (m *mover) emit(step string) {
	m.step = step
	m.lastEmit = m.done
	if m.onProgress != nil {
		m.onProgress(Progress{Step: step, Done: m.done, Total: m.total})
	}
}

func (m *mover) advance(n int64) {
	m.done += n
	if m.done-m.lastEmit >= progressEvery {
		m.emit(m.step)
	}
}

// rollback removes only what this call created.
func (m *mover) rollback() {
	for i := len(m.files) - 1; i >= 0; i-- {
		_ = os.Remove(m.files[i])
	}
	for i := len(m.dirs) - 1; i >= 0; i-- {
		_ = os.Remove(m.dirs[i]) // fails, harmlessly, when something else lives there
	}
}

func (m *mover) mkdir(dir string) error {
	created, err := mkdirTracked(dir)
	m.dirs = append(m.dirs, created...)
	return err
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// copyFile copies src to dst through dst+".tmp" and a rename.
func (m *mover) copyFile(src, dst string) error {
	if err := m.ctx.Err(); err != nil {
		return err
	}
	if err := m.mkdir(filepath.Dir(dst)); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	m.files = append(m.files, tmp)
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	buf := make([]byte, 64<<10)
	for {
		if err := m.ctx.Err(); err != nil {
			out.Close()
			return err
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				return werr
			}
			m.advance(int64(n))
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			return rerr
		}
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if !exists(dst) {
		m.files = append(m.files, dst)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	m.pairs = append(m.pairs, filePair{src, dst})
	return nil
}

// copyTree copies every regular file under srcDir to dstDir. A missing srcDir
// is not an error.
func (m *mover) copyTree(srcDir, dstDir string) error {
	if _, err := os.Stat(srcDir); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(srcDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := m.ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(dstDir, rel)
		switch {
		case d.IsDir():
			return m.mkdir(dst)
		case d.Type().IsRegular():
			return m.copyFile(p, dst)
		}
		return nil
	})
}

func sqliteDSN(path string) string {
	return "file:" + strings.ReplaceAll(url.PathEscape(strings.ReplaceAll(path, "\\", "/")), "%2F", "/") +
		"?_pragma=busy_timeout(5000)"
}

func openRaw(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func integrityOK(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA integrity_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var msgs []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return err
		}
		msgs = append(msgs, s)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(msgs) != 1 || msgs[0] != "ok" {
		return fmt.Errorf("database integrity check failed: %s", strings.Join(msgs, "; "))
	}
	return nil
}

func tableNames(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

func countRows(db *sql.DB, table string) (int64, error) {
	var n int64
	err := db.QueryRow(`SELECT COUNT(*) FROM "` + strings.ReplaceAll(table, `"`, `""`) + `"`).Scan(&n)
	return n, err
}

// ownLeftover reports whether to's database is the unfinished result of an
// earlier move from `from`: the pointer still asks for this move and the
// source database has not been renamed.
func ownLeftover(from, to paths.Layout) bool {
	loc, found, err := paths.LoadLocation()
	return err == nil && found && loc.PendingMove != "" && paths.SameDir(loc.PendingMove, to.Root) && exists(from.DB())
}

// Move copies the data folder `from` into `to`, checks the copy and then
// switches the pointer. Before the pointer is saved a failure removes what
// this call created in `to` and leaves the pointer and the source alone.
func Move(ctx context.Context, from, to paths.Layout, onProgress func(Progress)) (err error) {
	if paths.SameDir(from.Root, to.Root) || paths.Within(to.Root, from.Root) || paths.Within(from.Root, to.Root) ||
		paths.Within(to.Root, from.Voice()) {
		return errors.New(outsideMsg)
	}
	if !exists(from.DB()) {
		return errors.New("database: the current data folder has no database")
	}
	if exists(to.DB()) {
		if !ownLeftover(from, to) {
			return errors.New("database: the new folder already holds a database")
		}
		// A previous attempt died between making the copy live and saving
		// the pointer: the copy is ours and the source is still in place.
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(to.DB() + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("database: %w", err)
			}
		}
	}
	sizes, err := Measure(from)
	if err != nil {
		return fmt.Errorf("measure: %w", err)
	}
	sameVoice := paths.SameDir(from.Voice(), to.Voice())
	if sameVoice {
		sizes.Voice = 0
	}
	m := &mover{ctx: ctx, onProgress: onProgress, total: sizes.Total()}

	var src *sql.DB
	closeSrc := func() {
		if src != nil {
			src.Close()
			src = nil
		}
	}
	defer func() {
		closeSrc()
		if err != nil {
			m.rollback()
		}
	}()
	fail := func(step string, e error) error { return fmt.Errorf("%s: %w", step, e) }

	if e := m.mkdir(to.Root); e != nil {
		return fail("database", e)
	}

	// 1. Database snapshot.
	m.emit("database")
	tmp := to.DB() + ".tmp"
	for _, p := range []string{tmp, tmp + "-wal", tmp + "-shm"} {
		_ = os.Remove(p)
		m.files = append(m.files, p)
	}
	if src, err = openRaw(from.DB()); err != nil {
		return fail("database", err)
	}
	if _, err = src.ExecContext(ctx, `VACUUM INTO '`+strings.ReplaceAll(tmp, `'`, `''`)+`'`); err != nil {
		return fail("database", err)
	}
	m.done += sizes.DB
	if err = afterCopy("database"); err != nil {
		return fail("database", err)
	}

	// 2. Attachments and sync key.
	m.emit("attachments")
	if err = m.copyTree(from.Attachments(), to.Attachments()); err != nil {
		return fail("attachments", err)
	}
	if exists(from.SyncKey()) {
		if err = m.copyFile(from.SyncKey(), to.SyncKey()); err != nil {
			return fail("attachments", err)
		}
	}
	if err = afterCopy("attachments"); err != nil {
		return fail("attachments", err)
	}

	// 3. Voice engine and model.
	m.emit("voice")
	if !sameVoice {
		if err = m.copyTree(from.Voice(), to.Voice()); err != nil {
			return fail("voice", err)
		}
	}
	if err = afterCopy("voice"); err != nil {
		return fail("voice", err)
	}

	// 4. Check the copy.
	m.done = m.total
	m.emit("checking")
	if err = ctx.Err(); err != nil {
		return fail("checking", err)
	}
	if err = checkCopy(src, tmp, m.pairs, from.SyncKey(), to.SyncKey()); err != nil {
		return fail("checking", err)
	}
	closeSrc()

	// 5. Make the copy live, then 6. switch the pointer.
	if err = ctx.Err(); err != nil {
		return fail("switch", err)
	}
	m.files = append(m.files, to.DB())
	if err = os.Rename(tmp, to.DB()); err != nil {
		return fail("switch", err)
	}
	if err = paths.SaveLocation(paths.Location{DataDir: to.Root, PreviousDir: from.Root}); err != nil {
		return fail("switch", err)
	}

	// 7. Tidy the old copy. The switch has happened, so this is best effort.
	if !sameVoice {
		_ = os.RemoveAll(from.Voice())
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if exists(from.DB() + suffix) {
			_ = os.Rename(from.DB()+suffix, from.DB()+suffix+".moved")
		}
	}
	return nil
}

// checkCopy verifies the copied database, files and sync key.
func checkCopy(src *sql.DB, tmp string, pairs []filePair, srcKey, dstKey string) error {
	cp, err := openRaw(tmp)
	if err != nil {
		return err
	}
	if err := integrityOK(cp); err != nil {
		cp.Close()
		return err
	}
	tables, err := tableNames(src)
	if err != nil {
		cp.Close()
		return err
	}
	for _, t := range tables {
		a, err := countRows(src, t)
		if err != nil {
			cp.Close()
			return err
		}
		b, err := countRows(cp, t)
		if err != nil {
			cp.Close()
			return fmt.Errorf("table %s: %w", t, err)
		}
		if a != b {
			cp.Close()
			return fmt.Errorf("table %s has %d rows, the copy has %d", t, a, b)
		}
	}
	if err := cp.Close(); err != nil {
		return err
	}
	s, err := store.Open(tmp)
	if err != nil {
		return fmt.Errorf("the copy doesn't open: %w", err)
	}
	if err := s.Close(); err != nil {
		return err
	}
	for _, p := range pairs {
		a, err := os.Stat(p.src)
		if err != nil {
			return err
		}
		b, err := os.Stat(p.dst)
		if err != nil {
			return err
		}
		if a.Size() != b.Size() {
			return fmt.Errorf("%s was copied incompletely", filepath.Base(p.dst))
		}
	}
	if exists(srcKey) {
		a, err := os.ReadFile(srcKey)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(dstKey)
		if err != nil {
			return err
		}
		if !bytes.Equal(a, b) {
			return errors.New("sync.key was copied incorrectly")
		}
	}
	return nil
}
