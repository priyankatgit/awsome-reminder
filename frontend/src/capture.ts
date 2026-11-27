// Capture box (#capture): one-line input with a live parse preview.
// Layout and behaviour follow the "Capture box" section of the UI guide.
import { api, errorText, events, on, type Preview } from "./api";
import { chip, el, motionMs, tagChip } from "./dom";
import { icons, type IconName } from "./icons";

const PREVIEW_DEBOUNCE_MS = 80;

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
  let hideTimer: number | undefined; // pending flash/fade before hiding
  let endDraft = false; // the current text was saved or dismissed

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
    for (const t of p.tags ?? []) line.append(tagChip(t));
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

  /** Clears transient state; the draft text too unless keepDraft. */
  function reset(keepDraft = false): void {
    seq++;
    window.clearTimeout(timer);
    if (!keepDraft) input.value = "";
    showError("");
    closePicker(false);
    if (input.value.trim() === "") renderPreview(null); // a kept draft keeps its preview until refreshed
    flash.classList.remove("is-on");
    busy = false;
  }

  /**
   * Save and Esc end the draft: fade out, hide, clear. The pending timer is
   * kept so open() can cancel it if the box is reopened meanwhile.
   */
  function hide(): void {
    endDraft = true;
    box.classList.add("is-hidden");
    window.clearTimeout(hideTimer);
    hideTimer = window.setTimeout(() => {
      void api.hideCapture();
      reset();
      endDraft = false;
    }, motionMs("--dur-exit"));
  }

  function open(): void {
    window.clearTimeout(hideTimer); // a save/Esc hide still pending must not hide the new box
    reset(!endDraft); // keep a draft left by blur; drop one already saved or dismissed
    endDraft = false;
    box.classList.remove("is-hidden", "is-entering");
    void box.offsetWidth; // restart the enter animation
    box.classList.add("is-entering");
    input.focus();
    input.setSelectionRange(input.value.length, input.value.length);
    if (input.value.trim() !== "") void runPreview();
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
    endDraft = true;
    flash.classList.add("is-on");
    window.clearTimeout(hideTimer);
    hideTimer = window.setTimeout(hide, motionMs("--dur-flash"));
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

  // Go hides the window when it loses focus. Keep the draft text so a
  // half-typed thought survives; the box starts invisible on the next show
  // and open() restores it with the caret at the end.
  window.addEventListener("blur", () => {
    box.classList.add("is-hidden");
    closePicker(false);
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
