package hotkey

import "testing"

func TestParseCombo(t *testing.T) {
	ok := []struct{ in, want string }{
		{"ctrl+shift+space", "Ctrl+Shift+Space"},
		{"Cmd+Shift+K", "Shift+Cmd+K"},
		{" Shift + Control + f12 ", "Ctrl+Shift+F12"},
		{"alt+ctrl+ctrl+enter", "Ctrl+Alt+Enter"},
		{"Option+Command+Esc", "Alt+Cmd+Escape"},
		{"Meta+Tab", "Cmd+Tab"},
		{"Ctrl+0", "Ctrl+0"},
		{"CTRL+alt+z", "Ctrl+Alt+Z"},
		{"Ctrl+Shift+Space", "Ctrl+Shift+Space"},
		{"Shift+Alt+Tab", "Alt+Shift+Tab"},
	}
	for _, tc := range ok {
		c, err := ParseCombo(tc.in)
		if err != nil {
			t.Errorf("ParseCombo(%q): %v", tc.in, err)
			continue
		}
		if got := c.String(); got != tc.want {
			t.Errorf("ParseCombo(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	bad := []struct{ in, msg string }{
		{"space", "hotkey needs Ctrl, Alt or Cmd (Shift alone isn't enough)"},
		{"Shift+A", "hotkey needs Ctrl, Alt or Cmd (Shift alone isn't enough)"},
		{"Shift+Space", "hotkey needs Ctrl, Alt or Cmd (Shift alone isn't enough)"},
		{"Shift+Tab", "hotkey needs Ctrl, Alt or Cmd (Shift alone isn't enough)"},
		{"Shift+Enter", "hotkey needs Ctrl, Alt or Cmd (Shift alone isn't enough)"},
		{"", "hotkey is empty"},
		{"Ctrl+Foo", `unknown key "Foo"`},
		{"Ctrl+Shift", "hotkey needs a key (for example Space or K)"},
		{"Ctrl+A+B", "hotkey can only have one key (got A and B)"},
		{"Ctrl++A", "hotkey has an empty part"},
		{"Ctrl+F13", `unknown key "F13"`},
	}
	for _, tc := range bad {
		_, err := ParseCombo(tc.in)
		if err == nil {
			t.Errorf("ParseCombo(%q): expected error", tc.in)
		} else if err.Error() != tc.msg {
			t.Errorf("ParseCombo(%q) error = %q, want %q", tc.in, err, tc.msg)
		}
	}
}
