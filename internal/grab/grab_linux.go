//go:build linux

package grab

import (
	"bytes"
	"context"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// On Linux the grab reads the primary selection (the text the user has
// highlighted) with wl-paste on Wayland, or xclip or xsel on X11. Nothing
// is injected and the clipboard is never written, so there is nothing to
// restore. When the primary selection is empty, or no tool is installed,
// the clipboard's text is the fallback. The process and title come from
// xprop (_NET_ACTIVE_WINDOW), which does not see native Wayland windows.

// cmdTimeout bounds each external command.
const cmdTimeout = 150 * time.Millisecond

// Grab returns the primary selection, and the active window's WM_CLASS and
// title. keys are ignored: nothing is injected. It never panics.
func Grab(ctx context.Context, keys []Key) (res Result) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("grab: panic: %v", r)
		}
	}()
	res.Process, res.Title = activeWindow(ctx)
	if ctx.Err() != nil {
		return res
	}
	if text := firstOutput(ctx, primaryCommands(os.Getenv, exec.LookPath)); text != "" {
		res.Text, res.Grabbed = text, true
		return res
	}
	res.Text, _ = fallbackQuote(firstOutput(ctx, clipboardCommands(os.Getenv, exec.LookPath)))
	return res
}

// AccessibilityTrusted is always true on Linux: reading the primary
// selection needs no permission.
func AccessibilityTrusted() bool { return true }

// primaryCommands are the commands that print the primary selection, in
// the order to try them, keeping only installed tools: wl-paste first on
// Wayland (WAYLAND_DISPLAY set), then xclip, then xsel (X11 or XWayland).
// wl-paste asks for text/plain: without a type it prints whatever the source
// offers first, which can be image bytes.
func primaryCommands(env func(string) string, look func(string) (string, error)) [][]string {
	return installedCommands(env, look,
		[]string{"wl-paste", "--primary", "--no-newline", "--type", "text/plain"},
		[]string{"xclip", "-o", "-selection", "primary"},
		[]string{"xsel", "-o", "-p"})
}

// clipboardCommands are primaryCommands for the clipboard, for the
// fallback.
func clipboardCommands(env func(string) string, look func(string) (string, error)) [][]string {
	return installedCommands(env, look,
		[]string{"wl-paste", "--no-newline", "--type", "text/plain"},
		[]string{"xclip", "-o", "-selection", "clipboard"},
		[]string{"xsel", "-o", "-b"})
}

// installedCommands keeps the commands whose tool is installed: wayland
// only when WAYLAND_DISPLAY is set, then the X11 ones.
func installedCommands(env func(string) string, look func(string) (string, error), wayland []string, x11 ...[]string) [][]string {
	var cands [][]string
	if env("WAYLAND_DISPLAY") != "" {
		cands = append(cands, wayland)
	}
	cands = append(cands, x11...)
	var out [][]string
	for _, c := range cands {
		if _, err := look(c[0]); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// firstOutput runs each command in turn and returns the first non-empty
// output.
func firstOutput(ctx context.Context, cmds [][]string) string {
	for _, c := range cmds {
		if ctx.Err() != nil {
			return ""
		}
		if out := run(ctx, c); out != "" {
			return out
		}
	}
	return ""
}

// run runs a command with a 150 ms timeout and returns its standard
// output, or "" on failure or when the output is not valid UTF-8 text. An
// empty selection makes these tools exit non-zero, so failures are not
// logged unless they are timeouts.
func run(ctx context.Context, argv []string) string {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Printf("grab: %s: timed out", argv[0])
		}
		return ""
	}
	return textOutput(out.Bytes())
}

// activeWindow is the active window's WM_CLASS class and _NET_WM_NAME,
// from xprop, or empty when xprop is missing or finds no window.
func activeWindow(ctx context.Context) (process, title string) {
	if _, err := exec.LookPath("xprop"); err != nil {
		return "", ""
	}
	id := parseActiveWindow(run(ctx, []string{"xprop", "-root", "_NET_ACTIVE_WINDOW"}))
	if id == "" {
		return "", ""
	}
	return parseWindowProps(run(ctx, []string{"xprop", "-id", id, "WM_CLASS", "_NET_WM_NAME"}))
}

// parseActiveWindow takes the window id from
// "_NET_ACTIVE_WINDOW(WINDOW): window id # 0x3a00007", or "" when there is
// none (0x0, or "not found").
func parseActiveWindow(out string) string {
	_, id, ok := strings.Cut(out, "window id # ")
	if !ok {
		return ""
	}
	id = strings.TrimSpace(id)
	if f := strings.Fields(id); len(f) > 0 {
		id = strings.TrimSuffix(f[0], ",")
	}
	if n, err := strconv.ParseUint(strings.TrimPrefix(id, "0x"), 16, 64); err != nil || n == 0 {
		return ""
	}
	return id
}

// parseWindowProps takes the class (the second WM_CLASS string) and the
// title from xprop's WM_CLASS and _NET_WM_NAME lines.
func parseWindowProps(out string) (process, title string) {
	for _, line := range strings.Split(out, "\n") {
		name, value, ok := strings.Cut(line, " = ")
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(name, "WM_CLASS"):
			if s := quotedStrings(value); len(s) > 0 {
				process = s[len(s)-1]
			}
		case strings.HasPrefix(name, "_NET_WM_NAME"):
			if s := quotedStrings(value); len(s) > 0 {
				title = s[0]
			}
		}
	}
	return process, title
}

// quotedStrings parses xprop's comma-separated "..." values, undoing its
// backslash escapes.
func quotedStrings(s string) []string {
	var out []string
	for {
		i := strings.IndexByte(s, '"')
		if i < 0 {
			return out
		}
		s = s[i+1:]
		var b strings.Builder
		closed := false
		for j := 0; j < len(s); j++ {
			c := s[j]
			if c == '\\' && j+1 < len(s) {
				j++
				b.WriteByte(s[j])
				continue
			}
			if c == '"' {
				s = s[j+1:]
				closed = true
				break
			}
			b.WriteByte(c)
		}
		if !closed {
			return out
		}
		out = append(out, b.String())
	}
}
