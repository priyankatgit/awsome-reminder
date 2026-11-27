package app

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"

	"awesome-reminder/internal/paths"
)

// fatalShower shows a blocking error dialog with title and text.
type fatalShower func(title, text string) error

// ReportFatal tells the user that Run failed (a corrupt or newer database, an
// unwritable data folder, ...): a tray app that silently doesn't start is
// indistinguishable from one that is merely hidden. It logs err, prints it to
// stderr and shows a native message box with the error and the data folder
// (MessageBoxW on Windows, osascript on macOS, zenity on Linux when
// installed). The caller exits afterwards.
func ReportFatal(err error) {
	dir, derr := paths.Dir()
	if derr != nil {
		dir = ""
	}
	reportFatal(err, dir, showFatal, os.Stderr)
}

// reportFatal is ReportFatal with its dependencies injected. show may be nil
// (no dialog available).
func reportFatal(err error, dataDir string, show fatalShower, stderr io.Writer) {
	log.Printf("fatal: %v", err)
	title := appName + " could not start"
	text := fatalText(err, dataDir)
	fmt.Fprintln(stderr, text)
	if show == nil {
		return
	}
	if serr := show(title, text); serr != nil {
		fmt.Fprintf(stderr, "(could not show the error dialog: %v)\n", serr)
	}
}

func fatalText(err error, dataDir string) string {
	if dataDir == "" {
		dataDir = "(unknown)"
	}
	return fmt.Sprintf("%s could not start:\n\n%v\n\nData folder: %s\nDetails are in awesome.log in that folder.", appName, err, dataDir)
}

// appleScriptString quotes s as an AppleScript string literal.
func appleScriptString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// osascriptCommand is the macOS dialog command.
func osascriptCommand(title, text string) (string, []string) {
	return "osascript", []string{"-e", "display alert " + appleScriptString(title) + " message " + appleScriptString(text) + " as critical"}
}

// zenityCommand is the Linux dialog command (plain text, not Pango markup).
func zenityCommand(title, text string) (string, []string) {
	return "zenity", []string{"--error", "--no-markup", "--title=" + title, "--text=" + text}
}

// runDialog runs a dialog command if it is installed and waits for it.
func runDialog(name string, args []string) error {
	p, err := exec.LookPath(name)
	if err != nil {
		return err
	}
	return exec.Command(p, args...).Run()
}
