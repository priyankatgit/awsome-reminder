package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"awesome-reminder/internal/datamove"
	"awesome-reminder/internal/paths"
)

// Setup events emitted to the setup page.
const (
	EventSetupProgress = "setup:progress" // datamove.Progress
	EventSetupDone     = "setup:done"     // SetupDone
)

// SetupDone ends a move that ran in the background.
type SetupDone struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

// Errors the page turns into a confirmation step.
const (
	errConfirmAdopt = "confirm-adopt"
	errConfirmFresh = "confirm-fresh"
	errMoveRunning  = "A move is already running."
)

// FolderOption is one place the data can live.
type FolderOption struct {
	Kind     string `json:"kind"` // "standard", "beside" or "other"
	Label    string `json:"label"`
	Path     string `json:"path"`
	Free     uint64 `json:"free"`
	FreeText string `json:"freeText"`
}

// SetupInfo is what the setup page shows.
type SetupInfo struct {
	State       string         `json:"state"`
	Root        string         `json:"root"`
	Reason      string         `json:"reason"`
	ExistingDir string         `json:"existingDir"`
	Options     []FolderOption `json:"options"`
	MoveFrom    string         `json:"moveFrom"`
	MoveTo      string         `json:"moveTo"`
	NeedText    string         `json:"needText"`
}

// SetupService is the bound API of the setup window.
type SetupService struct {
	resolve    func() paths.Resolution
	exe        func() (string, error)
	move       func(ctx context.Context, from, to paths.Layout, onProgress func(datamove.Progress)) error
	adopt      func(target string, prev *paths.Location) error
	startFresh func(target string, prev *paths.Location, clearKeychain bool) error
	keep       func() error
	quit       func()
	emit       func(string, any)

	res paths.Resolution

	mu     sync.Mutex
	cancel context.CancelFunc // non-nil while a move runs
}

// newSetupService wires the real datamove functions.
func newSetupService(res paths.Resolution, quit func(), emit func(string, any)) *SetupService {
	return &SetupService{
		resolve:    paths.Resolve,
		exe:        os.Executable,
		move:       datamove.Move,
		adopt:      datamove.Adopt,
		startFresh: datamove.StartFresh,
		keep:       datamove.KeepStandard,
		quit:       quit,
		emit:       emit,
		res:        res,
	}
}

func (s *SetupService) state() paths.State { return s.res.State }

// exeDataDir is the "next to the app" folder, or "" when it would not suit:
// not writable, under Program Files, or inside a macOS app bundle.
func (s *SetupService) exeDataDir() string {
	exe, err := s.exe()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	if strings.Contains(filepath.ToSlash(dir)+"/", ".app/Contents/") {
		return ""
	}
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
		if pf := os.Getenv(env); pf != "" && (paths.SameDir(dir, pf) || paths.Within(dir, pf)) {
			return ""
		}
	}
	f, err := os.CreateTemp(dir, ".awesome-probe-*")
	if err != nil {
		return ""
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return filepath.Join(dir, "Data")
}

func freeOf(dir string) (uint64, string) {
	free, err := paths.FreeSpace(dir)
	if err != nil {
		return 0, ""
	}
	return free, datamove.FormatBytes(int64(free)) + " free"
}

// fromLayout is the data folder being left, nil when there is none.
func (s *SetupService) fromLayout() *paths.Layout {
	var root string
	switch s.state() {
	case paths.StateFirstRunExisting:
		root, _ = paths.StandardRoot()
	case paths.StateMovePending:
		root = s.res.Root
	default:
		return nil
	}
	if root == "" {
		return nil
	}
	l, err := paths.NewLayout(root)
	if err != nil {
		return nil
	}
	return &l
}

// Info describes the situation and the places to offer.
func (s *SetupService) Info() SetupInfo {
	info := SetupInfo{State: string(s.state()), Root: s.res.Root, Reason: s.res.Reason}
	std, _ := paths.StandardRoot()
	label := "Standard location"
	if s.state() == paths.StateFirstRunExisting {
		label = "Keep it where it is"
		info.ExistingDir = std
	}
	if s.state() == paths.StateMovePending {
		info.MoveFrom = s.res.Root
		info.MoveTo = s.res.Loc.PendingMove
	}
	if from := s.fromLayout(); from != nil {
		if sz, err := datamove.Measure(*from); err == nil {
			info.NeedText = datamove.FormatBytes(sz.Total())
		}
	}
	free, text := freeOf(std)
	info.Options = append(info.Options, FolderOption{Kind: "standard", Label: label, Path: std, Free: free, FreeText: text})
	if d := s.exeDataDir(); d != "" {
		free, text := freeOf(d)
		info.Options = append(info.Options, FolderOption{Kind: "beside", Label: "Next to the app", Path: d, Free: free, FreeText: text})
	}
	return info
}

