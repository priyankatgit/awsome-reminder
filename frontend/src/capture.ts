// Capture box (#capture): one-line input with a live parse preview.
// Layout and behaviour follow the "Capture box" section of the UI guide.
import { api, errorText, events, on, type Item, type Preview, type ReplyPrefill, type WakeStatus } from "./api";
import { el, motionMs, tagChip } from "./dom";
import { icons, type IconName } from "./icons";
import { KeyGuard } from "./key-guard";
import { canAnimate, easing, pop, ring, tokenNumber } from "./motion";
import { priorityLabel, setPriorityToken, type Priority } from "./priority";
import { mountReplyMode } from "./reply-mode";
import { placePaste, splitPasted } from "./multi-task";
import { ReviewList, saveSequential } from "./review-list";
import { mountVoice, playChime } from "./voice";
import type { SessionMode } from "./voice-segment";
import type { SessionEnd } from "./voice-session";

const PREVIEW_DEBOUNCE_MS = 80;
const NOTE_MS = 5000; // the Saved/Discarded note with Undo
const CONFIRM_MS = 3000; // the discard confirm after the first Esc
const SESSION_TEXT_CHARS = 200; // recent dictation passed as the whisper prompt
// A window focus this soon after a wake show is the OS focusing the shown
// box (where it cannot show without focus), not the user engaging.
const WAKE_FOCUS_GRACE_MS = 500;
// A wake-opened box that cannot listen (or ended with an error or a note)
// keeps the message up this long, then hides by itself.
const WAKE_ERROR_MS = 4000;

