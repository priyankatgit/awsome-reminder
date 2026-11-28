// Theme: black ("Obsidian") by default on every window, whatever the OS
// theme. Settings › Appearance can switch on "Match Windows", which shows the
// light palette while the OS is light. The choice is a per-device UI
// preference in localStorage (all windows share one origin), so a change
// reaches every open window through the storage event.
// See docs/superpowers/specs/2026-11-28-black-glossy-theme-design.md (R8).

export type ThemeChoice = "black" | "system";
export type Theme = "dark" | "light";

export const THEME_KEY = "ar.theme";
const LIGHT_QUERY = "(prefers-color-scheme: light)";

/** The saved choice; anything unknown (or no storage) is the black default. */
export function themeChoice(): ThemeChoice {
  try {
    return localStorage.getItem(THEME_KEY) === "system" ? "system" : "black";
  } catch {
    return "black";
  }
}

/** Which palette a choice shows, given whether the OS is light. */
export function resolveTheme(choice: ThemeChoice, osLight: boolean): Theme {
  return choice === "system" && osLight ? "light" : "dark";
}

function osLight(): boolean {
  return typeof matchMedia === "function" && matchMedia(LIGHT_QUERY).matches;
}

/** Sets <html data-theme> from the saved choice and the OS theme. */
export function applyTheme(): Theme {
  const t = resolveTheme(themeChoice(), osLight());
  document.documentElement.dataset.theme = t;
  return t;
}

/** Saves the choice and applies it here; other windows follow via storage. */
export function setThemeChoice(c: ThemeChoice): void {
  try {
    if (c === "black") localStorage.removeItem(THEME_KEY);
    else localStorage.setItem(THEME_KEY, c);
  } catch {
    // No storage: the choice lasts for this window only.
  }
  applyTheme();
}

/** Applies the theme now and keeps it current (other windows, OS changes). */
export function installTheme(): void {
  applyTheme();
  window.addEventListener("storage", (e) => {
    if (e.key === THEME_KEY || e.key === null) applyTheme();
  });
  if (typeof matchMedia === "function") matchMedia(LIGHT_QUERY).addEventListener?.("change", () => applyTheme());
}
