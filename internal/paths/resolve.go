package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// State says what the app should do at startup with the data folder.
type State string

const (
	StateReady            State = "ready"
	StateFirstRunExisting State = "existing"
	StateFirstRunFresh    State = "fresh"
	StateUnavailable      State = "unavailable"
	StateMovePending      State = "move"
)

// Resolution is the result of Resolve. Root is the root in use (Ready,
// Unavailable, MovePending) or the standard root (first-run states).
type Resolution struct {
	State  State
	Loc    Location
	Root   string
	Reason string
}

// Resolve inspects the pointer and the disk. It never writes anything.
func Resolve() Resolution {
	std, err := StandardRoot()
	if err != nil {
		return Resolution{State: StateUnavailable, Reason: fmt.Sprintf("Can't find the config folder: %v", err)}
	}
	loc, found, err := LoadLocation()
	if err != nil {
		return Resolution{State: StateUnavailable, Root: std, Reason: fmt.Sprintf("Can't read location.json: %v", err)}
	}
	if !found {
		if fileExists(filepath.Join(std, DBFile)) {
			return Resolution{State: StateFirstRunExisting, Root: std}
		}
		return Resolution{State: StateFirstRunFresh, Root: std}
	}
	root := loc.DataDir
	fail := func(reason string) Resolution {
		return Resolution{State: StateUnavailable, Loc: loc, Root: root, Reason: reason}
	}
	if !filepath.IsAbs(root) {
		return fail("The saved data folder path is not absolute.")
	}
	st, err := os.Stat(root)
	if err != nil {
		return fail("Folder not found: " + root)
	}
	if !st.IsDir() {
		return fail("Not a folder: " + root)
	}
	if !fileExists(filepath.Join(root, DBFile)) {
		return fail("awesome.db is missing from " + root)
	}
	if loc.PendingMove != "" {
		return Resolution{State: StateMovePending, Loc: loc, Root: root}
	}
	return Resolution{State: StateReady, Loc: loc, Root: root}
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