const tasks = (n: number): string => `${n} ${n === 1 ? "task" : "tasks"}`;

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
    <div class="cap-review" id="cap-review" hidden></div>
    <div class="cap-field">
      <span class="cap-lead" id="cap-lead" data-icon="search"></span>
      <input class="cap-input" id="cap-input" type="text" autocomplete="off" spellcheck="false"
             aria-label="To-do or reminder" aria-describedby="cap-preview cap-hint"
             placeholder="remind me tomorrow 5pm check deploy #backend" />
      <span class="cap-dots cap-busy" id="cap-busy" aria-hidden="true" hidden><i></i><i></i><i></i></span>
      <div class="cap-action-slot">
        <button class="mic-btn" id="cap-mic" type="button" aria-label="Start dictation" aria-pressed="false"
                title="Dictate (Ctrl+M)"><span class="mic-ring" aria-hidden="true"></span><span class="mic-icon" id="cap-mic-icon"></span></button>
      </div>
      <span class="cap-flash" id="cap-flash" aria-hidden="true"></span>
    </div>
    <p class="cap-error" id="cap-error" role="alert" hidden></p>
    <div class="cap-note" id="cap-note" tabindex="-1" role="status" hidden>
      <span class="cap-note-text" id="cap-note-text"></span>
      <button class="btn btn-ghost cap-note-undo" id="cap-note-undo" type="button" aria-keyshortcuts="Control+Z"></button>
    </div>
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
      <span class="cap-hint-status" id="cap-hint-status" role="status" aria-live="polite" hidden></span>
      <span><kbd>↵</kbd> <span id="cap-hint-save">save</span></span>
      <span><kbd>Shift</kbd><span class="keycap-plus">+</span><kbd>↵</kbd> add another</span>
      <span><kbd>Ctrl</kbd><span class="keycap-plus">+</span><kbd>↵</kbd> save &amp; open list</span>
      <span><kbd>Tab</kbd> time</span>
      <span><kbd>Ctrl</kbd><span class="keycap-plus">+</span><kbd>M</kbd> dictate</span>
      <span><kbd>Esc</kbd> close</span>
    </footer>
    <footer class="cap-hint cap-hint-voice">
      <span class="cap-listen" id="cap-listen" hidden>
        <span class="cap-listen-dot" aria-hidden="true"></span>
        <span class="cap-listen-label" id="cap-listen-label">Listening…</span>
        <canvas class="cap-wave" id="cap-wave" aria-hidden="true"></canvas>
        <span class="cap-timer" id="cap-timer">0:00</span>
      </span>
      <span class="cap-wake-hint" id="cap-wake-hint" hidden></span>
      <span class="cap-voice-multi">“new task” next</span>
      <span class="cap-voice-multi">“save all” done</span>
      <span class="cap-voice-single">auto-stops after a pause</span>
      <span class="cap-voice-save" id="cap-voice-save" hidden><kbd>↵</kbd> <span id="cap-voice-save-text"></span></span>
      <span><kbd>Esc</kbd> stop</span>
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
  const reviewBox = root.querySelector<HTMLElement>("#cap-review")!;
  const note = root.querySelector<HTMLElement>("#cap-note")!;
  const noteText = root.querySelector<HTMLElement>("#cap-note-text")!;
  const noteUndo = root.querySelector<HTMLButtonElement>("#cap-note-undo")!;
  const voiceLive = root.querySelector<HTMLElement>("#cap-voice-live")!;
  const hintStatus = root.querySelector<HTMLElement>("#cap-hint-status")!;
  const hintSave = root.querySelector<HTMLElement>("#cap-hint-save")!;
  const voiceSave = root.querySelector<HTMLElement>("#cap-voice-save")!;
  const voiceSaveText = root.querySelector<HTMLElement>("#cap-voice-save-text")!;
  const wakeHint = root.querySelector<HTMLElement>("#cap-wake-hint")!;
  noteUndo.innerHTML = `${icons.undo}<span>Undo</span>`;

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

  // Reply mode (the reply hotkey): the reply parts of the preview, the person
  // editor and the reply hint row live in reply-mode.ts.
  const reply = mountReplyMode({
    box,
    input,
    line,
    field: root.querySelector<HTMLElement>(".cap-field")!,
    hint: root.querySelector<HTMLElement>("#cap-hint")!,
    canEditPerson: () => !busy && picker.hidden && box.dataset.voice !== "listening" && box.dataset.voice !== "transcribing",
  });

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

  // Review list (Phase 3): tasks dictated one after another wait here until
  // Enter or a spoken "save all". Not used in reply mode.
  const list = new ReviewList(reviewBox, onListChange, {
    onEdit: editRow,
    onBack: () => input.focus(),
  });
  let confirmTimer: number | undefined; // the discard confirm is showing
  let noteTimer: number | undefined; // the Saved/Discarded note is showing
  let noteAction: (() => void | Promise<void>) | null = null;
  let leftover = false; // the box opened with rows left from a voice session
  let leftoverText = false; // ...and the input holds text from a wake session
  let typedRows = false; // some rows were typed (Shift+Enter, a paste), not dictated

  // Wake mode ("Hey Awesome"): the box shows without focus and the guard
  // keeps stray typing out until the user engages (click, hotkey, Ctrl+M).
  const keyGuard = new KeyGuard();
  let wakeAt = -Infinity; // performance.now() of the last wake show
  let wakeGen = 0; // bumped by every hide: a wake still starting gives up
  let wakeOpened = false; // a wake opened the box since the last hotkey open
  let wakeHideTimer: number | undefined; // a wake-opened box hides after its message
  let wakeChime = true; // WakeStatus.chime, kept current by events.wakeStatus

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

  /** The wake hint: "Click or press <hotkey> to type", keycaps for the hotkey. */
  function renderWakeHint(hotkey: string): void {
    const parts: (Node | string)[] = [hotkey ? "Click or press " : "Click to type"];
    if (hotkey) {
      hotkey.split("+").forEach((k, i) => {
        if (i > 0) parts.push(el("span", "keycap-plus", "+"));
        parts.push(el("kbd", undefined, k));
      });
      parts.push(" to type");
    }
    wakeHint.replaceChildren(...parts);
  }

  function armGuard(): void {
    keyGuard.arm();
    wakeHint.hidden = false;
  }

  function releaseGuard(): void {
    window.clearTimeout(wakeHideTimer); // engagement and every hide cancel it
    wakeHideTimer = undefined;
    keyGuard.release();
    wakeHint.hidden = true;
  }

  /** The user engaged with a wake-opened box: typing and focused rules from now on. */
  function engage(): void {
    if (!keyGuard.armed()) return;
    releaseGuard();
    voice.focusGained();
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
    // In reply mode the lead stays the reply arrow and the nudge summary
    // (reply.render) speaks for any due time.
    restingLead = reply.active ? "reply" : !p ? "search" : p.dueAt ? "bell" : "todo";
    setRemind(!reply.active && Boolean(p?.dueAt), p?.human ?? "");
    reply.render(p);
    if (!p) {
      if (!reply.active) lineBody.append(el("span", "cap-empty", "Type a to-do, or add a time like “tomorrow 5pm”"));
      return;
    }
    const reminder = Boolean(p.dueAt);
    if (!reminder && !reply.active) lineBody.append(el("span", "cap-kind", "To-do"));
    for (const t of p.tags ?? []) lineBody.append(tagChip(t));

    if (reply.active) {
      // the nudge summary covers alerts
    } else if (reminder && p.alerts && p.alerts.length > 0) {
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
    if (!keepDraft) {
      // The review list is never cleared here: reply paths must not destroy
      // rows. Only a normal save, a confirmed Esc and a spoken cancel clear it.
      input.value = "";
      reply.exit();
    }
    clearConfirm();
    hideNote();
    leftover = false;
    leftoverText = false;
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
    updateReview();
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
    ghost.scrollLeft = input.scrollLeft; // a long line shows the same part the input did
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
    wakeGen++;
    releaseGuard();
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

  /** The hotkey (and a wake, with focus false: the input is left unfocused). */
  function open(focus = true): void {
    window.clearTimeout(hideTimer); // a save/Esc hide still pending must not hide the new box
    // Reopening a box a wake opened: whatever it left (rows, or text alone,
    // even text that landed after it hid) counts as "from voice".
    const fromWake = wakeOpened && box.classList.contains("is-hidden");
    wakeOpened = false;
    const live = box.dataset.voice === "listening" || box.dataset.voice === "transcribing";
    // Keep a draft left by blur; drop one already saved or dismissed, and a
    // reply draft (the capture hotkey always opens a normal box).
    reset(!endDraft && !reply.active);
    endDraft = false;
    box.classList.remove("is-hidden", "is-entering");
    void box.offsetWidth; // restart the enter animation
    box.classList.add("is-entering");
    if (focus) {
      input.focus();
      input.setSelectionRange(input.value.length, input.value.length);
    }
    // Text and rows a session left behind; not the rows of one still running.
    leftoverText = !live && fromWake && !reply.active && input.value.trim() !== "";
    leftover = !live && !reply.active && (list.items().length > 0 || leftoverText);
    updateReview();
    if (input.value.trim() !== "") void runPreview();
  }

  // ---- review list ----

  /** How many tasks Enter would save: the rows plus a non-empty input. */
  function pending(): number {
    return list.items().length + (input.value.trim() !== "" ? 1 : 0);
  }

  /** Shows or hides the list, and keeps the hint row in step with it. */
  function updateReview(): void {
    const rows = reply.active ? 0 : list.items().length;
    reviewBox.hidden = rows === 0;
    const kept = rows + (leftoverText && input.value.trim() !== "" ? 1 : 0); // left by a session
    if (kept === 0) leftover = false;
    const n = reply.active ? 0 : pending();
    hintSave.textContent = n > 1 ? `save ${n}` : "save";
    // The listening hint row shows it too, only when Enter would save several.
    voiceSave.hidden = n <= 1;
    voiceSaveText.textContent = n > 1 ? `save ${n}` : "";
    if (confirmTimer !== undefined && rows > 0) {
      hintStatus.textContent = `Discard ${tasks(n)}? Esc again to discard · ↵ to save`;
      hintStatus.hidden = false;
    } else if (leftover && kept > 0) {
      hintStatus.textContent = `${kept} unsaved ${kept === 1 ? "task" : "tasks"}${typedRows ? "" : " from voice"}`;
      hintStatus.hidden = false;
    } else {
      hintStatus.hidden = true;
      hintStatus.textContent = "";
    }
  }

  function onListChange(): void {
    if (list.items().length === 0) {
      clearConfirm();
      typedRows = false;
    }
    updateReview();
    reviewBox.scrollTop = reviewBox.scrollHeight;
  }

  function clearConfirm(): void {
    window.clearTimeout(confirmTimer);
    confirmTimer = undefined;
  }

  /** First Esc with rows: ask; a second within CONFIRM_MS discards. */
  function askDiscard(): void {
    confirmTimer = window.setTimeout(() => {
      confirmTimer = undefined;
      updateReview();
    }, CONFIRM_MS);
    updateReview();
  }

  /** Row edit: its text goes into the input; the typed text takes its place. */
  function editRow(i: number): void {
    const typed = input.value;
    const text = list.takeAt(i);
    if (typed.trim() !== "") {
      const items = list.items();
      items.splice(i, 0, typed);
      list.restore(items);
    }
    input.value = text;
    showError("");
    input.focus();
    input.setSelectionRange(text.length, text.length);
    updateReview();
    void runPreview();
  }

  /**
   * A short status line for NOTE_MS, with an Undo button (Ctrl+Z when
   * focused) when undo is given. Without undo it is only information
   * ("Stopped after 5 minutes") and the box stays open.
   */
  function showNote(text: string, undo?: () => void | Promise<void>): void {
    window.clearTimeout(noteTimer);
    noteAction = undo ?? null;
    noteText.textContent = text;
    noteUndo.hidden = !undo;
    note.hidden = false;
    noteTimer = window.setTimeout(() => {
      hideNote();
      // A spoken save/discard left the box open for the note; close it now
      // unless something new is going on.
      if (undo && !busy && pending() === 0 && box.dataset.voice !== "listening" && box.dataset.voice !== "transcribing") hide();
    }, NOTE_MS);
  }

  function hideNote(): void {
    window.clearTimeout(noteTimer);
    noteTimer = undefined;
    noteAction = null;
    note.hidden = true;
    noteText.textContent = "";
  }

  async function runUndo(): Promise<void> {
    const act = noteAction;
    if (!act) return;
    hideNote();
    window.clearTimeout(hideTimer);
    await act();
    input.focus();
  }

  noteUndo.addEventListener("click", () => void runUndo());

  /**
   * Voice boundary ("new task"): the input becomes a row at the end of the
   * list. Returns the row count, 0 when there was nothing to add (or in
   * reply mode, which has no rows). A showing discard confirm goes away, so
   * a pending Esc-Esc cannot sweep the new row.
   */
  function addTask(): number {
    const text = input.value.trim();
    if (reply.active || text === "") return 0;
    clearConfirm();
    leftover = false;
    leftoverText = false;
    input.value = "";
    list.add(text);
    void runPreview();
    updateReview();
    return list.items().length;
  }

  /**
   * Voice: "scratch that" clears the input, or drops the last row when it is
   * empty (never in reply mode, where the rows are hidden). Returns whether
   * anything was removed.
   */
  function scratch(): boolean {
    if (input.value.trim() !== "") {
      input.value = "";
      void runPreview();
      updateReview();
      return true;
    }
    if (reply.active) return false;
    return list.popLast() !== undefined;
  }

  /** The recent session text for the whisper prompt: rows plus the input. */
  function sessionText(): string {
    const parts = reply.active ? [input.value] : [...list.items(), input.value];
    return parts
      .map((s) => s.trim())
      .filter((s) => s !== "")
      .join(" ")
      .slice(-SESSION_TEXT_CHARS);
  }

  /** Voice "cancel" (spoken) or a confirmed Esc: drops the rows and the input. */
  function cancelAll(spoken: boolean): void {
    if (busy || reply.active) return;
    const rows = list.items();
    const typed = input.value;
    const n = pending();
    if (n === 0) return;
    clearConfirm();
    list.clear();
    input.value = "";
    showError("");
    void runPreview();
    updateReview();
    if (!spoken) {
      hide();
      return;
    }
    showNote(`Discarded ${tasks(n)}`, () => {
      list.restore([...rows, ...list.items()]);
      if (input.value.trim() === "") input.value = typed;
      updateReview();
      void runPreview();
    });
  }

  /** Voice "save all" (spoken: Undo note, box stays) or Enter (flash and hide). */
  async function saveAll(spoken: boolean): Promise<void> {
    await save(spoken);
  }

  /**
   * Enter: while dictating, stop and wait for pending segments first.
   * Ctrl+Enter (openList) also opens the list on the last saved task.
   */
  async function submit(openList = false): Promise<void> {
    if (busy) return;
    if (box.dataset.voice === "listening" || box.dataset.voice === "transcribing") await voice.finish();
    await save(false, openList);
  }

  async function save(spoken = false, openList = false): Promise<void> {
    if (busy) return;
    if (reply.active) {
      if (input.value.trim() === "") return;
      busy = true;
      showError("");
      let saved: Item;
      try {
        saved = await api.captureReply(input.value, reply.meta());
      } catch (err) {
        busy = false;
        const msg = errorText(err);
        showError(msg.includes("empty input") ? "Add some text to save" : msg);
        return;
      }
      finishSave(openList ? saved.id : undefined);
      return;
    }
    const rows = list.items();
    const typed = input.value.trim() !== "";
    if (rows.length === 0 && !typed) return;
    busy = true;
    showError("");
    clearConfirm();
    const texts = typed ? [...rows, input.value] : rows;
    const r = await saveSequential(texts, (t) => api.capture(t));
    if (r.failedAt !== null) {
      busy = false;
      // Saved rows leave the list; the failing row (or the input) shows why.
      const done = Math.min(r.saved.length, rows.length);
      dropSaved(done);
      if (r.failedAt < rows.length) {
        list.setError(r.failedAt - done, r.error);
      } else {
        showError(r.error.includes("empty input") ? "Add some text to save" : r.error);
      }
      if (r.saved.length > 0) {
        const ids = r.saved.map((it) => it.id);
        const savedTexts = texts.slice(0, r.saved.length);
        showNote(`Saved ${tasks(r.saved.length)}`, () => undoSaved(ids, savedTexts));
      }
      updateReview();
      // A spoken save in a wake box nobody engaged with: show why, then hide
      // keeping the unsaved rest as the draft (never left up unattended).
      if (keyGuard.armed()) hideSoon();
      return;
    }
    dropSaved(rows.length);
    // A spoken save, or Enter on a typed batch: keep the box open for the
    // Undo note (a batch is easy to get wrong); it hides when the note ends,
    // unless a new task is started meanwhile. Ctrl+Enter goes to the list,
    // which has its own Undo.
    if (spoken || (texts.length > 1 && !openList)) {
      // Keep the box open for the Undo note; it hides when the note ends.
      busy = false;
      const ids = r.saved.map((it) => it.id);
      input.value = "";
      flash.classList.add("is-on");
      window.setTimeout(() => flash.classList.remove("is-on"), motionMs("--dur-flash"));
      void runPreview();
      updateReview();
      showNote(`Saved ${tasks(ids.length)}`, () => undoSaved(ids, texts));
      return;
    }
    finishSave(openList ? r.saved.at(-1)?.id : undefined);
  }

  /** Removes the saved prefix; rows added while the save ran stay. */
  function dropSaved(n: number): void {
    for (let i = 0; i < n; i++) list.removeAt(0);
  }

  /**
   * Undo of a save: delete what was saved and put those texts back in the
   * list. Stops at the first failed delete; only deleted ones come back.
   */
  async function undoSaved(ids: string[], texts: string[]): Promise<void> {
    let n = 0;
    try {
      for (const id of ids) {
        await api.deleteItem(id);
        n++;
      }
    } catch (err) {
      showError(errorText(err));
    }
    list.restore([...texts.slice(0, n), ...list.items()]);
    endDraft = false;
    updateReview();
  }

  /**
   * Enter's save: flash, fly the typed line away, hide. With openId
   * (Ctrl+Enter) the list then opens on that item.
   */
  function finishSave(openId?: string): void {
    endDraft = true;
    flash.classList.add("is-on");
    window.clearTimeout(hideTimer);
    // Saved already; now the line flies off and the box fades over the
    // flight's last --dur-exit, so the window hides as the line arrives.
    const wait = flyAway() ? Math.max(motionMs("--dur-fly") - motionMs("--dur-exit"), 0) : motionMs("--dur-flash");
    hideTimer = window.setTimeout(() => {
      hide();
      if (openId !== undefined) void api.openList(openId);
    }, wait);
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
    clearConfirm(); // anything else dismisses the discard confirm
    updateReview();
    schedulePreview();
  });

  /**
   * Shift+Enter: the typed line joins the review list and the box stays open
   * for the next one; Enter then saves them all (R9 of the black glossy theme
   * design). Reply mode is one line, so there it saves like Enter.
   */
  function stage(): void {
    if (busy) return;
    const n = addTask();
    if (n > 0) {
      typedRows = true;
      voiceLive.textContent = `Task ${n} added`;
    }
    input.focus();
  }

  input.addEventListener("keydown", (e) => {
    if (e.isComposing) return;
    if (e.key === "Enter" && e.shiftKey && !reply.active) {
      e.preventDefault();
      stage();
    } else if (e.key === "Enter") {
      e.preventDefault();
      void submit(e.ctrlKey || e.metaKey);
    } else if (e.key === "Tab" && !e.shiftKey) {
      e.preventDefault();
      void openPicker();
    } else if (e.key === "ArrowUp" && !reply.active && list.items().length > 0) {
      e.preventDefault();
      list.focusLast();
    }
  });

  // A paste of several lines (a list from notes or chat): every line but the
  // last joins the review list, the last goes into the input. An <input>
  // would otherwise squash the lines into one.
  input.addEventListener("paste", (e) => {
    if (reply.active || busy) return;
    const lines = splitPasted(e.clipboardData?.getData("text/plain") ?? "");
    if (lines.length < 2) return;
    e.preventDefault();
    const start = input.selectionStart ?? input.value.length;
    const end = input.selectionEnd ?? start;
    const placed = placePaste(input.value, start, end, lines);
    clearConfirm();
    leftover = false;
    leftoverText = false;
    if (placed.rows.length > 0) typedRows = true;
    for (const t of placed.rows) list.add(t);
    input.value = placed.input;
    input.setSelectionRange(placed.caret, placed.caret);
    showError("");
    voiceLive.textContent = `${tasks(placed.rows.length)} added`;
    updateReview();
    void runPreview();
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
      // A transcription error shown earlier in the session stays visible;
      // a new session clears it.
      input.value = input.value.trim() === "" ? text : `${input.value.trimEnd()} ${text}`;
      input.classList.remove("is-dictated");
      void input.offsetWidth; // restart the fade-in
      input.classList.add("is-dictated");
      clearConfirm();
      if (!keyGuard.armed()) {
        // A wake-opened box keeps the caret out until the user engages.
        input.focus();
        input.setSelectionRange(input.value.length, input.value.length);
      }
      updateReview();
      void runPreview();
    },
    showError,
    setListeningLead(on: boolean): void {
      listeningLead = on;
      lead.classList.toggle("is-listening", on);
      paintLead(on ? "mic" : leadIcon);
    },
    addTask,
    scratch,
    saveAll,
    cancelAll,
    multi: () => !reply.active,
    sessionText,
    showNote: (text: string) => showNote(text),
    sessionEnded: wakeEnded,
    wakeFailed,
  });

  /**
   * A session ended. One the user never engaged with (still "wake") hides
   * the box and keeps whatever it left as the draft: a quiet wake leaves
   * nothing and hides with no error; text or rows show up on the next open
   * as "N unsaved tasks from voice". A spoken save or cancel of something
   * keeps the box for its Undo note (the save or discard runs right after
   * this), which hides it when it ends. A transcription error with nothing
   * dictated, or the "Stopped after 5 minutes" note, stays WAKE_ERROR_MS.
   */
  function wakeEnded(mode: SessionMode, end: SessionEnd | null): void {
    if (mode !== "wake" || !keyGuard.armed()) return; // engaged, or hidden already
    if ((end === "save" || end === "cancel") && pending() > 0) return;
    if (end === "abort") return; // wakeFailed follows with the mic error
    if (!note.hidden || (pending() === 0 && !errorRow.hidden)) {
      hideSoon();
      return;
    }
    hideKeepingDraft();
  }

  /**
   * A wake that could not start a session: show why (msg, or the error
   * already showing) for WAKE_ERROR_MS, then hide keeping the draft, so the
   * box never stays up unattended (Go suppresses wakes while it shows).
   * Engagement or a hide cancels it.
   */
  function wakeFailed(msg?: string): void {
    if (msg) showError(msg);
    if (keyGuard.armed()) hideSoon();
  }

  function hideSoon(): void {
    window.clearTimeout(wakeHideTimer);
    wakeHideTimer = window.setTimeout(hideKeepingDraft, WAKE_ERROR_MS);
  }

  /** Hides like a blur does: the draft stays for the next open. */
  function hideKeepingDraft(): void {
    wakeGen++;
    releaseGuard();
    voice.cancel();
    closePicker(false);
    box.classList.add("is-hidden");
    window.clearTimeout(hideTimer);
    hideTimer = window.setTimeout(() => void api.hideCapture(), motionMs("--dur-exit"));
  }

  /**
   * "Hey Awesome": Go showed the box without focus. Open it as the hotkey
   * would (a hidden reply draft gives way to a normal box), keep the
   * keyboard out until the user engages, chime, then listen in wake mode.
   */
  async function openForWake(): Promise<void> {
    open(false);
    wakeOpened = true;
    input.blur(); // the page may still have it focused from the last open
    armGuard();
    wakeAt = performance.now();
    const gen = ++wakeGen;
    // Nothing is awaited before the chime: every step here delays the mic
    // (the wake-to-listening budget is 500 ms). The hint row shows only once
    // listening, by when the hotkey text has arrived.
    void api
      .getSettings()
      .then((st) => renderWakeHint(st.hotkey))
      .catch(() => renderWakeHint(""));
    if (wakeChime) {
      try {
        await playChime();
      } catch (err) {
        // The recorder meets the same block and reports it as a mic error.
        console.warn("wake chime:", err);
      }
      if (gen !== wakeGen) return;
    }
    voice.startSession(keyGuard.armed() ? "wake" : "focused");
  }

  /** The caret at the end of the input. */
  function caretToEnd(): void {
    input.focus();
    input.setSelectionRange(input.value.length, input.value.length);
  }

  // A click in a wake-opened box engages it. A click on a bare part of the
  // box (the hint row, the preview) puts the caret in the input, guard or
  // not: a real click activates the window first, so by now the focus
  // handler may already have engaged the box.
  box.addEventListener("mousedown", (e) => {
    engage();
    const target = e.target instanceof Element ? e.target : null;
    if (target?.closest("input, textarea, select, button, label, a, [tabindex]")) return;
    if (document.activeElement === input) return;
    e.preventDefault();
    caretToEnd();
  });

  box.addEventListener("pointerdown", () => {
    if (confirmTimer === undefined) return;
    clearConfirm();
    updateReview();
  });

  // The key guard runs first (capture phase), so a blocked key reaches
  // neither the input's own handler (Enter saves, Tab opens the picker) nor
  // the shortcuts below.
  document.addEventListener(
    "keydown",
    (e) => {
      if (!keyGuard.armed()) return;
      if (e.ctrlKey && !e.altKey && !e.shiftKey && !e.metaKey && e.key.toLowerCase() === "m") {
        // Ctrl+M engages; it does not stop the session the user is joining.
        e.preventDefault();
        e.stopImmediatePropagation();
        engage();
        input.focus();
        return;
      }
      if (keyGuard.allows(e)) return;
      e.preventDefault();
      e.stopImmediatePropagation();
    },
    true,
  );

  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape" && confirmTimer !== undefined) {
      clearConfirm();
      updateReview();
    }
    if (noteAction && e.ctrlKey && !e.altKey && !e.shiftKey && !e.metaKey && e.key.toLowerCase() === "z") {
      // Ctrl+Z undoes the Saved/Discarded note from anywhere in the box,
      // the input included (instead of the input's own text undo).
      e.preventDefault();
      void runUndo();
      return;
    }
    if (reply.handleKey(e)) return; // Alt+P: the person editor
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
    if (keyGuard.armed()) {
      // A wake the user never engaged with: stop listening and hide; what
      // was dictated stays as the draft.
      hideKeepingDraft();
      return;
    }
    if (voice.escape()) return;
    if (!picker.hidden) closePicker(true);
    else if (busy) return;
    else if (!reply.active && list.items().length > 0) {
      // Rows would be lost: confirm first.
      if (confirmTimer !== undefined) cancelAll(false);
      else askDiscard();
    } else hide();
  });

  // Go hides the window when it loses focus. Keep the draft text so a
  // half-typed thought survives; the box starts invisible on the next show
  // and open() restores it with the caret at the end.
  window.addEventListener("blur", () => {
    wakeGen++;
    releaseGuard();
    box.classList.add("is-hidden");
    closePicker(false);
    voice.cancel(); // never keep the mic open behind a hidden box
  });
  window.addEventListener("focus", () => {
    box.classList.remove("is-hidden");
    // Never focuses the input by itself: a focus can come with a wake show,
    // before or after capture:wake, and keys must not land in the draft.
    // The capture:open and reply:open handlers place the caret, and the
    // page restores it when the window takes focus; the box hides on blur,
    // so every re-entry goes through one of them (or a click in the box).
    if (keyGuard.armed() && performance.now() - wakeAt >= WAKE_FOCUS_GRACE_MS) {
      // After the grace period a focus is the user clicking or Alt+Tabbing
      // into a wake-opened box (whose input the wake left unfocused): engage
      // and put the caret in the input, as the hotkey would.
      engage();
      if (picker.hidden) caretToEnd();
    }
  });

  // Size the native window to the content; the top edge stays put.
  new ResizeObserver(() => {
    void api.resizeCapture(Math.ceil(content.getBoundingClientRect().height));
  }).observe(content);

  /**
   * The reply hotkey: a fresh box in reply mode holding the suggested line,
   * or (keepText) the open box switched to reply mode around its own text.
   * With nobody guessed, the caret starts in the person editor.
   */
  function openReply(p: ReplyPrefill): void {
    // The reply hotkey on a wake-opened box engages it, as the capture hotkey does.
    releaseGuard();
    voice.focusGained();
    if (!p.keepText) {
      endDraft = true; // the suggested line replaces any draft
      open();
      reply.enter(p);
      input.value = p.line;
    } else {
      reply.enter(p);
    }
    // Swap the lead to the reply arrow with the bell's pop.
    remindSay.textContent = "";
    restingLead = "reply";
    endRemind();
    play(lead, "is-pop");
    showError("");
    if (!p.keepText && p.owedTo.trim() === "") {
      reply.editPerson();
      // The window may take focus a moment later; end in the editor.
      window.requestAnimationFrame(() => {
        if (reply.editing) reply.editPerson();
      });
    } else if (picker.hidden) {
      input.focus();
      input.setSelectionRange(input.value.length, input.value.length);
    }
    window.clearTimeout(timer);
    void runPreview();
  }

  on(events.captureOpen, () => {
    // The hotkey on a wake-opened box: focused rules, keep listening. Also
    // when an Esc already hid it, so a session still draining does not hide
    // the box the user just opened.
    releaseGuard();
    voice.focusGained();
    open();
  });
  on(events.captureWake, () => void openForWake());
  on<WakeStatus>(events.wakeStatus, (st) => {
    wakeChime = st.chime;
    // The wake word was turned off while a wake box listens unengaged:
    // release the mic and hide, keeping what was dictated.
    if (!st.enabled && keyGuard.armed()) hideKeepingDraft();
  });
  void api.voice
    .wakeStatus()
    .then((st) => {
      wakeChime = st.chime;
    })
    .catch(() => undefined);
  on<ReplyPrefill>(events.replyOpen, openReply);
  renderPreview(null);
  input.focus();
}
