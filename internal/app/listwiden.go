package app

import (
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// widenWindow is the list window as listWidener drives it (DIPs).
type widenWindow interface {
	Bounds() application.Rect
	WorkArea() application.Rect // work area of the screen it is on
	SetBounds(application.Rect)
}

// listWidener widens the list window while the detail panel is open and puts
// it back afterwards. It restores only what it changed: when the window was
// already wide enough, Restore does nothing.
type listWidener struct {
	mu      sync.Mutex
	widened bool
	origW   int // width before widening
	shiftX  int // how far Widen moved the left edge (<= 0)
}

// Widen grows w to minWidth, capped to the work area. If that would push the
// right edge off screen the window moves left.
func (l *listWidener) Widen(w widenWindow, minWidth int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.widened {
		return
	}
	b, wa := w.Bounds(), w.WorkArea()
	target := minWidth
	if wa.Width > 0 {
		target = min(target, wa.Width)
	}
	if target <= b.Width {
		return
	}
	nb := b
	nb.Width = target
	if wa.Width > 0 && nb.X+target > wa.X+wa.Width {
		nb.X = max(wa.X, wa.X+wa.Width-target)
	}
	w.SetBounds(nb)
	l.widened, l.origW, l.shiftX = true, b.Width, nb.X-b.X
}

// Restore undoes Widen, if it widened anything.
func (l *listWidener) Restore(w widenWindow) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.widened {
		return
	}
	l.widened = false
	b := w.Bounds()
	b.Width = l.origW
	b.X -= l.shiftX
	w.SetBounds(b)
}

// wailsWiden adapts a Wails window to widenWindow.
type wailsWiden struct{ win *application.WebviewWindow }

func (w wailsWiden) Bounds() application.Rect     { return w.win.Bounds() }
func (w wailsWiden) SetBounds(r application.Rect) { w.win.SetBounds(r) }
func (w wailsWiden) WorkArea() application.Rect {
	if s, err := w.win.GetScreen(); err == nil && s != nil {
		return s.WorkArea
	}
	return application.Rect{}
}

// currentList returns the open list window, or nil.
func (w *wailsWindows) currentList() *application.WebviewWindow {
	w.list.mu.Lock()
	defer w.list.mu.Unlock()
	return w.list.cur
}

// WidenList widens the open list window to minWidth, capped to the screen.
func (w *wailsWindows) WidenList(minWidth int) {
	if win := w.currentList(); win != nil {
		w.widen.Widen(wailsWiden{win}, minWidth)
	}
}

// RestoreListWidth restores the width WidenList changed.
func (w *wailsWindows) RestoreListWidth() {
	if win := w.currentList(); win != nil {
		w.widen.Restore(wailsWiden{win})
	}
}

// Reset forgets a widening whose window has closed.
func (l *listWidener) Reset() {
	l.mu.Lock()
	l.widened = false
	l.mu.Unlock()
}
