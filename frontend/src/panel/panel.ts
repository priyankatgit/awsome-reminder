// The detail panel: slides in from the right of the list window and edits one
// item's title, priority, due/repeat, tags and description. list.ts imports
// this module the first time a panel opens, so a list that never opens one
// carries none of it.
//
// Title and description autosave through their own debounced autosaver
// (latest value per item wins); priority, due, repeat and tags save at once.
// Saves always go to the item whose data the fields show, so an edit made
// just before a switch lands on the item it was typed into.
import "./panel.css";
import { api, errorText, events, on, type Item, type SnoozePreset } from "../api";
import { el, motionMs } from "../dom";
import { icons } from "../icons";
import { agoLabel, isDone, isReminder } from "../item-row";
import { closeMenu, openMenu } from "../menu";
import { createAutosaver, type Autosaver } from "./autosave";
import { dueField, isEditing, priorityField, repeatField, tagsField, titleField } from "./fields";

const WIDEN_TO = 1100; // px: the list window grows to this when narrower (capped to the screen)
const REFRESH_DEBOUNCE_MS = 50;

export interface PanelHooks {
  /** Deletes the item through the list, which shows its Undo toast. */
  remove(id: string): void;
  /** The panel has closed; the list takes focus back. */
  closed(): void;
}

let hooks: PanelHooks = { remove: () => {}, closed: () => {} };
let openId: string | null = null; // the item the panel is for
let shown: Item | null = null; // the item whose data fills the fields
let loadSeq = 0;
let refreshTimer: number | undefined;
let offChanged: (() => void) | null = null;
let savedSinceOpen = false;
let immediate = 0; // discrete saves in flight
const inflight = new Set<Promise<unknown>>();
type TextKind = "title" | "description";
/** Text saves that failed, keyed `${kind}:${itemId}`, so Retry can resend them. */
const failed = new Map<string, { kind: TextKind; id: string; value: string }>();

/** Failed text saves of the item on show: only these drive its status and Retry. */
function shownFailures() {
  return Array.from(failed.values()).filter((f) => f.id === shown?.id);
}

interface Field {
  setError(msg: string): void;
}
/** Discrete saves in flight per field: a refresh leaves a busy field alone. */
const busy = new Map<Field, number>();
const isBusy = (f: Field) => (busy.get(f) ?? 0) > 0;

function quiet(call: () => Promise<unknown>): void {
  try {
    void call().catch(() => {});
  } catch {
    // no backend (plain-browser preview)
  }
}

function saveMessage(err: unknown): string {
  const msg = errorText(err);
  return msg.includes("empty input") ? "Add some text to save" : msg;
}

/** Wraps a text save: shows its inline error and remembers it for Retry. */
function textSaver(
  kind: TextKind,
  call: (id: string, v: string) => Promise<Item>,
  setError: (msg: string) => void,
): Autosaver<string> {
  const saver = createAutosaver<string>(async (id, value) => {
    try {
      const it = await call(id, value);
      failed.delete(`${kind}:${id}`);
      if (id === shown?.id) setError("");
      applySaved(it);
    } catch (err) {
      failed.set(`${kind}:${id}`, { kind, id, value });
      if (id === shown?.id) setError(saveMessage(err));
      throw err;
    }
  });
  saver.onStatus(renderStatus);
  return saver;
}

// ---------- DOM ----------

const root = el("aside", "panel");
root.hidden = true;
root.setAttribute("aria-labelledby", "panel-heading");

const head = el("header", "panel-head");
const heading = el("h2", "panel-heading", "Details");
heading.id = "panel-heading";
const closeBtn = el("button", "icon-btn");
closeBtn.type = "button";
closeBtn.setAttribute("aria-label", "Close details");
closeBtn.title = "Close (Esc)";
closeBtn.innerHTML = icons.close;
head.append(heading, closeBtn);

