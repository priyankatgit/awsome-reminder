// The detail panel's field controls: title, priority, due, repeat and tags.
// Each builder returns its row element, a fill(item) that shows an item, and
// setError for the inline per-field message. Saving is the caller's job; the
// builders only report what the user picked.
import type { Item } from "../api";
import { el, tagChip } from "../dom";
import { icons } from "../icons";
import { priorityLabel, type Priority } from "../priority";

export type Repeat = "none" | "daily" | "weekdays" | "weekly";

const WEEKDAYS_RULE = "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR";
const SINGLE_WEEKLY = /^FREQ=WEEKLY;BYDAY=(SU|MO|TU|WE|TH|FR|SA)$/;

/** The Repeat choice an RRULE corresponds to; "custom" for typed rules the select can't express. */
export function repeatOf(rrule: string): Repeat | "custom" {
  if (rrule === "") return "none";
  if (rrule === "FREQ=DAILY") return "daily";
  if (rrule === WEEKDAYS_RULE) return "weekdays";
  if (SINGLE_WEEKLY.test(rrule)) return "weekly";
  return "custom";
}

const pad = (n: number) => String(n).padStart(2, "0");

/** An ISO time as a datetime-local value in local time ("" for none). */
export function toLocalInput(iso: string | null): string {
  if (!iso) return "";
  const d = new Date(iso);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** A complete datetime-local value as a local Date; null while incomplete. */
export function fromLocalInput(v: string): Date | null {
  const m = /^(\d{4,})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(v);
  if (!m) return null;
  const d = new Date(+m[1], +m[2] - 1, +m[3], +m[4], +m[5]);
  return Number.isNaN(d.getTime()) ? null : d;
}

const weekdayLong = new Intl.DateTimeFormat("en-GB", { weekday: "long" });
const snoozeFmt = new Intl.DateTimeFormat("en-GB", { weekday: "short", hour: "2-digit", minute: "2-digit", hourCycle: "h23" });

let seq = 0;

/** A labelled grid row with an inline error line under the control. */
function fieldRow(label: string, forControl?: string) {
  const id = `pf-${++seq}`;
  const root = el("div", "pf");
  const lab = forControl ? el("label", "pf-label", label) : el("span", "pf-label", label);
  lab.id = `${id}-label`;
  if (lab instanceof HTMLLabelElement && forControl) lab.htmlFor = forControl;
  const body = el("div", "pf-body");
  const error = el("p", "field-error pf-error");
  error.id = `${id}-error`;
  error.setAttribute("role", "alert");
  error.hidden = true;
  root.append(lab, body, error);
  return {
    root,
    body,
    label: lab,
    labelId: lab.id,
    errorId: error.id,
    setError(msg: string, control?: HTMLElement): void {
      error.textContent = msg;
      error.hidden = msg === "";
      if (control) {
        control.setAttribute("aria-invalid", String(msg !== ""));
        if (msg) control.setAttribute("aria-describedby", error.id);
        else control.removeAttribute("aria-describedby");
      }
    },
  };
}

/** Whether focus is inside node: the user is editing it, so refreshes leave it alone. */
export function isEditing(node: HTMLElement): boolean {
  return node.contains(document.activeElement);
}

export function titleField(onInput: (text: string) => void) {
  const input = el("input", "input pf-title");
  input.type = "text";
  input.id = `pf-title-${++seq}`;
  input.autocomplete = "off";
  input.spellcheck = false;
  const row = fieldRow("Title", input.id);
  row.root.classList.add("pf-wide");
  row.label.classList.add("sr-only");
  row.body.append(input);
  input.addEventListener("input", () => onInput(input.value));
  return {
    root: row.root,
    input,
    fill(it: Item, force: boolean): void {
      if (force || !isEditing(input)) input.value = it.text;
    },
    setError: (msg: string) => row.setError(msg, input),
  };
}

const PRIORITIES: { value: Priority; short: string }[] = [
  { value: "high", short: "High" },
  { value: "medium", short: "Med" },
  { value: "low", short: "Low" },
  { value: "", short: "None" },
];

/** A radio group (High)(Med)(Low)(None); arrows move and pick, like native radios. */
export function priorityField(onPick: (p: Priority) => void) {
  const row = fieldRow("Priority");
  const group = el("div", "seg");
  group.setAttribute("role", "radiogroup");
  group.setAttribute("aria-labelledby", row.labelId);
  const buttons = PRIORITIES.map(({ value, short }) => {
    const b = el("button", "seg-btn");
    b.type = "button";
    b.setAttribute("role", "radio");
    b.dataset.prio = value || "none";
    b.setAttribute("aria-label", priorityLabel(value));
    if (value) b.append(el("span", "prio-dot"));
    b.append(el("span", undefined, short));
    b.addEventListener("click", () => {
      if (b.getAttribute("aria-checked") !== "true") onPick(value);
    });
    group.append(b);
    return b;
  });
  group.addEventListener("keydown", (e) => {
    const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
    if (!step) return;
    e.preventDefault();
    e.stopPropagation(); // not the list's row navigation
    const i = buttons.indexOf(document.activeElement as HTMLButtonElement);
    const next = buttons[(Math.max(i, 0) + step + buttons.length) % buttons.length];
    next.focus();
    next.click();
  });
  row.body.append(group);
  function set(p: string): void {
    for (const [i, b] of buttons.entries()) {
      const on = PRIORITIES[i].value === p;
      b.setAttribute("aria-checked", String(on));
      b.tabIndex = on ? 0 : -1;
    }
  }
  return {
    root: row.root,
    fill: (it: Item) => set(it.priority ?? ""),
    set,
    setError: (msg: string) => row.setError(msg, group),
  };
}

/** A datetime-local plus a clear button; a cleared due makes the item a to-do. */
export function dueField(onSet: (due: Date | null) => void) {
  const input = el("input", "input input-time pf-due");
  input.type = "datetime-local";
  input.id = `pf-due-${++seq}`;
  const row = fieldRow("Due", input.id);
  const clear = el("button", "icon-btn pf-clear");
  clear.type = "button";
  clear.setAttribute("aria-label", "Clear due time");
  clear.title = "Clear due time (makes it a to-do)";
  clear.innerHTML = icons.close;
  const note = el("span", "pf-note");
  row.body.append(input, clear, note);
  let shown = "";
  input.addEventListener("change", () => {
    const d = fromLocalInput(input.value);
    if (d) onSet(d); // an incomplete value waits; the clear button is the way to remove it
  });
  input.addEventListener("blur", () => {
    if (!fromLocalInput(input.value)) input.value = shown; // drop a half-typed value
  });
  clear.addEventListener("click", () => {
    onSet(null);
    input.focus();
  });
  return {
    root: row.root,
    fill(it: Item, force: boolean): void {
      shown = toLocalInput(it.dueAt);
      if (force || !isEditing(input)) input.value = shown;
      clear.hidden = !it.dueAt;
      note.textContent = !it.dueAt
        ? "To-do · set a time to make it a reminder"
        : it.snoozeUntil
          ? `Snoozed until ${snoozeFmt.format(new Date(it.snoozeUntil))}`
          : "";
    },
    setError: (msg: string) => row.setError(msg, input),
  };
}

/** The Repeat select; disabled until the item has a due time. */
export function repeatField(onSet: (r: Repeat) => void) {
  const select = el("select", "input pf-repeat");
  select.id = `pf-repeat-${++seq}`;
  const row = fieldRow("Repeat", select.id);
  const opt = (value: string, label: string) => {
    const o = el("option", undefined, label);
    o.value = value;
    return o;
  };
  const weekly = opt("weekly", "Weekly");
  const custom = opt("custom", "Custom (as typed)");
  custom.disabled = true;
  select.append(opt("none", "None"), opt("daily", "Daily"), opt("weekdays", "Weekdays"), weekly, custom);
  const note = el("span", "pf-note");
  row.body.append(select, note);
  select.addEventListener("change", () => {
    if (select.value !== "custom") onSet(select.value as Repeat);
  });
  return {
    root: row.root,
    fill(it: Item): void {
      const r = repeatOf(it.rrule ?? "");
      custom.hidden = r !== "custom";
      select.value = r;
      select.disabled = !it.dueAt;
      weekly.textContent = it.dueAt ? `Weekly on ${weekdayLong.format(new Date(it.dueAt))}` : "Weekly";
      note.textContent = it.dueAt ? "" : "Set a due time first";
    },
    setError: (msg: string) => row.setError(msg, select),
  };
}

/**
 * Tag chips with remove buttons and an "add" input (Enter, comma or space
 * adds; Backspace on an empty input removes the last tag). onSet resolves
 * whether the new list was saved; the input keeps its text when it wasn't.
 */
export function tagsField(onSet: (tags: string[]) => Promise<boolean>) {
  const input = el("input", "pf-tag-add");
  input.type = "text";
  input.id = `pf-tags-${++seq}`;
  input.autocomplete = "off";
  input.spellcheck = false;
  input.placeholder = "+ tag";
  const row = fieldRow("Tags", input.id);
  const box = el("div", "pf-tags");
  const chips = el("span", "pf-tag-chips");
  box.append(chips, input);
  row.body.append(box);
  let tags: string[] = [];

  const real = () => tags.filter((t) => t !== "inbox");
  async function add(): Promise<void> {
    const t = input.value.trim().replace(/^#/, "").toLowerCase();
    if (!t) return;
    if (real().includes(t)) {
      input.value = "";
      return;
    }
    if (await onSet([...real(), t])) input.value = "";
  }

  input.addEventListener("keydown", (e) => {
    if (e.isComposing) return;
    if (e.key === "Enter" || e.key === "," || e.key === " ") {
      e.preventDefault();
      void add();
    } else if (e.key === "Backspace" && input.value === "" && real().length) {
      e.preventDefault();
      void onSet(real().slice(0, -1));
    } else if (e.key === "Escape" && input.value !== "") {
      e.preventDefault();
      e.stopPropagation(); // clears the text; a second Esc closes the panel
      input.value = "";
      row.setError("", input);
    }
  });
  input.addEventListener("input", () => row.setError("", input));
  input.addEventListener("blur", () => void add());
  box.addEventListener("click", (e) => {
    if (e.target === box) input.focus();
  });

  return {
    root: row.root,
    fill(it: Item): void {
      tags = it.tags ?? [];
      const nodes = tags.map((t) => {
        const c = el("span", "pf-tag");
        c.append(tagChip(t));
        if (t === "inbox" && tags.length === 1) return c; // the implicit tag: nothing to remove
        const x = el("button", "pf-tag-remove");
        x.type = "button";
        x.setAttribute("aria-label", `Remove tag #${t}`);
        x.innerHTML = icons.close;
        x.addEventListener("click", () => {
          void onSet(real().filter((r) => r !== t));
          input.focus();
        });
        c.append(x);
        return c;
      });
      chips.replaceChildren(...nodes);
    },
    setError: (msg: string) => row.setError(msg, input),
  };
}
