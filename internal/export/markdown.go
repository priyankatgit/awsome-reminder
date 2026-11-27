// Package export renders the item view to a markdown file.
package export

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"awesome-reminder/internal/core"
	"awesome-reminder/internal/model"
)

var weekdays = map[string]string{
	"MO": "monday", "TU": "tuesday", "WE": "wednesday", "TH": "thursday",
	"FR": "friday", "SA": "saturday", "SU": "sunday",
}

func rruleLabel(rule string) string {
	switch rule {
	case "FREQ=DAILY":
		return "every day"
	case "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR":
		return "every weekday"
	}
	if code, ok := strings.CutPrefix(rule, "FREQ=WEEKLY;BYDAY="); ok {
		if n, ok := weekdays[code]; ok {
			return "every " + n
		}
	}
	return rule
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func openLine(it model.Item, loc *time.Location) string {
	s := "- [ ]"
	switch it.Priority {
	case model.PriorityHigh:
		s += " (!high)"
	case model.PriorityMedium:
		s += " (!med)"
	case model.PriorityLow:
		s += " (!low)"
	}
	s += " " + oneLine(it.Text)
	var meta []string
	if it.DueAt != nil {
		meta = append(meta, "due "+it.DueAt.In(loc).Format("2006-01-02 15:04"))
	}
	if it.RRule != "" {
		meta = append(meta, rruleLabel(it.RRule))
	}
	if len(meta) > 0 {
		s += " (" + strings.Join(meta, ", ") + ")"
	}
	return s
}

// Markdown renders v as a plain-text todo list, with times shown in loc.
func Markdown(v core.View, loc *time.Location) []byte {
	var b strings.Builder
	b.WriteString("# Awesome Reminder\n")
	for _, g := range v.Tags {
		if len(g.Items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## #%s\n", g.Tag)
		for _, it := range g.Items {
			b.WriteString(openLine(it, loc) + "\n")
		}
	}
	if len(v.Done) > 0 {
		b.WriteString("\n## Done\n")
		for _, it := range v.Done {
			b.WriteString("- [x] " + oneLine(it.Text))
			for _, t := range it.Tags {
				b.WriteString(" #" + t)
			}
			b.WriteString("\n")
		}
	}
	return []byte(b.String())
}

// WriteAtomic writes data to path via a temp file in the same directory and a
// rename, creating the parent directory if needed.
func WriteAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".export-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Exporter writes the markdown export after a quiet period.
type Exporter struct {
	path     func() string
	view     func() (core.View, error)
	loc      *time.Location
	debounce time.Duration

	mu      sync.Mutex
	timer   *time.Timer
	pending bool // a Trigger has not been exported yet
	closed  bool

	runMu sync.Mutex // serializes exports, so Close can wait for one in flight
}

func NewExporter(path func() string, view func() (core.View, error), loc *time.Location, debounce time.Duration) *Exporter {
	return &Exporter{path: path, view: view, loc: loc, debounce: debounce}
}

// Trigger schedules an export; repeated calls within the debounce window coalesce.
func (e *Exporter) Trigger() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	e.pending = true
	if e.timer == nil {
		e.timer = time.AfterFunc(e.debounce, e.run)
		return
	}
	e.timer.Reset(e.debounce)
}

// Close stops the timer and, if an export is still pending, writes it before
// returning (so a change made just before Quit is not lost); it also waits
// for an export already running. Later triggers are ignored.
func (e *Exporter) Close() {
	e.mu.Lock()
	e.closed = true
	if e.timer != nil {
		e.timer.Stop()
	}
	pending := e.pending
	e.pending = false
	e.mu.Unlock()

	e.runMu.Lock()
	defer e.runMu.Unlock()
	if pending {
		e.export()
	}
}

// run is the timer callback: it exports unless Close took the pending
// export over.
func (e *Exporter) run() {
	e.runMu.Lock()
	defer e.runMu.Unlock()
	e.mu.Lock()
	pending := e.pending
	e.pending = false
	e.mu.Unlock()
	if pending {
		e.export()
	}
}

func (e *Exporter) export() {
	p := e.path()
	if p == "" {
		return
	}
	v, err := e.view()
	if err != nil {
		log.Printf("export: view: %v", err)
		return
	}
	if err := WriteAtomic(p, Markdown(v, e.loc)); err != nil {
		log.Printf("export: write %s: %v", p, err)
	}
}