const title = titleField((text) => shown && titleSaver.change(shown.id, text));
const priority = priorityField((p) => {
  if (!shown) return;
  priority.set(p); // feels instant; a failure refills from the item
  const id = shown.id;
  void saveNow(priority, () => api.setPriority(id, p));
});
const due = dueField((d) => shown && void saveNow(due, (id) => api.setDue(id, d)));
const repeat = repeatField((r) => shown && void saveNow(repeat, (id) => api.setRepeat(id, r)));
const tags = tagsField((list) => (shown ? saveNow(tags, (id) => api.setTags(id, list)) : Promise.resolve(false)));

/** Task 8 mounts the Markdown editor here; until then a plain textarea. */
export const descriptionHost = el("div", "panel-desc");
const descLabel = el("span", "pf-label panel-desc-label", "Description");
descLabel.id = "panel-desc-label";
const descText = el("textarea", "input panel-desc-text");
descText.setAttribute("aria-labelledby", descLabel.id);
descText.placeholder = "Notes, steps, links (Markdown)";
descText.spellcheck = false;
const descError = el("p", "field-error");
descError.setAttribute("role", "alert");
descError.hidden = true;
descriptionHost.append(descText);
const setDescError = (msg: string) => {
  descError.textContent = msg;
  descError.hidden = msg === "";
};

const titleSaver = textSaver("title", (id, v) => api.setText(id, v), title.setError);
/** The description's own autosaver (Task 8's editor reuses it). */
export const descriptionSaver = textSaver("description", (id, v) => api.setDescription(id, v), setDescError);

descText.addEventListener("input", () => shown && descriptionSaver.change(shown.id, descText.value));
descText.addEventListener("blur", () => void descriptionSaver.flush());
title.input.addEventListener("blur", () => void titleSaver.flush());

const body = el("div", "panel-body");
const desc = el("div", "panel-desc-field");
desc.append(descLabel, descriptionHost, descError);
body.append(title.root, priority.root, due.root, repeat.root, tags.root, desc);

const foot = el("footer", "panel-foot");
const meta = el("p", "panel-meta");
const metaText = el("span");
const status = el("span", "panel-status");
status.setAttribute("role", "status");
meta.append(metaText, status);
const actionError = el("p", "field-error");
actionError.setAttribute("role", "alert");
actionError.hidden = true;
const actions = el("div", "panel-actions");
const doneBtn = el("button", "btn");
doneBtn.type = "button";
const snoozeBtn = el("button", "btn btn-ghost");
snoozeBtn.type = "button";
snoozeBtn.setAttribute("aria-haspopup", "menu");
snoozeBtn.setAttribute("aria-expanded", "false");
snoozeBtn.innerHTML = `${icons.snooze}<span>Snooze</span>${icons.chevronDown}`;
const deleteBtn = el("button", "btn btn-ghost panel-delete");
deleteBtn.type = "button";
deleteBtn.innerHTML = `${icons.trash}<span>Delete</span>`;
actions.append(doneBtn, snoozeBtn, deleteBtn);
foot.append(meta, actionError, actions);

root.append(head, body, foot);

// ---------- saving ----------

/**
 * A discrete field's save: immediate, with its error shown inline on that
 * field. A failure puts the control back to the stored value.
 */
async function saveNow(field: Field, call: (id: string) => Promise<Item>): Promise<boolean> {
  if (!shown) return false;
  const id = shown.id;
  busy.set(field, (busy.get(field) ?? 0) + 1);
  immediate++;
  renderStatus();
  const p = call(id);
  inflight.add(p);
  let it: Item | null = null;
  let err: unknown = null;
  try {
    it = await p;
  } catch (e) {
    err = e;
  } finally {
    inflight.delete(p);
    busy.set(field, (busy.get(field) ?? 1) - 1);
    immediate--;
  }
  // The newest of several quick picks wins: only the last save refills its field.
  const own = isBusy(field) ? undefined : field;
  if (it) {
    if (id === shown?.id) field.setError("");
    applySaved(it, own);
  } else if (id === shown?.id) {
    field.setError(errorText(err));
    fill(false, own);
  }
  renderStatus();
  return it !== null;
}

