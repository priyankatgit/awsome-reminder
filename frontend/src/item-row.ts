// One row of the list window: checkbox, time, text, tag chips and the ⋯
// button. Rows are keyed by section + item ID; list.ts reuses the element
// and only refills it when its signature changes.
import type { Item } from "./api";
import { el, tagChip } from "./dom";
import { icons } from "./icons";

/** A row as the list renders it. */
export interface RowModel {
  key: string; // `${sectionKey}|${item.id}`
  item: Item;
  /** The tag of the section holding the row; its chip is left out. */
  sectionTag?: string;
}

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/** When an item next demands attention: its snooze time, else its due time. */
export function effectiveDue(it: Item): Date | null {
  const s = it.snoozeUntil ?? it.dueAt;
  return s ? new Date(s) : null;
}

export function isDone(it: Item): boolean {
  return it.status === "done";
}

export function isReminder(it: Item): boolean {
  return it.kind === "reminder" && it.dueAt !== null;
}

function pad(n: number): string {
  return String(n).padStart(2, "0");
}

function clock(d: Date): string {
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function startOfDay(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}

const weekday = new Intl.DateTimeFormat("en-GB", { weekday: "short" });
const dayMonth = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "short" });
const full = new Intl.DateTimeFormat("en-GB", {
  weekday: "short",
  day: "numeric",
  month: "short",
  year: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
});

/** "09:30" today, "Tue 17:00" within the next six days, else "27 Nov 17:00". */
export function timeLabel(d: Date, now: Date): string {
  const days = Math.round((startOfDay(d) - startOfDay(now)) / DAY);
  if (days === 0) return clock(d);
  if (days > 0 && days < 7) return `${weekday.format(d)} ${clock(d)}`;
  return `${dayMonth.format(d)} ${clock(d)}`;
}

/** "5m", "2h", "3d": how long ago d was (at least 1m). */
export function agoLabel(d: Date, now: Date): string {
  const ms = Math.max(now.getTime() - d.getTime(), MINUTE);
  if (ms < HOUR) return `${Math.floor(ms / MINUTE)}m`;
  if (ms < DAY) return `${Math.floor(ms / HOUR)}h`;
  return `${Math.floor(ms / DAY)}d`;
}

/** Whether an open reminder is past its effective time. */
export function isOverdue(it: Item, now: Date): boolean {
  const d = effectiveDue(it);
  return !isDone(it) && isReminder(it) && d !== null && d.getTime() < now.getTime();
}

/** Everything a row's content depends on; equal signatures need no refill. */
export function rowSignature(m: RowModel, now: Date): string {
  const it = m.item;
  const d = effectiveDue(it);
  return JSON.stringify([
    it.text,
    it.tags,
    it.kind,
    it.status,
    it.rrule,
    it.dueAt,
    it.snoozeUntil,
    m.sectionTag ?? "",
    d && isReminder(it) ? timeLabel(d, now) : "",
    isOverdue(it, now) && d ? agoLabel(d, now) : "",
  ]);
}

/** Creates an empty row element; fillRow renders its content. */
export function createRow(m: RowModel): HTMLElement {
  const row = el("div", "row");
  row.setAttribute("role", "listitem");
  row.tabIndex = -1;
  row.dataset.key = m.key;
  row.dataset.id = m.item.id;
  return row;
}

/** (Re)renders the row's content from m. */
export function fillRow(row: HTMLElement, m: RowModel, now: Date): void {
  const it = m.item;
  const done = isDone(it);
  const reminder = isReminder(it);
  const overdue = isOverdue(it, now);
  const due = effectiveDue(it);

  row.dataset.id = it.id;
  row.classList.toggle("is-done", done);
  row.classList.toggle("is-overdue", overdue);
  row.classList.remove("is-editing", "is-completing");

  const check = el("button", "row-check");
  check.type = "button";
  check.tabIndex = -1;
  check.setAttribute("role", "checkbox");
  check.setAttribute("aria-checked", String(done));
  check.setAttribute("aria-label", done ? `Reopen: ${it.text}` : `Mark done: ${it.text}`);
  check.dataset.shape = reminder ? "round" : "square";
  const box = el("span", "check-box");
  box.innerHTML = icons.check;
  check.append(box);

  const parts: HTMLElement[] = [check];

  if (reminder && due && !done) {
    const t = el("time", "row-time", timeLabel(due, now));
    t.dateTime = due.toISOString();
    t.title = full.format(due);
    parts.push(t);
  }

  const main = el("span", "row-main");
  if (overdue && due) {
    main.append(el("span", "row-overdue", `overdue ${agoLabel(due, now)} · `));
  } else if (it.snoozeUntil && !done) {
    main.append(el("span", "row-note", "snoozed · "));
  }
  const text = el("span", "row-text", it.text);
  text.title = it.text;
  main.append(text);
  parts.push(main);

  if (it.rrule && !done) {
    const rep = el("span", "row-repeat");
    rep.title = "Repeats";
    rep.innerHTML = icons.repeat;
    rep.append(el("span", "sr-only", "repeats"));
    parts.push(rep);
  }

  const tags = el("span", "row-tags");
  for (const t of it.tags ?? []) {
    if (t !== m.sectionTag) tags.append(tagChip(t));
  }
  parts.push(tags);

  const more = el("button", "row-more");
  more.type = "button";
  more.tabIndex = -1;
  more.setAttribute("aria-label", `Actions for ${it.text}`);
  more.setAttribute("aria-haspopup", "menu");
  more.setAttribute("aria-expanded", "false");
  more.innerHTML = icons.more;
  parts.push(more);

  row.replaceChildren(...parts);
}

/**
 * Swaps the row's text and tags for an inline input holding value. Returns
 * the input and a setter for the inline error line.
 */
export function editRow(row: HTMLElement, value: string): { input: HTMLInputElement; setError: (msg: string) => void } {
  row.classList.add("is-editing");
  const input = el("input", "row-edit");
  input.type = "text";
  input.value = value;
  input.autocomplete = "off";
  input.spellcheck = false;
  input.setAttribute("aria-label", "Edit item");
  const error = el("span", "row-error");
  error.setAttribute("role", "alert");
  error.hidden = true;
  const main = row.querySelector(".row-main");
  row.querySelectorAll(".row-time, .row-repeat, .row-tags").forEach((n) => n.remove());
  if (main) main.replaceWith(input);
  else row.append(input);
  row.append(error); // wraps onto its own line below the input
  return {
    input,
    setError: (msg: string) => {
      error.textContent = msg;
      error.hidden = msg === "";
      input.setAttribute("aria-invalid", String(msg !== ""));
    },
  };
}
