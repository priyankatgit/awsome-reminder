//go:build linux

package hotkey

import lib "golang.design/x/hotkey"

const goos = "linux"

var libMods = map[string]lib.Modifier{
	"Ctrl": lib.ModCtrl, "Alt": lib.Mod1, "Shift": lib.ModShift,
}

var libKeys = map[string]lib.Key{
	"Space": lib.KeySpace, "Enter": lib.KeyReturn, "Tab": lib.KeyTab, "Escape": lib.KeyEscape,
	"0": lib.Key0, "1": lib.Key1, "2": lib.Key2, "3": lib.Key3, "4": lib.Key4,
	"5": lib.Key5, "6": lib.Key6, "7": lib.Key7, "8": lib.Key8, "9": lib.Key9,
	"A": lib.KeyA, "B": lib.KeyB, "C": lib.KeyC, "D": lib.KeyD, "E": lib.KeyE, "F": lib.KeyF,
	"G": lib.KeyG, "H": lib.KeyH, "I": lib.KeyI, "J": lib.KeyJ, "K": lib.KeyK, "L": lib.KeyL,
	"M": lib.KeyM, "N": lib.KeyN, "O": lib.KeyO, "P": lib.KeyP, "Q": lib.KeyQ, "R": lib.KeyR,
	"S": lib.KeyS, "T": lib.KeyT, "U": lib.KeyU, "V": lib.KeyV, "W": lib.KeyW, "X": lib.KeyX,
	"Y": lib.KeyY, "Z": lib.KeyZ,
	"F1": lib.KeyF1, "F2": lib.KeyF2, "F3": lib.KeyF3, "F4": lib.KeyF4, "F5": lib.KeyF5, "F6": lib.KeyF6,
	"F7": lib.KeyF7, "F8": lib.KeyF8, "F9": lib.KeyF9, "F10": lib.KeyF10, "F11": lib.KeyF11, "F12": lib.KeyF12,
}

// mainVK has no virtual-key table here (the registrar's key codes are not
// Windows ones); nothing on this OS reads the codes.
func mainVK(string) (uint16, bool) { return 0, false }