/** Saves every pending edit now; resolves when all saves have settled. */
async function flushAll(): Promise<void> {
  due.commit(); // a typed but uncommitted due time
  await Promise.all([titleSaver.flush(), descriptionSaver.flush(), ...Array.from(inflight, (p) => p.catch(() => {}))]);
}

/** A save returned it; own is the field that saved it (refilled even if focused). */
function applySaved(it: Item, own?: Field): void {
  savedSinceOpen = true;
  if (it.id !== shown?.id) return;
  shown = it;
  fill(false, own);
}

function renderStatus(): void {
  const saving = immediate > 0 || titleSaver.status() === "saving" || descriptionSaver.status() === "saving";
  if (saving) {
    status.replaceChildren("Saving…");
  } else if (shownFailures().length > 0) {
    const retry = el("button", "panel-retry", "Retry");
    retry.type = "button";
    retry.addEventListener("click", () => {
      for (const f of shownFailures()) (f.kind === "title" ? titleSaver : descriptionSaver).change(f.id, f.value);
      void flushAll();
    });
    status.replaceChildren("Couldn't save · ", retry);
  } else if (savedSinceOpen) {
    const ok = el("span", "panel-saved");
    ok.innerHTML = icons.check;
    ok.append("Saved");
    status.replaceChildren(ok);
  } else {
    status.replaceChildren();
  }
  status.dataset.state = saving ? "saving" : shownFailures().length ? "error" : "";
}

// ---------- filling ----------

const dayMonth = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "short" });

/**
 * Shows `shown`. A field being edited (focused, or with its save in flight)
 * keeps what the user has; force (a new item) and own (the field whose save
 * just settled) overwrite it.
 */
function fill(force: boolean, own?: Field): void {
  const it = shown;
  if (!it) return;
  const free = (f: Field, node?: HTMLElement) => force || f === own || (!isBusy(f) && !(node && isEditing(node)));
  if (force || titleSaver.status() !== "saving") title.fill(it, force);
  if (free(priority)) priority.fill(it);
  if (force || own === due || !isBusy(due)) due.fill(it, force || own === due); // due.fill skips a focused input itself
  if (free(repeat, repeat.root)) repeat.fill(it);
  if (free(tags)) tags.fill(it);
  if (force || (!isEditing(descriptionHost) && descriptionSaver.status() !== "saving")) descText.value = it.description ?? "";

  const now = new Date();
  const edited = new Date(it.updatedAt);
  const ago = now.getTime() - edited.getTime() < 60_000 ? "just now" : `${agoLabel(edited, now)} ago`;
  metaText.textContent = `created ${dayMonth.format(new Date(it.createdAt))} · edited ${ago}`;

  const done = isDone(it);
  doneBtn.className = done ? "btn btn-ghost" : "btn btn-primary";
  doneBtn.innerHTML = done ? "<span>Reopen</span>" : `${icons.check}<span>Mark done</span>`;
  snoozeBtn.hidden = !isReminder(it) || done; // to-dos have nothing to snooze
}

function clearErrors(): void {
  for (const f of [title, priority, due, repeat, tags]) f.setError("");
  setDescError("");
  actionError.hidden = true;
}

async function load(): Promise<boolean> {
  const id = openId;
  if (!id) return false;
  const my = ++loadSeq;
  let item: Item;
  try {
    item = (await api.itemDetail(id)).item;
  } catch (err) {
    if (my !== loadSeq || openId !== id) return false;
    if (/not found/i.test(errorText(err))) void closePanel(); // gone from the store
    else showActionError(err);
    return false;
  }
  if (my !== loadSeq || openId !== id) return false;
  if (item.status === "deleted") {
    void closePanel();
    return false;
  }
  const switched = shown?.id !== item.id;
  shown = item;
  root.removeAttribute("aria-busy");
  fill(switched);
  return true;
}

function scheduleRefresh(): void {
  window.clearTimeout(refreshTimer);
  refreshTimer = window.setTimeout(() => void load(), REFRESH_DEBOUNCE_MS);
}

