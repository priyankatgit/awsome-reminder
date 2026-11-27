// Reply mode of the capture box ("Replies owed"): the reply hotkey opens the
// box with a suggested line, who the reply is owed to and where the message
// came from. capture.ts owns the line, the lead icon and saving; this module
// owns the reply parts of the preview row, the person editor, the reply hint
// row and the macOS Accessibility note.
import { api, type Preview, type ReplyMeta, type ReplyPrefill, type Settings } from "./api";
import { el } from "./dom";
import { pop } from "./motion";

/** What is saved with a reply besides its line. */
export interface ReplyState {
  owedTo: string;
  source: string;
  quote: string;
}

/** internal/scheduler firstNudge. */
const FIRST_NUDGE_MS = 2 * 60 * 60 * 1000;
const DAY_MS = 24 * 60 * 60 * 1000;
/** Settings.workDays names, indexed like Date.getDay(). */
const DAY_NAMES = ["sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"];
const SHORT_DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** Settings until the real ones arrive (the Go defaults). */
const DEFAULT_RULES: Pick<Settings, "defaultTime" | "workDays"> = {
  defaultTime: "09:00",
  workDays: ["monday", "tuesday", "wednesday", "thursday", "friday"],
};

const ACCESS_NOTE =
  "To grab the selected text, allow Awesome Reminder in System Settings › Privacy & Security › Accessibility. Using the clipboard for now.";

/** "9:05": 24-hour clock without a leading zero on the hour. */
function clockLabel(h: number, m: number): string {
  return `${h}:${String(m).padStart(2, "0")}`;
}

/** "HH:MM" as a label; unparsable is 09:00, as in the scheduler. */
function defaultClock(hhmm: string): string {
  const m = /^(\d{1,2}):(\d{2})$/.exec(hhmm.trim());
  if (!m || Number(m[1]) > 23 || Number(m[2]) > 59) return "9:00";
  return clockLabel(Number(m[1]), Number(m[2]));
}

function startOfDay(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate());
}

/** Calendar days from a to b (local dates; DST-safe by rounding). */
function daysBetween(a: Date, b: Date): number {
  return Math.round((startOfDay(b).getTime() - startOfDay(a).getTime()) / DAY_MS);
}

/** "17:00" today, "Thu 17:00" within the week, else "20 Dec 17:00". */
function dueLabel(created: Date, due: Date): string {
  const t = clockLabel(due.getHours(), due.getMinutes());
  const n = daysBetween(created, due);
  if (n === 0) return t;
  if (n > 0 && n < 7) return `${SHORT_DAYS[due.getDay()]} ${t}`;
  return `${due.getDate()} ${MONTHS[due.getMonth()]} ${t}`;
}

/**
 * When a reply saved at `created` will nudge, as the scheduler plans it
 * (internal/scheduler nudges): without a due time, 2 hours later on any day,
 * then the next working morning at the default time ("then 9:00" when that is
 * tomorrow, else "then Mon 9:00"); with one, the item's alerts, then the
 * working-day nudges.
 */
export function nudgeSummary(created: Date, settings: Pick<Settings, "defaultTime" | "workDays">, due: Date | null): string {
  const work = new Set((settings.workDays ?? []).map((d) => d.toLowerCase()));
  const at = defaultClock(settings.defaultTime);
  if (due) {
    const head = `alerts before ${dueLabel(created, due)}`;
    return work.size > 0 ? `${head}, then daily ${at}` : head;
  }
  const first = startOfDay(new Date(created.getTime() + FIRST_NUDGE_MS));
  for (let i = 1; i <= 7; i++) {
    const d = new Date(first.getFullYear(), first.getMonth(), first.getDate() + i);
    if (!work.has(DAY_NAMES[d.getDay()])) continue;
    return daysBetween(created, d) === 1 ? `nudge in 2h, then ${at}` : `nudge in 2h, then ${SHORT_DAYS[d.getDay()]} ${at}`;
  }
  return "nudge in 2h";
}

/** The person a reply is owed to, as shown; nobody is "Someone". */
export function personLabel(owedTo: string): string {
  return owedTo.trim() || "Someone";
}

/** The parts of the capture box reply mode works with. */
export interface ReplyHost {
  /** #capture: carries data-mode="reply" while active. */
  box: HTMLElement;
  /** The line input. */
  input: HTMLInputElement;
  /** The preview row's first line: the reply parts go first in it. */
  line: HTMLElement;
  /** The input row: the Accessibility note follows it. */
  field: HTMLElement;
  /** The normal hint row: the reply hint row follows it. */
  hint: HTMLElement;
  /** Whether Alt+P may open the person editor now (not saving, recording or picking a time). */
  canEditPerson(): boolean;
}

export interface ReplyMode {
  readonly active: boolean;
  /** Enters reply mode with what the hotkey found (the caller sets the line). */
  enter(p: ReplyPrefill): void;
  /** Back to normal mode; a no-op when not active. */
  exit(): void;
  /** What captureReply saves besides the line. */
  meta(): ReplyMeta;
  /** Updates the reply parts of the preview for the latest parse. */
  render(p: Preview | null): void;
  /** Opens the person editor and focuses it. */
  editPerson(): void;
  /** Handles Alt+P; true when the key was taken. */
  handleKey(e: KeyboardEvent): boolean;
}

