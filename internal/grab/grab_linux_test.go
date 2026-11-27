//go:build linux

package grab

import (
	"errors"
	"reflect"
	"testing"
)

// installed returns a LookPath stand-in that finds only the named tools.
func installed(tools ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		for _, t := range tools {
			if t == name {
				return "/usr/bin/" + name, nil
			}
		}
		return "", errors.New("not found")
	}
}

func envOf(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

var (
	wlPrimary = []string{"wl-paste", "--primary", "--no-newline"}
	xclipPrim = []string{"xclip", "-o", "-selection", "primary"}
	xselPrim  = []string{"xsel", "-o", "-p"}
)

func TestLinuxPrimaryCommandOrder(t *testing.T) {
	wayland := envOf(map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"})
	x11 := envOf(map[string]string{"DISPLAY": ":0"})
	all := installed("wl-paste", "xclip", "xsel")
	cases := []struct {
		name string
		env  func(string) string
		look func(string) (string, error)
		want [][]string
	}{
		{"wayland: wl-paste first", wayland, all, [][]string{wlPrimary, xclipPrim, xselPrim}},
		{"wayland without wl-paste", wayland, installed("xsel"), [][]string{xselPrim}},
		{"x11: xclip then xsel", x11, all, [][]string{xclipPrim, xselPrim}},
		{"x11: xsel only", x11, installed("xsel"), [][]string{xselPrim}},
		{"nothing installed", x11, installed(), nil},
		{"wayland, nothing installed", wayland, installed(), nil},
	}
	for _, c := range cases {
		if got := primaryCommands(c.env, c.look); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: primaryCommands = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestLinuxClipboardCommandOrder(t *testing.T) {
	wayland := envOf(map[string]string{"WAYLAND_DISPLAY": "wayland-0"})
	x11 := envOf(map[string]string{"DISPLAY": ":0"})
	all := installed("wl-paste", "xclip", "xsel")
	wl := []string{"wl-paste", "--no-newline"}
	xc := []string{"xclip", "-o", "-selection", "clipboard"}
	xs := []string{"xsel", "-o", "-b"}
	if got, want := clipboardCommands(wayland, all), [][]string{wl, xc, xs}; !reflect.DeepEqual(got, want) {
		t.Errorf("wayland: clipboardCommands = %q, want %q", got, want)
	}
	if got, want := clipboardCommands(x11, all), [][]string{xc, xs}; !reflect.DeepEqual(got, want) {
		t.Errorf("x11: clipboardCommands = %q, want %q", got, want)
	}
	if got := clipboardCommands(x11, installed()); got != nil {
		t.Errorf("nothing installed: clipboardCommands = %q, want nil", got)
	}
}

func TestParseActiveWindow(t *testing.T) {
	cases := map[string]string{
		"_NET_ACTIVE_WINDOW(WINDOW): window id # 0x3a00007\n": "0x3a00007",
		"_NET_ACTIVE_WINDOW(WINDOW): window id # 0x0\n":       "",
		"_NET_ACTIVE_WINDOW:  not found.\n":                   "",
		"":                                                    "",
	}
	for in, want := range cases {
		if got := parseActiveWindow(in); got != want {
			t.Errorf("parseActiveWindow(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseWindowProps(t *testing.T) {
	out := "WM_CLASS(STRING) = \"Navigator\", \"firefox\"\n" +
		"_NET_WM_NAME(UTF8_STRING) = \"Inbox - \\\"Work\\\" - Mozilla Firefox\"\n"
	process, title := parseWindowProps(out)
	if process != "firefox" || title != `Inbox - "Work" - Mozilla Firefox` {
		t.Errorf("parseWindowProps = %q, %q; want %q, %q", process, title, "firefox", `Inbox - "Work" - Mozilla Firefox`)
	}
	process, title = parseWindowProps("WM_CLASS:  not found.\n_NET_WM_NAME:  not found.\n")
	if process != "" || title != "" {
		t.Errorf("parseWindowProps(not found) = %q, %q; want empty", process, title)
	}
}
