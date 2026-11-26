// Capture box (#capture): one-line input with a live parse preview.
// Layout and behaviour follow the "Capture box" section of the UI guide.
import { api, errorText, events, on, type Preview } from "./api";
import { icons, type IconName } from "./icons";

const PREVIEW_DEBOUNCE_MS = 80;
const TAG_HUES = 8;

const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");

/** Duration of a CSS time token in ms (0 under reduced motion). */
function motionMs(token: string): number {
  if (reducedMotion.matches) return 0;
  const v = getComputedStyle(document.documentElement).getPropertyValue(token).trim();
  const n = parseFloat(v);
  return v.endsWith("ms") ? n : n * 1000;
}

/** Stable tag colour slot: FNV-1a over the tag, mod 8. "inbox" is neutral. */
export function tagSlot(tag: string): string {
  if (tag === "inbox") return "inbox";
  let h = 0x811c9dc5;
  for (let i = 0; i < tag.length; i++) {
    h ^= tag.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return String(h % TAG_HUES);
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function chip(text: string, cls: string): HTMLElement {
  return el("span", `chip ${cls}`, text);
}

/** "YYYY-MM-DDTHH:MM" in local time, for <input type="datetime-local">. */
function toLocalInput(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

const TEMPLATE = `
<main class="capture" id="capture" aria-label="Quick capture">
  <div class="capture-content" id="capture-content">
    <div class="cap-field">
      <span class="cap-lead" id="cap-lead" data-icon="search"></span>
      <input class="cap-input" id="cap-input" type="text" autocomplete="off" spellcheck="false"
             aria-label="To-do or reminder" aria-describedby="cap-preview cap-hint"
             placeholder="remind me tomorrow 5pm check deploy #backend" />
      <div class="cap-action-slot" aria-hidden="true"></div>
      <span class="cap-flash" id="cap-flash" aria-hidden="true"></span>
    </div>
    <p class="cap-error" id="cap-error" role="alert" hidden></p>
    <div class="cap-picker" id="cap-picker" hidden>
      <label class="cap-picker-label" for="cap-due">Due</label>
      <input class="cap-due" id="cap-due" type="datetime-local" />
      <button class="btn btn-primary" id="cap-due-set" type="button">Set time</button>
      <span class="cap-picker-hint"><kbd>↵</kbd> set <kbd>Esc</kbd> cancel</span>
    </div>
    <div class="cap-preview" id="cap-preview" role="status" aria-live="polite"></div>
    <footer class="cap-hint" id="cap-hint">
      <span><kbd>↵</kbd> save</span>
      <span><kbd>Tab</kbd> time</span>
      <span><kbd>Esc</kbd> close</span>
    </footer>
  </div>
</main>`;

export function mountCapture(root: HTMLElement): void {
  root.innerHTML = TEMPLATE;
  const box = root.querySelector<HTMLElement>("#capture")!;
  const content = root.querySelector<HTMLElement>("#capture-content")!;
  const lead = root.querySelector<HTMLElement>("#cap-lead")!;
  const input = root.querySelector<HTMLInputElement>("#cap-input")!;
  const flash = root.querySelector<HTMLElement>("#cap-flash")!;
  const errorRow = root.querySelector<HTMLElement>("#cap-error")!;
  const picker = root.querySelector<HTMLElement>("#cap-picker")!;
  const due = root.querySelector<HTMLInputElement>("#cap-due")!;
  const dueSet = root.querySelector<HTMLButtonElement>("#cap-due-set")!;
  const previewRow = root.querySelector<HTMLElement>("#cap-preview")!;

  let seq = 0; // invalidates in-flight previews
  let timer: number | undefined;
  let current: Preview | null = null;
  let busy = false; // a save or hide is in progress

  function setLead(name: IconName): void {
    if (lead.dataset.icon === name && lead.firstChild) return;
    lead.dataset.icon = name;
    lead.innerHTML = icons[name];
  }

  function showError(msg: string): void {
    errorRow.textContent = msg;
    errorRow.hidden = msg === "";
  }

  function renderPreview(p: Preview | null): void {
    current = p;
    previewRow.replaceChildren();
    if (!p) {
      setLead("search");
      previewRow.append(el("span", "cap-empty", "Type a to-do, or add a time like “tomorrow 5pm”"));
      return;
    }
    const reminder = p.kind === "reminder";
    setLead(reminder ? "bell" : "todo");

    const line = el("div", "cap-line");
    line.append(el("span", "cap-kind", reminder ? "Reminder" : "To-do"));
    if (reminder && p.human) line.append(chip(p.human, "chip-time"));
    for (const t of p.tags ?? []) {
      const c = chip(`#${t}`, "chip-tag");
      c.dataset.hue = tagSlot(t);
      line.append(c);
    }
    previewRow.append(line);

    if (reminder && p.alerts && p.alerts.length > 0) {
      previewRow.append(el("div", "cap-sub", `alerts ${p.alerts.map((a) => a.replace(/ before$/, "")).join(" · ")}`));
    } else if (reminder) {
      previewRow.append(el("div", "cap-sub", "already past: no alerts"));
    }
    for (const w of p.warnings ?? []) {
      const row = el("div", "cap-warning");
      row.innerHTML = icons.warning;
      row.append(el("span", undefined, w));
      previewRow.append(row);
    }
  }

  async function runPreview(): Promise<void> {
    const my = ++seq;
    const text = input.value;
    if (text.trim() === "") {
      renderPreview(null);
      return;
    }
    try {
      const p = await api.preview(text);
      if (my === seq) renderPreview(p);
    } catch (err) {
      if (my === seq) showError(errorText(err));
    }
  }

  function schedulePreview(): void {
    window.clearTimeout(timer);
    timer = window.setTimeout(runPreview, PREVIEW_DEBOUNCE_MS);
  }

  function closePicker(refocus: boolean): void {
    picker.hidden = true;
    if (refocus) input.focus();
  }

  function reset(): void {
    seq++;
    window.clearTimeout(timer);
    input.value = "";
    showError("");
    closePicker(false);
    renderPreview(null);
    flash.classList.remove("is-on");
    busy = false;
  }

  /** Fades the box out, then asks Go to hide the window. */
  function hide(): void {
    box.classList.add("is-hidden");
    window.setTimeout(() => {
      void api.hideCapture();
      reset();
    }, motionMs("--dur-exit"));
  }

  function open(): void {
    reset();
    box.classList.remove("is-hidden", "is-entering");
    void box.offsetWidth; // restart the enter animation
    box.classList.add("is-entering");
    input.focus();
  }

  async function save(): Promise<void> {
    if (busy || input.value.trim() === "") return;
    busy = true;
    showError("");
    try {
      await api.capture(input.value);
    } catch (err) {
      busy = false;
      const msg = errorText(err);
      showError(msg.includes("empty input") ? "Add some text to save" : msg);
      return;
    }
    flash.classList.add("is-on");
    window.setTimeout(hide, motionMs("--dur-flash"));
  }

  async function openPicker(): Promise<void> {
    let d: Date;
    if (current?.dueAt) {
      d = new Date(current.dueAt);
    } else {
      let hh = 9;
      let mm = 0;
      try {
        const s = await api.getSettings();
        [hh, mm] = s.defaultTime.split(":").map(Number);
      } catch {
        // keep 09:00
      }
      d = new Date();
      d.setDate(d.getDate() + 1);
      d.setHours(hh, mm, 0, 0);
    }
    due.value = toLocalInput(d);
    picker.hidden = false;
    due.focus();
  }

  async function confirmPicker(): Promise<void> {
    if (!due.value) {
      closePicker(true);
      return;
    }
    try {
      input.value = await api.withDue(input.value, new Date(due.value));
    } catch (err) {
      showError(errorText(err));
    }
    closePicker(true);
    input.setSelectionRange(input.value.length, input.value.length);
    void runPreview();
  }

  input.addEventListener("input", () => {
    showError("");
    schedulePreview();
  });

  input.addEventListener("keydown", (e) => {
    if (e.isComposing) return;
    if (e.key === "Enter") {
      e.preventDefault();
      void save();
    } else if (e.key === "Tab" && !e.shiftKey) {
      e.preventDefault();
      void openPicker();
    }
  });

  picker.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      void confirmPicker();
    }
  });
  dueSet.addEventListener("click", () => void confirmPicker());

  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return;
    e.preventDefault();
    if (!picker.hidden) closePicker(true);
    else if (!busy) hide();
  });

  // Go hides the window when it loses focus; make sure the next show starts
  // from a clean, invisible state instead of flashing the old content.
  window.addEventListener("blur", () => {
    box.classList.add("is-hidden");
    reset();
  });
  window.addEventListener("focus", () => {
    box.classList.remove("is-hidden");
    if (picker.hidden) input.focus();
  });

  // Size the native window to the content; the top edge stays put.
  new ResizeObserver(() => {
    void api.resizeCapture(Math.ceil(content.getBoundingClientRect().height));
  }).observe(content);

  on(events.captureOpen, open);
  renderPreview(null);
  input.focus();
}
