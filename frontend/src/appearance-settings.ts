// Settings › Appearance: black by default; "Match Windows" shows the light
// palette while Windows is light. Applies at once, outside the Save flow
// (like the Sync and Voice sections), to every open window.
import { setThemeChoice, themeChoice } from "./theme";

const TEMPLATE = `
<h2 class="sync-title" id="appearance-title">Appearance</h2>
<div class="field">
  <div class="field-row field-switch">
    <label class="field-label" for="theme-system">Match Windows light mode</label>
    <input class="switch" id="theme-system" type="checkbox" role="switch" aria-describedby="theme-help" />
  </div>
  <p class="field-help" id="theme-help">Black is the default. Turn this on to go light while Windows is light. Applies right away.</p>
</div>`;

/** Mounts the section into host (an empty element after the settings form). */
export function mountAppearanceSection(host: HTMLElement): void {
  host.setAttribute("aria-labelledby", "appearance-title");
  host.innerHTML = TEMPLATE;
  const sw = host.querySelector<HTMLInputElement>("#theme-system")!;
  sw.checked = themeChoice() === "system";
  sw.addEventListener("change", () => setThemeChoice(sw.checked ? "system" : "black"));
  // Another settings window (or a reset) changed it.
  window.addEventListener("storage", () => {
    sw.checked = themeChoice() === "system";
  });
}