function showActionError(err: unknown): void {
  actionError.textContent = errorText(err);
  actionError.hidden = false;
}

// ---------- actions and keys ----------

function act(call: (id: string) => Promise<void>): void {
  if (!shown) return;
  actionError.hidden = true;
  call(shown.id).catch(showActionError);
}

doneBtn.addEventListener("click", () => act((id) => (shown && isDone(shown) ? api.reopen(id) : api.done(id))));

snoozeBtn.addEventListener("click", () => {
  const pick = (preset: SnoozePreset) => () => act((id) => api.snooze(id, preset));
  openMenu(
    snoozeBtn,
    [
      { label: "Snooze 10 min", icon: "snooze", run: pick("10m") },
      { label: "Snooze 1 hour", icon: "snooze", run: pick("1h") },
      { label: "Snooze until tomorrow", icon: "snooze", run: pick("tomorrow") },
    ],
    { label: "Snooze", returnFocus: snoozeBtn, trigger: snoozeBtn },
  );
});

deleteBtn.addEventListener("click", async () => {
  const id = shown?.id;
  if (!id) return;
  await closePanel(); // saves pending edits first, so Undo brings them back too
  hooks.remove(id);
});

closeBtn.addEventListener("click", () => void closePanel());

root.addEventListener("keydown", (e) => {
  if (e.key !== "Escape" || e.isComposing || e.defaultPrevented) return;
  e.preventDefault();
  e.stopPropagation(); // the list's Esc (clear search, close window) waits for the next press
  void closePanel();
});

/** Ctrl+S saves now, wherever focus is in the window. */
function onDocKey(e: KeyboardEvent): void {
  if ((e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === "s") {
    e.preventDefault();
    void flushAll();
  }
}

// ---------- public ----------

/** Adds the (hidden) panel to the list window. Called once, after the lazy import. */
export function initPanel(host: HTMLElement, h: PanelHooks): void {
  hooks = h;
  host.append(root);
}

export function isPanelOpen(): boolean {
  return openId !== null;
}

export function panelItemId(): string | null {
  return openId;
}

/**
 * Shows itemId in the panel, opening it if needed. A switch from another item
 * sends that item's pending edits first. focus "title" focuses the title once
 * the item has loaded.
 */
export async function openPanel(itemId: string, focus?: "title"): Promise<void> {
  if (openId !== null && openId !== itemId) void flushAll();
  const opening = openId === null;
  openId = itemId;
  if (opening) {
    root.hidden = false;
    void root.offsetWidth; // start the slide from off-screen
    root.classList.add("is-open");
    document.documentElement.dataset.panel = "open";
    quiet(() => api.widenList(WIDEN_TO));
    offChanged = on(events.itemsChanged, scheduleRefresh);
    document.addEventListener("keydown", onDocKey);
  }
  if (shown?.id !== itemId) {
    clearErrors();
    savedSinceOpen = false;
    renderStatus();
    root.setAttribute("aria-busy", "true");
  }
  const ok = await load();
  if (ok && focus === "title" && openId === itemId) {
    title.input.focus();
    title.input.setSelectionRange(title.input.value.length, title.input.value.length);
  }
}

/** Saves pending edits, then slides the panel out and gives the window its width back. */
export async function closePanel(): Promise<void> {
  const id = openId;
  if (id === null) return;
  await flushAll();
  if (openId !== id) return; // switched or closed meanwhile
  openId = null;
  shown = null;
  loadSeq++;
  window.clearTimeout(refreshTimer);
  offChanged?.();
  offChanged = null;
  document.removeEventListener("keydown", onDocKey);
  closeMenu(false);
  root.classList.remove("is-open");
  delete document.documentElement.dataset.panel;
  hooks.closed();
  window.setTimeout(() => {
    if (openId !== null) return; // reopened during the slide
    root.hidden = true;
    quiet(() => api.restoreListWidth());
  }, motionMs("--dur-exit"));
}
