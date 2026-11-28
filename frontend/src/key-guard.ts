// Keyboard guard for a capture box opened by "Hey Awesome" (spec "Keyboard
// guard"). The wake show normally leaves the keyboard with the user's app;
// where the box gets focus anyway (Wayland, a failed native call), stray
// typing must not reach the input or save anything until the user engages:
// a click in the box, the hotkey or Ctrl+M (they call release()).

const MODIFIERS = new Set(["Shift", "Control", "Alt", "Meta", "AltGraph", "CapsLock", "OS"]);

export class KeyGuard {
  private on = false;

  arm(): void {
    this.on = true;
  }

  release(): void {
    this.on = false;
  }

  armed(): boolean {
    return this.on;
  }

  /**
   * Whether a keydown may do its usual thing. While armed only Esc (it
   * cancels the session and hides the box) and modifier-only keys pass;
   * printable keys, Enter, Backspace, Delete, Tab and every other key or
   * shortcut (a paste would change the draft too) are blocked.
   */
  allows(e: KeyboardEvent): boolean {
    if (!this.on) return true;
    return e.key === "Escape" || MODIFIERS.has(e.key);
  }
}
