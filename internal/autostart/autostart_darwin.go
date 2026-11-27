//go:build darwin

package autostart

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// baseDir overrides the LaunchAgents directory (tests).
var baseDir string

const label = "dev.awesome-reminder.app"

func entryPath() string {
	dir := baseDir
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, "Library", "LaunchAgents")
	}
	return filepath.Join(dir, label+".plist")
}

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func Set(enabled bool, exePath string) error {
	p := entryPath()
	if !enabled {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("autostart: remove: %w", err)
		}
		return nil
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + esc(exePath) + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	if err := os.WriteFile(p, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("autostart: write: %w", err)
	}
	return nil
}

// Target returns ProgramArguments[0] of the launch agent; "" if there is no
// entry.
func Target() (string, error) {
	b, err := os.ReadFile(entryPath())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("autostart: read: %w", err)
	}
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false // the DOCTYPE is not fetched
	var key string   // text of the last <key>
	var inArgs bool  // inside the ProgramArguments <array>
	var elem string  // element whose text is being read
	for {
		tok, err := d.Token()
		if err != nil {
			return "", nil // no ProgramArguments: treat as a foreign entry
		}
		switch t := tok.(type) {
		case xml.StartElement:
			elem = t.Name.Local
			if elem == "array" && key == "ProgramArguments" {
				inArgs = true
			}
		case xml.EndElement:
			if t.Name.Local == "array" && inArgs {
				return "", nil
			}
			elem = ""
		case xml.CharData:
			switch {
			case elem == "key":
				key = string(t)
			case elem == "string" && inArgs:
				return string(t), nil
			}
		}
	}
}

func Enabled() (bool, error) {
	_, err := os.Stat(entryPath())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