export function mountReplyMode(host: ReplyHost): ReplyMode {
  const { box, input } = host;
  const normalPlaceholder = input.placeholder;
  const normalDescribedBy = input.getAttribute("aria-describedby") ?? "";

  // Preview parts, kept across renders so the person editor keeps its focus
  // while the parse preview refreshes: [Reply owed] for [Sam] · from Teams · nudge…
  const parts = el("span", "cap-reply");
  parts.hidden = true;
  const chip = el("span", "chip chip-reply", "Reply owed");
  const forWord = el("span", "cap-reply-for", "for ");
  const person = el("button", "chip chip-person");
  person.type = "button";
  person.title = "Change person (Alt+P)";
  person.setAttribute("aria-keyshortcuts", "Alt+P");
  const personInput = el("input", "cap-person");
  personInput.type = "text";
  personInput.autocomplete = "off";
  personInput.spellcheck = false;
  personInput.placeholder = "Someone";
  personInput.setAttribute("aria-label", "Reply owed to");
  personInput.hidden = true;
  const metaText = el("span", "cap-sub cap-reply-meta");
  parts.append(chip, forWord, person, personInput, metaText);
  host.line.prepend(parts);

  const note = el("p", "cap-access", ACCESS_NOTE);
  note.id = "cap-access";
  note.hidden = true;
  host.field.after(note);

  const hint = el("footer", "cap-hint cap-hint-reply");
  hint.id = "cap-hint-reply";
  hint.innerHTML =
    `<span><kbd>↵</kbd> save</span>` +
    `<span><kbd>Alt</kbd><span class="keycap-plus">+</span><kbd>P</kbd> person</span>` +
    `<span><kbd>Tab</kbd> time</span>` +
    `<span><kbd>Esc</kbd> cancel</span>`;
  host.hint.after(hint);

  let active = false;
  let state: ReplyState = { owedTo: "", source: "", quote: "" };
  let rules = DEFAULT_RULES;
  let due: Date | null = null;
  let editing = false;
  let run = 0; // drops a settings answer that arrives after exit

  function paint(): void {
    const label = personLabel(state.owedTo);
    person.textContent = label;
    person.setAttribute("aria-label", `Change person, now ${label}`);
    const bits = [state.source.trim() ? `from ${state.source.trim()}` : "", nudgeSummary(new Date(), rules, due)].filter(Boolean);
    metaText.textContent = `· ${bits.join(" · ")}`;
  }

  function closeEditor(commit: boolean): void {
    if (!editing) return;
    editing = false;
    if (commit) state = { ...state, owedTo: personInput.value.trim() };
    personInput.hidden = true;
    person.hidden = false;
    paint();
  }

  function backToLine(): void {
    input.focus();
    input.setSelectionRange(input.value.length, input.value.length);
  }

  function editPerson(): void {
    if (!active) return;
    if (!editing) {
      editing = true;
      personInput.value = state.owedTo;
      person.hidden = true;
      personInput.hidden = false;
    }
    personInput.focus();
    personInput.select();
  }

  personInput.addEventListener("keydown", (e) => {
    if (e.isComposing) return;
    if (e.key === "Enter" || e.key === "Escape") {
      // Esc here only leaves the editor (and drops the edit); the box stays.
      e.preventDefault();
      e.stopPropagation();
      closeEditor(e.key === "Enter");
      backToLine();
    }
  });
  personInput.addEventListener("blur", () => closeEditor(true));
  person.addEventListener("click", () => editPerson());

  function enter(p: ReplyPrefill): void {
    const was = active;
    active = true;
    closeEditor(false);
    state = { owedTo: p.owedTo.trim(), source: p.source.trim(), quote: p.quote };
    due = null;
    box.dataset.mode = "reply";
    parts.hidden = false;
    note.hidden = !p.needsAccess || p.keepText;
    input.placeholder = "Reply to Sam: send the figures";
    input.setAttribute("aria-describedby", normalDescribedBy.replace("cap-hint", "cap-hint-reply") + (note.hidden ? "" : " cap-access"));
    paint();
    if (!was) pop(chip, "--chip-pop-scale", "--dur-chip");
    const my = ++run;
    api
      .getSettings()
      .then((s) => {
        if (my !== run || !active) return;
        rules = { defaultTime: s.defaultTime, workDays: s.workDays ?? [] };
        paint();
      })
      .catch(() => undefined); // keep the defaults
  }

  function exit(): void {
    if (!active) return;
    run++;
    closeEditor(false);
    active = false;
    state = { owedTo: "", source: "", quote: "" };
    due = null;
    delete box.dataset.mode;
    parts.hidden = true;
    note.hidden = true;
    input.placeholder = normalPlaceholder;
    input.setAttribute("aria-describedby", normalDescribedBy);
  }

  return {
    get active() {
      return active;
    },
    enter,
    exit,
    meta(): ReplyMeta {
      closeEditor(true); // a save while the editor is open keeps what was typed
      return { owedTo: state.owedTo, source: state.source, quote: state.quote };
    },
    render(p: Preview | null): void {
      if (!active) return;
      due = p?.dueAt ? new Date(p.dueAt) : null;
      paint();
    },
    editPerson,
    handleKey(e: KeyboardEvent): boolean {
      if (!active || !e.altKey || e.ctrlKey || e.shiftKey || e.metaKey || e.code !== "KeyP") return false;
      e.preventDefault(); // macOS Option+P would type "π"
      if (host.canEditPerson()) editPerson();
      return true;
    },
  };
}