// Check inspects a folder the user picked.
func (s *SetupService) Check(picked string) datamove.Check {
	return datamove.CheckTarget(picked, s.fromLayout())
}

// resolveTarget turns a choice into the checked data folder.
func (s *SetupService) resolveTarget(kind, path string) (datamove.Check, error) {
	switch kind {
	case "standard":
		std, err := paths.StandardRoot()
		if err != nil {
			return datamove.Check{}, err
		}
		_, statErr := os.Stat(filepath.Join(std, paths.DBFile))
		return datamove.Check{Path: std, HasData: statErr == nil}, nil
	case "beside":
		d := s.exeDataDir()
		if d == "" {
			return datamove.Check{}, errors.New("That place isn't available.")
		}
		path = d
	case "other":
	default:
		return datamove.Check{}, errors.New("Unknown folder choice.")
	}
	c := s.Check(path)
	if c.Err != "" {
		return c, errors.New(c.Err)
	}
	if c.Same {
		return c, errors.New("That is already the data folder.")
	}
	return c, nil
}

func (s *SetupService) finish() {
	RequestRestart()
	s.quit()
}

// Choose applies the user's choice. Errors "confirm-adopt" and "confirm-fresh"
// ask the page to confirm and call again with confirm set. A move runs in the
// background and reports through EventSetupProgress and EventSetupDone.
func (s *SetupService) Choose(kind, path string, confirm bool) error {
	if s.moving() {
		return errors.New(errMoveRunning)
	}
	prev := &s.res.Loc
	if kind == "standard" {
		switch s.state() {
		case paths.StateFirstRunFresh:
			std, err := paths.StandardRoot()
			if err != nil {
				return err
			}
			if err := s.startFresh(std, nil, false); err != nil {
				return err
			}
			s.finish()
			return nil
		case paths.StateFirstRunExisting:
			if err := s.keep(); err != nil {
				return err
			}
			s.finish()
			return nil
		}
	}
	c, err := s.resolveTarget(kind, path)
	if err != nil {
		return err
	}
	switch s.state() {
	case paths.StateFirstRunFresh:
		if c.HasData {
			if !confirm {
				return errors.New(errConfirmAdopt)
			}
			err = s.adopt(c.Path, nil)
		} else {
			err = s.startFresh(c.Path, nil, false)
		}
	case paths.StateFirstRunExisting:
		if !c.HasData {
			return s.startMove(*s.fromLayout(), c.Path)
		}
		if !confirm {
			return errors.New(errConfirmAdopt)
		}
		err = s.adopt(c.Path, prev)
	case paths.StateUnavailable:
		switch {
		case c.HasData && !confirm:
			return errors.New(errConfirmAdopt)
		case c.HasData:
			err = s.adopt(c.Path, prev)
		case !confirm:
			return errors.New(errConfirmFresh)
		default:
			err = s.startFresh(c.Path, prev, true)
		}
	default:
		return errors.New("Nothing to choose in this state.")
	}
	if err != nil {
		return err
	}
	s.finish()
	return nil
}

func (s *SetupService) moving() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancel != nil
}

// RunPendingMove runs the move the pointer asks for.
func (s *SetupService) RunPendingMove() error {
	if s.state() != paths.StateMovePending {
		return errors.New("No move is waiting.")
	}
	from, err := paths.NewLayout(s.res.Root)
	if err != nil {
		return err
	}
	return s.startMove(from, s.res.Loc.PendingMove)
}

// startMove copies from into the target folder in the background.
func (s *SetupService) startMove(from paths.Layout, target string) error {
	to, err := paths.NewLayout(target)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		cancel()
		return errors.New(errMoveRunning)
	}
	s.cancel = cancel
	s.mu.Unlock()
	go func() {
		err := s.move(ctx, from, to, func(p datamove.Progress) { s.emit(EventSetupProgress, p) })
		cancel()
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
		if err != nil {
			s.emit(EventSetupDone, SetupDone{Error: err.Error()})
			return
		}
		s.emit(EventSetupDone, SetupDone{OK: true})
		s.finish()
	}()
	return nil
}

// CancelMove stops a running move.
func (s *SetupService) CancelMove() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

// AbandonMove drops a pending move that cannot run and goes back to the
// current data folder.
func (s *SetupService) AbandonMove() error {
	loc, found, err := paths.LoadLocation()
	if err != nil {
		return err
	}
	if !found {
		return errors.New("No move is waiting.")
	}
	loc.PendingMove = ""
	if err := paths.SaveLocation(loc); err != nil {
		return err
	}
	s.finish()
	return nil
}

// Retry looks again; a folder that is back starts the app.
func (s *SetupService) Retry() SetupInfo {
	s.res = s.resolve()
	if s.res.State == paths.StateReady {
		s.finish()
	}
	return s.Info()
}

// Quit closes setup without starting the app.
func (s *SetupService) Quit() { s.quit() }
