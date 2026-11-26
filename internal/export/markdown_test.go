package export

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"awesome-reminder/internal/core"
	"awesome-reminder/internal/model"
)

func due(s string) *time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.UTC)
	if err != nil {
		panic(err)
	}
	return &t
}

func goldenView() core.View {
	read := model.Item{Text: "read RFC", Kind: model.KindTodo}
	deploy := model.Item{Text: "check\ndeploy", Kind: model.KindReminder, DueAt: due("2026-11-26 17:00")}
	standup := model.Item{Text: "standup", Kind: model.KindReminder, DueAt: due("2026-11-30 10:00"), RRule: "FREQ=WEEKLY;BYDAY=MO"}
	deploy.Tags = []string{"backend"}
	weekday := model.Item{Text: "weekday sync", DueAt: due("2026-12-01 09:00"), RRule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", Tags: []string{"backend"}}
	odd := model.Item{Text: "odd one", DueAt: due("2026-12-02 09:00"), RRule: "FREQ=MONTHLY", Tags: []string{"backend"}}
	return core.View{
		Tags: []core.TagGroup{
			{Tag: "inbox", Items: []model.Item{read, deploy, standup}},
			{Tag: "backend", Items: []model.Item{deploy, weekday, odd}},
			{Tag: "empty", Items: nil},
		},
		Done: []model.Item{
			{Text: "fix flaky test", Tags: []string{"backend"}},
			{Text: "ship it", Tags: []string{"backend", "infra"}},
			{Text: "plain done"},
		},
	}
}

func TestMarkdownGolden(t *testing.T) {
	got := Markdown(goldenView(), time.UTC)
	want, err := os.ReadFile("testdata/golden.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestMarkdownEmptyAndLoc(t *testing.T) {
	if got := string(Markdown(core.View{}, time.UTC)); got != "# Awesome Reminder\n" {
		t.Fatalf("empty: %q", got)
	}
	loc := time.FixedZone("x", 2*3600)
	v := core.View{Tags: []core.TagGroup{{Tag: "a", Items: []model.Item{{Text: "t", DueAt: due("2026-11-26 17:00")}}}}}
	want := "# Awesome Reminder\n\n## #a\n- [ ] t (due 2026-11-26 19:00)\n"
	if got := string(Markdown(v, loc)); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestWriteAtomicReplaces(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "dir", "todo.md")
	if err := WriteAtomic(p, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(p, []byte("two")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "two" {
		t.Fatalf("got %q", b)
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	if len(ents) != 1 {
		t.Fatalf("leftover files: %v", ents)
	}
}

func TestExporterDebounces(t *testing.T) {
	p := filepath.Join(t.TempDir(), "todo.md")
	var views atomic.Int32
	e := NewExporter(func() string { return p }, func() (core.View, error) {
		views.Add(1)
		return goldenView(), nil
	}, time.UTC, 30*time.Millisecond)
	defer e.Close()
	e.Trigger()
	e.Trigger()
	e.Trigger()
	time.Sleep(200 * time.Millisecond)
	if n := views.Load(); n != 1 {
		t.Fatalf("view calls = %d, want 1", n)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
}

func TestExporterDisabledPath(t *testing.T) {
	dir := t.TempDir()
	var views atomic.Int32
	e := NewExporter(func() string { return "" }, func() (core.View, error) {
		views.Add(1)
		return core.View{}, nil
	}, time.UTC, 20*time.Millisecond)
	defer e.Close()
	e.Trigger()
	time.Sleep(120 * time.Millisecond)
	if views.Load() != 0 {
		t.Fatal("view called with empty path")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatal("unexpected files")
	}
}

func TestExporterCloseCancels(t *testing.T) {
	p := filepath.Join(t.TempDir(), "todo.md")
	e := NewExporter(func() string { return p }, func() (core.View, error) { return core.View{}, nil }, time.UTC, 50*time.Millisecond)
	e.Trigger()
	e.Close()
	time.Sleep(150 * time.Millisecond)
	if _, err := os.Stat(p); err == nil {
		t.Fatal("wrote after Close")
	}
}
