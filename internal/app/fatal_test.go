package app

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestReportFatalShowsErrorAndDataDir(t *testing.T) {
	var title, text string
	var out bytes.Buffer
	show := func(ti, te string) error { title, text = ti, te; return nil }
	reportFatal(errors.New("open store: database is from a newer version (schema 9)"), `C:\Users\me\AppData\Roaming\awesome-reminder`, show, &out)
	if !strings.Contains(title, "Awesome Reminder") {
		t.Errorf("title %q", title)
	}
	for _, want := range []string{"could not start", "schema 9", `C:\Users\me\AppData\Roaming\awesome-reminder`, "awesome.log"} {
		if !strings.Contains(text, want) {
			t.Errorf("text %q lacks %q", text, want)
		}
	}
	if !strings.Contains(out.String(), "schema 9") {
		t.Errorf("stderr %q", out.String())
	}
}

func TestReportFatalFallsBackToStderr(t *testing.T) {
	var out bytes.Buffer
	reportFatal(errors.New("boom"), "/home/me/.config/awesome-reminder", func(string, string) error {
		return errors.New("no display")
	}, &out)
	if s := out.String(); !strings.Contains(s, "boom") || !strings.Contains(s, "/home/me/.config/awesome-reminder") || !strings.Contains(s, "no display") {
		t.Fatalf("stderr %q", s)
	}
	out.Reset()
	reportFatal(errors.New("boom"), "", nil, &out)
	if s := out.String(); !strings.Contains(s, "boom") || !strings.Contains(s, "unknown") {
		t.Fatalf("no shower: stderr %q", s)
	}
}

func TestAppleScriptString(t *testing.T) {
	// A backslash must be doubled, or a path such as C:\new reads as an
	// escape sequence; a trailing one would swallow the closing quote.
	got := appleScriptString(`a "b" \c` + "\nd" + `\`)
	if want := `"a \"b\" \\c` + "\n" + `d\\"`; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestFatalCommands(t *testing.T) {
	name, args := osascriptCommand("T", `x "y"`)
	if name != "osascript" || len(args) != 2 || args[0] != "-e" ||
		args[1] != `display alert "T" message "x \"y\"" as critical` {
		t.Fatalf("osascript %s %q", name, args)
	}
	name, args = zenityCommand("T", "a <b> & c")
	if name != "zenity" || strings.Join(args, "|") != "--error|--no-markup|--title=T|--text=a <b> & c" {
		t.Fatalf("zenity %s %q", name, args)
	}
}
