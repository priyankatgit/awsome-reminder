// Capture box (#capture): one-line input with a live parse preview.
// Layout and behaviour follow the "Capture box" section of the UI guide.
import { api, errorText, events, on, type Preview } from "./api";
import { el, motionMs, tagChip } from "./dom";
import { icons, type IconName } from "./icons";
import { canAnimate, easing, pop, ring, tokenNumber } from "./motion";
import { priorityLabel, setPriorityToken, type Priority } from "./priority";
import { mountVoice } from "./voice";

const PREVIEW_DEBOUNCE_MS = 80;

/** Capture chips in Alt+digit order; Alt+0 clears. */
const CHIPS: { p: Exclude<Priority, "">; text: string; key: string }[] = [
  { p: "high", text: "High", key: "1" },
  { p: "medium", text: "Med", key: "2" },
  { p: "low", text: "Low", key: "3" },
];

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
      <div class="cap-listen" id="cap-listen" hidden>
        <span class="cap-listen-label" id="cap-listen-label">Listening…</span>
        <canvas class="cap-wave" id="cap-wave" aria-hidden="true"></canvas>
        <span class="cap-timer" id="cap-timer">0:00</span>
      </div>
      <div class="cap-action-slot">
        <button class="mic-btn" id="cap-mic" type="button" aria-label="Start dictation" aria-pressed="false"
                title="Dictate (Ctrl+M)"><span class="mic-ring" aria-hidden="true"></span><span class="mic-icon" id="cap-mic-icon"></span></button>
      </div>
      <span class="cap-flash" id="cap-flash" aria-hidden="true"></span>
    </div>
    <p class="cap-error" id="cap-error" role="alert" hidden></p>
    <p class="sr-only" id="cap-voice-live" role="status" aria-live="polite"></p>
    <section class="cap-voice" id="cap-voice" aria-labelledby="cap-voice-title" hidden>
      <h2 class="cap-voice-title" id="cap-voice-title"></h2>
      <p class="cap-voice-body" id="cap-voice-body"></p>
      <div class="cap-progress" id="cap-progress" role="progressbar" aria-label="Voice download"
           aria-valuemin="0" aria-valuemax="100" aria-valuenow="0" hidden><span class="cap-progress-bar" id="cap-progress-bar"></span></div>
      <p class="cap-voice-meta" id="cap-voice-meta"></p>
      <div class="cap-voice-actions" id="cap-voice-actions"></div>
    </section>
    <div class="cap-picker" id="cap-picker" hidden>
      <label class="cap-picker-label" for="cap-due">Due</label>
      <input class="cap-due" id="cap-due" type="datetime-local" />
      <button class="btn btn-primary" id="cap-due-set" type="button">Set time</button>
      <span class="cap-picker-hint"><kbd>↵</kbd> set <kbd>Esc</kbd> cancel</span>
    </div>
    <div class="cap-prio" id="cap-prio" role="group" aria-label="Priority">
      ${CHIPS.map(
        (c) =>
          `<button class="prio-chip" type="button" data-prio="${c.p}" aria-pressed="false" aria-keyshortcuts="Alt+${c.key}"
                   title="${priorityLabel(c.p)} priority (Alt+${c.key})"><span class="prio-dot" aria-hidden="true"></span>${c.text}</button>`,
      ).join("")}
      <span class="cap-prio-hint">
        <span><kbd>Alt</kbd><span class="keycap-plus">+</span><kbd>1</kbd>–<kbd>3</kbd> priority</span>
        <span><kbd>Alt</kbd><span class="keycap-plus">+</span><kbd>0</kbd> clear</span>
      </span>
    </div>
    <div class="cap-preview" id="cap-preview" role="status" aria-live="polite"></div>
    <div class="cap-listen-sub">Speak naturally — “remind me tomorrow at 5 pm to call Sam”</div>
    <footer class="cap-hint" id="cap-hint">
      <span><kbd>↵</kbd> save</span>
      <span><kbd>Tab</kbd> time</span>
      <span><kbd>Ctrl</kbd><span class="keycap-plus">+</span><kbd>M</kbd> dictate</span>
      <span><kbd>Esc</kbd> close</span>
    </footer>
    <footer class="cap-hint cap-hint-voice">
      <span>auto-stops after a pause</span>
      <span><kbd>Esc</kbd> cancel</span>
      <span><kbd>Ctrl</kbd><span class="keycap-plus">+</span><kbd>M</kbd> stop</span>
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
  const prioChips = [...root.querySelectorAll<HTMLButtonElement>(".prio-chip")];

  // The preview row keeps the reminder badge and its announcement across
  // renders so they can animate in and out; only the rest is rebuilt.
  const remindSay = el("span", "sr-only");
  const badge = el("span", "chip chip-remind");
  badge.setAttribute("aria-hidden", "true"); // remindSay carries the words
  badge.hidden = true;
  const lineBody = el("span", "cap-line-body");
  const more = el("div", "cap-more");
  const line = el("div", "cap-line");
  line.append(badge, lineBody);
  previewRow.append(remindSay, line, more);

  let seq = 0; // invalidates in-flight previews
  let timer: number | undefined;
  let current: Preview | null = null;
  let busy = false; // a save or hide is in progress
  let hideTimer: number | undefined; // pending flash/fade before hiding
  let endDraft = false; // the current text was saved or dismissed

  let leadIcon: IconName = "search"; // what the parse preview wants
  let listeningLead = false; // voice shows a mic instead while recording

  let remindOn = false; // a due time is detected and the indicator is shown
  let remindHuman = "";
  let restingLead: IconName = "search"; // the lead once the reminder fades out
  let remindTimer: number | undefined; // pending end of the fade-out

  let fly: Animation | null = null; // the saved line flying to the tray

  function setLead(name: IconName): void {
    leadIcon = name;
    if (listeningLead) return;
    paintLead(name);
  }

  function paintLead(name: IconName): void {
    if (lead.dataset.icon === name && lead.firstChild) return;
    lead.dataset.icon = name;
    lead.innerHTML = icons[name];
  }

  function showError(msg: string): void {
    errorRow.textContent = msg;
    errorRow.hidden = msg === "";
  }

  /** Restarts a one-shot CSS animation class and drops it when it ends. */
  function play(node: HTMLElement, cls: string): void {
    node.classList.remove(cls);
    void node.offsetWidth;
    node.classList.add(cls);
    node.addEventListener("animationend", () => node.classList.remove(cls), { once: true });
  }

  /**
   * The live reminder indicator: a light-red bell that pops in and a
   * "Reminder · <human>" badge leading the preview row, both fading out when
   * the time words go away. Announced through the preview's role=status.
   */
  function setRemind(on: boolean, human: string): void {
    if (on) {
      window.clearTimeout(remindTimer);
      lead.classList.remove("is-leaving");
      badge.classList.remove("is-leaving");
      badge.textContent = `Reminder · ${human}`;
      badge.hidden = false;
      if (!remindOn || human !== remindHuman) remindSay.textContent = `Reminder detected for ${human}`;
      if (!remindOn) {
        lead.classList.add("is-remind");
        setLead("bell");
        play(lead, "is-pop");
        play(badge, "is-pop");
        const bell = lead.querySelector("svg");
        if (bell && !listeningLead) ring(bell); // once, after the pop
      }
      remindOn = true;
      remindHuman = human;
      return;
    }
    if (!remindOn) {
      if (!lead.classList.contains("is-leaving")) setLead(restingLead); // mid-fade, endRemind sets it
      return;
    }
    remindOn = false;
    remindHuman = "";
    remindSay.textContent = "Reminder removed";
    lead.classList.add("is-leaving");
    badge.classList.add("is-leaving");
    remindTimer = window.setTimeout(endRemind, motionMs("--dur-pop-exit"));
  }

  /** Puts the indicator away at once (end of the fade-out, or a reset). */
  function endRemind(): void {
    window.clearTimeout(remindTimer);
    lead.classList.remove("is-leaving", "is-remind", "is-pop");
    badge.classList.remove("is-leaving", "is-pop");
    badge.hidden = true;
    remindOn = false;
    remindHuman = "";
    setLead(restingLead);
  }

  /** Shows priority p on the chips; a chip turning on pops on the spring. */
  function showPriority(p: string): void {
    for (const c of prioChips) {
      const on = c.dataset.prio === p;
      const was = c.getAttribute("aria-pressed") === "true";
      c.setAttribute("aria-pressed", String(on));
      if (on && !was) pop(c, "--chip-pop-scale", "--dur-chip");
    }
  }

  function renderPreview(p: Preview | null): void {
    current = p;
    lineBody.replaceChildren();
    more.replaceChildren();
    showPriority(p?.priority ?? "");
    restingLead = !p ? "search" : p.dueAt ? "bell" : "todo";
    setRemind(Boolean(p?.dueAt), p?.human ?? "");
    if (!p) {
      lineBody.append(el("span", "cap-empty", "Type a to-do, or add a time like “tomorrow 5pm”"));
      return;
    }
    const reminder = Boolean(p.dueAt);
    if (!reminder) lineBody.append(el("span", "cap-kind", "To-do"));
    for (const t of p.tags ?? []) lineBody.append(tagChip(t));

    if (reminder && p.alerts && p.alerts.length > 0) {
      more.append(el("div", "cap-sub", `alerts ${p.alerts.map((a) => a.replace(/ before$/, "")).join(" · ")}`));
    } else if (reminder) {
      more.append(el("div", "cap-sub", "already past: no alerts"));
    }
    for (const w of p.warnings ?? []) {
      const row = el("div", "cap-warning");
      row.innerHTML = icons.warning;
      row.append(el("span", undefined, w));
      more.append(row);
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
    if (input.value.trim() === "") {
      // A new draft starts quiet: no fade-out, no "Reminder removed".
      // A kept draft keeps its preview until refreshed.
      remindSay.textContent = "";
      restingLead = "search";
      endRemind();
      renderPreview(null);
    }
    flash.classList.remove("is-on");
    endFly();
    busy = false;
  }

  /**
   * After a save: a copy of the typed line shrinks and flies to the
   * bottom-right corner (towards the tray) while the input shows nothing.
   * Returns false when motion is off, and the caller hides as before.
   */
  function flyAway(): boolean {
    if (!canAnimate(input) || input.value.trim() === "") return false;
    const r = input.getBoundingClientRect();
    const cs = getComputedStyle(input);
    const padL = parseFloat(cs.paddingLeft) || 0;
    const padR = parseFloat(cs.paddingRight) || 0;
    const ghost = el("span", "cap-fly", input.value);
    ghost.setAttribute("aria-hidden", "true");
    Object.assign(ghost.style, {
      left: `${r.left + padL}px`,
      top: `${r.top}px`,
      maxWidth: `${Math.max(r.width - padL - padR, 0)}px`,
      height: `${r.height}px`,
      lineHeight: `${r.height}px`,
    });
    box.append(ghost);
    box.classList.add("is-flying"); // the input's own text and caret go blank
    const g = ghost.getBoundingClientRect();
    const dx = window.innerWidth - g.right;
    const dy = window.innerHeight - g.bottom;
    const lift = tokenNumber("--fly-lift");
    fly = ghost.animate(
      [
        // A quick rise (ease-out), then the dive into the corner (ease-in).
        { transform: "none", opacity: 1, easing: easing("--ease-enter") },
        { transform: `translateY(${lift}px)`, opacity: 1, offset: 0.2, easing: easing("--ease-fly") },
        { transform: `translate(${dx}px, ${dy}px) scale(${tokenNumber("--fly-scale")})`, opacity: 0 },
      ],
      { duration: motionMs("--dur-fly"), fill: "forwards" },
    );
    return true;
  }

  /** Drops the flying line at once (end of the hide, or a reopen mid-flight). */
  function endFly(): void {
    const a = fly;
    fly = null;
    a?.cancel();
    box.querySelectorAll(".cap-fly").forEach((n) => n.remove());
    box.classList.remove("is-flying");
  }

  /**
   * Save and Esc end the draft: fade out, hide, clear. The pending timer is
   * kept so open() can cancel it if the box is reopened meanwhile.
   */
  function hide(): void {
    voice.cancel();
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
    // Saved already; now the line flies off and the box fades over the
    // flight's last --dur-exit, so the window hides as the line arrives.
    const wait = flyAway() ? Math.max(motionMs("--dur-fly") - motionMs("--dur-exit"), 0) : motionMs("--dur-flash");
    hideTimer = window.setTimeout(hide, wait);
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

  /** Rewrites the priority token in the text and re-previews at once. */
  function applyPriority(p: Priority): void {
    input.value = setPriorityToken(input.value, p);
    showError("");
    showPriority(p); // the parse confirms it a moment later
    window.clearTimeout(timer);
    void runPreview();
  }

  for (const c of prioChips) {
    c.addEventListener("click", (e) => {
      const p = c.dataset.prio as Priority;
      applyPriority(c.getAttribute("aria-pressed") === "true" ? "" : p);
      if (e.detail > 0) {
        // A mouse click goes back to typing; keyboard users stay on the chips.
        input.focus();
        input.setSelectionRange(input.value.length, input.value.length);
      }
    });
  }

  picker.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      void confirmPicker();
    }
  });
  dueSet.addEventListener("click", () => void confirmPicker());

  const voice = mountVoice(root, {
    box,
    input,
    insert(text: string): void {
      input.value = input.value.trim() === "" ? text : `${input.value.trimEnd()} ${text}`;
      input.classList.remove("is-dictated");
      void input.offsetWidth; // restart the fade-in
      input.classList.add("is-dictated");
      showError("");
      input.focus();
      input.setSelectionRange(input.value.length, input.value.length);
      void runPreview();
    },
    showError,
    setListeningLead(on: boolean): void {
      listeningLead = on;
      lead.classList.toggle("is-listening", on);
      paintLead(on ? "mic" : leadIcon);
    },
  });

  document.addEventListener("keydown", (e) => {
    if (e.ctrlKey && !e.altKey && !e.shiftKey && !e.metaKey && e.key.toLowerCase() === "m") {
      e.preventDefault();
      if (!busy && picker.hidden) voice.toggle();
      return;
    }
    if (e.altKey && !e.ctrlKey && !e.shiftKey && !e.metaKey) {
      const digit = /^(?:Digit|Numpad)([0-3])$/.exec(e.code)?.[1];
      if (digit === undefined) return;
      e.preventDefault();
      if (busy || box.dataset.voice === "listening" || box.dataset.voice === "transcribing") return;
      applyPriority(digit === "0" ? "" : CHIPS[Number(digit) - 1].p);
      return;
    }
    if (e.key !== "Escape") return;
    e.preventDefault();
    if (voice.escape()) return;
    if (!picker.hidden) closePicker(true);
    else if (!busy) hide();
  });

  // Go hides the window when it loses focus. Keep the draft text so a
  // half-typed thought survives; the box starts invisible on the next show
  // and open() restores it with the caret at the end.
  window.addEventListener("blur", () => {
    box.classList.add("is-hidden");
    closePicker(false);
    voice.cancel(); // never keep the mic open behind a hidden box
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
