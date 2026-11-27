// List window (#list): search, Today / Upcoming, one section per tag and a
// collapsed Done section. Follows the "List window" section of the UI guide.
//
// Rendering is a keyed update: section and row elements are kept in maps and
// reused across refreshes, a row is refilled only when its signature
// changes, and elements are moved only when out of place. Focus, scroll
// position and an open inline edit therefore survive an items:changed
// refresh.
import { api, errorText, events, on, type Item, type SnoozePreset, type View } from "./api";
import { el, keycaps, motionMs } from "./dom";
import { icons } from "./icons";
import { createRow, editRow, fillRow, isDone, isReminder, rowSignature, type RowModel } from "./item-row";
import { closeMenu, isMenuOpen, openMenu, type MenuItem } from "./menu";

const REFRESH_DEBOUNCE_MS = 50;
const RETICK_MS = 60_000; // relative times and the Upcoming horizon move on
const TOAST_MS = 5_000;
const DONE_OPEN_KEY = "awesome-reminder.list.doneOpen";
const DEFAULT_HOTKEY = "Ctrl+Shift+Space";
/** A click on the row text waits this long for a double-click (inline edit) before opening the panel. */
const DBLCLICK_MS = 250;

type PanelModule = typeof import("./panel/panel");

interface SectionModel {
  key: string; // "upcoming" | `tag:${tag}` | "done"
  title: string;
  tag?: string;
  rows: RowModel[];
}

interface SectionEls {
  root: HTMLElement;
  title: HTMLElement; // text node holder (or the toggle button for Done)
  rows: HTMLElement;
}

interface EditState {
  key: string;
  id: string;
  original: string;
  input?: HTMLInputElement;
  setError?: (msg: string) => void;
  /** The Update in flight, if any; resolves to whether it succeeded. */
  saving: Promise<boolean> | null;
}

function loadDoneOpen(): boolean {
  try {
    return localStorage.getItem(DONE_OPEN_KEY) === "1";
  } catch {
    return false;
  }
}

function saveDoneOpen(open: boolean): void {
  try {
    localStorage.setItem(DONE_OPEN_KEY, open ? "1" : "0");
  } catch {
    // per-viewer convenience only
  }
}

function normalize(v: View | null | undefined): View {
  return {
    upcoming: v?.upcoming ?? [],
    tags: (v?.tags ?? []).map((g) => ({ tag: g.tag, items: g.items ?? [] })),
    done: v?.done ?? [],
  };
}

/** Whether every whitespace-separated term occurs in the item's text or "#tags". */
function matches(it: Item, terms: string[]): boolean {
  if (terms.length === 0) return true;
  const hay = `${it.text} ${(it.tags ?? []).map((t) => `#${t}`).join(" ")}`.toLowerCase();
  return terms.every((t) => hay.includes(t));
}

function isTyping(t: EventTarget | null): t is HTMLInputElement | HTMLTextAreaElement {
  return t instanceof HTMLInputElement || t instanceof HTMLTextAreaElement;
}

const TEMPLATE = `
<div class="list-app">
  <header class="list-header">
    <div class="list-header-inner">
      <label class="list-search">
        <span class="list-search-icon" aria-hidden="true">${icons.search}</span>
        <input class="list-search-input" id="list-search" type="text" role="searchbox"
               autocomplete="off" spellcheck="false" placeholder="Search" aria-label="Search items" />
        <kbd aria-hidden="true">/</kbd>
      </label>
      <button class="icon-btn" id="list-settings" type="button" aria-label="Settings">${icons.settings}</button>
    </div>
  </header>
  <main class="list-scroll" id="list-scroll" aria-label="Items">
    <div class="list-content">
      <div class="list-empty" id="list-empty" hidden>
        <span class="list-empty-icon" aria-hidden="true">${icons.inbox}</span>
        <p class="list-empty-text" id="list-empty-text"></p>
      </div>
      <p class="list-nomatch" id="list-nomatch" role="status" hidden>No matches</p>
      <div class="list-sections" id="list-sections"></div>
    </div>
  </main>
  <div class="toast" id="list-toast" role="status" aria-live="polite">
    <span class="toast-text" id="list-toast-text"></span>
    <span class="toast-sep" id="list-toast-sep" aria-hidden="true" hidden>·</span>
    <button class="toast-action" id="list-toast-action" type="button" hidden></button>
  </div>
</div>`;

export function mountList(root: HTMLElement): void {
  document.documentElement.dataset.view = "list";
  root.innerHTML = TEMPLATE;
  const search = root.querySelector<HTMLInputElement>("#list-search")!;
  const settingsBtn = root.querySelector<HTMLButtonElement>("#list-settings")!;
  const scroll = root.querySelector<HTMLElement>("#list-scroll")!;
  const sectionsBox = root.querySelector<HTMLElement>("#list-sections")!;
  const empty = root.querySelector<HTMLElement>("#list-empty")!;
  const emptyText = root.querySelector<HTMLElement>("#list-empty-text")!;
  const noMatch = root.querySelector<HTMLElement>("#list-nomatch")!;
  const toast = root.querySelector<HTMLElement>("#list-toast")!;
  const toastText = root.querySelector<HTMLElement>("#list-toast-text")!;
  const toastAction = root.querySelector<HTMLButtonElement>("#list-toast-action")!;
  const toastSep = root.querySelector<HTMLElement>("#list-toast-sep")!;

  let view: View | null = null; // null until the first fetch settles
  let nextView: View | null = null; // fetched while a done animation held rendering
  let query = "";
  let doneOpen = loadDoneOpen();
  let hotkey = DEFAULT_HOTKEY;
  let selectedKey: string | null = null;
  let selectedIndex = 0;
  let edit: EditState | null = null;
  let holds = 0; // running done animations
  let rendering = false; // render() is moving nodes: ignore the blurs that causes
  let fetchSeq = 0;
  let refreshTimer: number | undefined;
  let focusAfterLoad: string | null = null; // item to focus once the first view renders
  let selectIdAfterRefresh: string | null = null;

  const sectionEls = new Map<string, SectionEls>();
  let sectionSeq = 0; // monotonic: section ids never repeat, even after a section is dropped
  const rowEls = new Map<string, HTMLElement>();
  const rowSigs = new Map<string, string>();
  const rowModels = new Map<string, RowModel>();

  // ---------- model ----------

  function buildSections(): SectionModel[] {
    if (!view) return [];
    const terms = query.toLowerCase().split(/\s+/).filter((t) => t !== "");
    // The row being edited always stays, so a filter can't drop its input.
    const rows = (key: string, items: Item[], tag?: string): RowModel[] =>
      items
        .map((item) => ({ key: `${key}|${item.id}`, item, sectionTag: tag }))
        .filter((r) => r.key === edit?.key || matches(r.item, terms));
    const out: SectionModel[] = [];
    const up = rows("upcoming", view.upcoming ?? []);
    if (up.length) out.push({ key: "upcoming", title: "Today / Upcoming", rows: up });
    for (const g of view.tags ?? []) {
      const key = `tag:${g.tag}`;
      const r = rows(key, g.items ?? [], g.tag);
      if (r.length) out.push({ key, title: `#${g.tag} (${r.length})`, tag: g.tag, rows: r });
    }
    const done = rows("done", view.done ?? []);
    if (done.length) out.push({ key: "done", title: `Done (${done.length})`, rows: done });
    return out;
  }

  function hasOpenItems(): boolean {
    return !!view && ((view.upcoming?.length ?? 0) > 0 || (view.tags ?? []).some((g) => (g.items?.length ?? 0) > 0));
  }

  // ---------- rendering ----------

  function sectionFor(m: SectionModel): SectionEls {
    let s = sectionEls.get(m.key);
    if (s) return s;
    const rootEl = el("section", "sec");
    rootEl.dataset.key = m.key;
    const n = ++sectionSeq;
    const head = el("h2", "sec-head");
    head.id = `sec-${n}-head`;
    const rowsEl = el("div", "sec-rows");
    rowsEl.setAttribute("role", "list");
    rowsEl.setAttribute("aria-labelledby", head.id);
    rowsEl.id = `sec-${n}-rows`;
    let title: HTMLElement;
    if (m.key === "done") {
      const btn = el("button", "sec-toggle");
      btn.type = "button";
      btn.setAttribute("aria-controls", rowsEl.id);
      btn.innerHTML = icons.chevron;
      title = el("span", "sec-title");
      btn.append(title);
      btn.addEventListener("click", () => {
        doneOpen = !doneOpen;
        saveDoneOpen(doneOpen);
        render();
      });
      head.append(btn);
    } else {
      title = el("span", "sec-title");
      head.append(title);
    }
    rootEl.append(head, rowsEl);
    s = { root: rootEl, title, rows: rowsEl };
    sectionEls.set(m.key, s);
    return s;
  }

  /** Makes parent's children exactly wanted, moving only what is out of place. */
  function reconcile(parent: HTMLElement, wanted: HTMLElement[]): void {
    const keep = new Set<Element>(wanted);
    for (const c of Array.from(parent.children)) if (!keep.has(c)) c.remove();
    wanted.forEach((w, i) => {
      if (parent.children[i] !== w) parent.insertBefore(w, parent.children[i] ?? null);
    });
  }

  function visibleRows(): HTMLElement[] {
    return Array.from(sectionsBox.querySelectorAll<HTMLElement>(".sec-rows:not([hidden]) > .row"));
  }

  function renderEmpty(): void {
    emptyText.replaceChildren("Nothing here. Press ", keycaps(hotkey), " to capture.");
  }

  function render(): void {
    rendering = true;
    try {
      renderNow();
    } finally {
      rendering = false;
    }
  }

  function renderNow(): void {
    const now = new Date();
    // A row (or the edit input) that is moved or removed loses focus; give it
    // back afterwards.
    const active = document.activeElement;
    const rowHadFocus = active instanceof HTMLElement && active.classList.contains("row");
    const editInput = edit?.input && active === edit.input ? edit.input : null;
    const caret = editInput ? [editInput.selectionStart ?? 0, editInput.selectionEnd ?? 0] : null;

    const models = buildSections();
    const wantedSections: HTMLElement[] = [];
    const liveKeys = new Set<string>();

    for (const m of models) {
      const s = sectionFor(m);
      if (s.title.textContent !== m.title) s.title.textContent = m.title;
      const collapsed = m.key === "done" && !doneOpen;
      s.root.classList.toggle("is-collapsed", collapsed);
      s.root.querySelector(".sec-toggle")?.setAttribute("aria-expanded", String(!collapsed));
      s.rows.hidden = collapsed;
      const wantedRows: HTMLElement[] = [];
      for (const r of m.rows) {
        liveKeys.add(r.key);
        rowModels.set(r.key, r);
        let rowEl = rowEls.get(r.key);
        if (!rowEl) {
          rowEl = createRow(r);
          rowEls.set(r.key, rowEl);
        }
        if (edit?.key !== r.key) {
          const sig = rowSignature(r, now);
          if (rowSigs.get(r.key) !== sig) {
            fillRow(rowEl, r, now);
            rowSigs.set(r.key, sig);
          }
        }
        wantedRows.push(rowEl);
      }
      reconcile(s.rows, wantedRows);
      wantedSections.push(s.root);
    }
    reconcile(sectionsBox, wantedSections);

    for (const k of Array.from(rowEls.keys())) {
      if (!liveKeys.has(k)) {
        rowEls.delete(k);
        rowSigs.delete(k);
        rowModels.delete(k);
      }
    }
    const liveSections = new Set(models.map((m) => m.key));
    for (const k of Array.from(sectionEls.keys())) if (!liveSections.has(k)) sectionEls.delete(k);

    // The edited item was deleted or moved elsewhere: its row is gone.
    if (edit && !liveKeys.has(edit.key)) {
      const lost = edit;
      edit = null;
      if (lost.input && lost.input.value !== lost.original) showToast("Edit discarded: the item changed elsewhere");
    } else if (editInput && edit?.input === editInput && document.activeElement !== editInput) {
      editInput.focus({ preventScroll: true });
      if (caret) editInput.setSelectionRange(caret[0], caret[1]);
    }

    const loaded = view !== null;
    empty.hidden = !loaded || query !== "" || hasOpenItems();
    noMatch.hidden = !loaded || query === "" || models.length > 0;

    restoreSelection(rowHadFocus);
  }

  /** Keeps the selection on the same row, the same item, or the same index. */
  function restoreSelection(refocus: boolean): void {
    const rows = visibleRows();
    let target: HTMLElement | undefined;
    if (selectIdAfterRefresh) {
      target = rows.find((r) => r.dataset.id === selectIdAfterRefresh);
      if (target) selectIdAfterRefresh = null;
    }
    if (!target && selectedKey) {
      target = rows.find((r) => r.dataset.key === selectedKey);
      if (!target) {
        const id = selectedKey.slice(selectedKey.indexOf("|") + 1);
        target = rows.find((r) => r.dataset.id === id);
      }
    }
    if (!target && selectedKey !== null && rows.length) target = rows[Math.min(selectedIndex, rows.length - 1)];
    applySelection(target ?? null, rows, refocus && !!target && document.activeElement !== target);
  }

  function applySelection(target: HTMLElement | null, rows = visibleRows(), focus = false): void {
    for (const r of rows) {
      const on = r === target;
      r.classList.toggle("is-selected", on);
      if (on) r.setAttribute("aria-current", "true");
      else r.removeAttribute("aria-current");
      r.tabIndex = -1;
    }
    // Roving tab stop: the selected row, else the first one.
    const stop = target ?? rows[0];
    if (stop) stop.tabIndex = 0;
    selectedKey = target?.dataset.key ?? null;
    selectedIndex = target ? Math.max(rows.indexOf(target), 0) : 0;
    if (focus && target) {
      target.focus({ preventScroll: true });
      target.scrollIntoView({ block: "nearest" });
    }
  }

  /** Selects row and focuses it; scroll brings it into view (keyboard moves). */
  function select(row: HTMLElement | null | undefined, scroll = true): void {
    if (!row) return;
    applySelection(row, visibleRows(), false);
    row.focus({ preventScroll: true });
    if (scroll) row.scrollIntoView({ block: "nearest" });
  }

  function selectedRow(): HTMLElement | null {
    return selectedKey ? rowEls.get(selectedKey) ?? null : null;
  }

  function move(delta: number): void {
    const rows = visibleRows();
    if (!rows.length) return;
    const cur = selectedRow();
    const i = cur ? rows.indexOf(cur) : -1;
    const next = i < 0 ? (delta > 0 ? 0 : rows.length - 1) : Math.min(Math.max(i + delta, 0), rows.length - 1);
    select(rows[next]);
  }

  // ---------- data ----------

  function scheduleRefresh(): void {
    window.clearTimeout(refreshTimer);
    refreshTimer = window.setTimeout(() => void refresh(), REFRESH_DEBOUNCE_MS);
  }

  async function refresh(): Promise<void> {
    const my = ++fetchSeq;
    let v: View;
    try {
      v = normalize(await api.view());
    } catch {
      // No backend (plain browser) or a transient error: keep what we have,
      // or show the empty state on the first load.
      if (view !== null || my !== fetchSeq) return;
      v = normalize(null);
    }
    if (my !== fetchSeq) return;
    nextView = v;
    applyIfIdle();
  }

  /**
   * Applies a fetched view unless a done animation is running. An open edit
   * does not hold rendering: render() leaves the edited row's content alone.
   */
  function applyIfIdle(): void {
    if (holds > 0) return;
    const first = view === null;
    if (nextView) {
      view = nextView;
      nextView = null;
    }
    render();
    if (first) {
      void consumeFocus();
      if (focusAfterLoad) {
        const id = focusAfterLoad;
        focusAfterLoad = null;
        focusItem(id);
      }
    }
  }

  // ---------- focus from OpenList(id) ----------

  async function consumeFocus(): Promise<void> {
    let id = "";
    try {
      id = await api.takePendingFocus();
    } catch {
      return;
    }
    if (!id) return;
    if (view === null) focusAfterLoad = id;
    else focusItem(id);
  }

  function findRow(id: string): HTMLElement | undefined {
    return visibleRows().find((r) => r.dataset.id === id);
  }

  function focusItem(id: string): void {
    let row = findRow(id);
    if (!row && query !== "") {
      search.value = query = "";
      render();
      row = findRow(id);
    }
    if (!row && !doneOpen && (view?.done ?? []).some((it) => it.id === id)) {
      doneOpen = true; // for this window only; the saved preference stays
      render();
      row = findRow(id);
    }
    if (!row) return;
    applySelection(row, visibleRows(), false);
    row.focus({ preventScroll: true });
    row.scrollIntoView({ block: "center" });
    row.classList.remove("is-highlight");
    void row.offsetWidth; // restart the fade
    row.classList.add("is-highlight");
    const r = row;
    window.setTimeout(() => r.classList.remove("is-highlight"), motionMs("--dur-highlight"));
  }

  // ---------- toast ----------

  let toastTimer: number | undefined;
  let toastRun: (() => void) | null = null;

  function hideToast(): void {
    window.clearTimeout(toastTimer);
    toast.classList.remove("is-visible");
    toastRun = null;
  }

  function armToast(): void {
    window.clearTimeout(toastTimer);
    toastTimer = window.setTimeout(hideToast, TOAST_MS);
  }

  function showToast(message: string, action?: { label: string; run: () => void }): void {
    toastText.textContent = message;
    toastAction.hidden = toastSep.hidden = !action;
    toastAction.textContent = action?.label ?? "";
    toastRun = action?.run ?? null;
    toast.classList.add("is-visible");
    armToast();
  }

  toastAction.addEventListener("click", () => {
    const run = toastRun;
    hideToast();
    run?.();
    selectedRow()?.focus({ preventScroll: true });
  });
  // Don't let the toast vanish while the pointer or focus is on it.
  toast.addEventListener("pointerenter", () => window.clearTimeout(toastTimer));
  toast.addEventListener("pointerleave", () => toast.classList.contains("is-visible") && armToast());
  toast.addEventListener("focusin", () => window.clearTimeout(toastTimer));
  toast.addEventListener("focusout", () => toast.classList.contains("is-visible") && armToast());

  function fail(err: unknown): void {
    showToast(errorText(err));
  }

  // ---------- actions ----------

  function modelOf(row: HTMLElement | null): RowModel | undefined {
    return row?.dataset.key ? rowModels.get(row.dataset.key) : undefined;
  }

  async function toggleDone(row: HTMLElement): Promise<void> {
    if (!(await settleEdit())) return;
    const m = modelOf(row);
    if (!m) return;
    const it = m.item;
    if (isDone(it)) {
      selectIdAfterRefresh = it.id;
      api.reopen(it.id).catch(fail);
      return;
    }
    const rows = Array.from(sectionsBox.querySelectorAll<HTMLElement>(".row")).filter((r) => r.dataset.id === it.id);
    rows.forEach((r) => r.classList.add("is-completing"));
    holds++;
    const wait = new Promise((res) => window.setTimeout(res, motionMs("--dur-done")));
    try {
      await Promise.all([api.done(it.id), wait]);
    } catch (err) {
      fail(err);
    } finally {
      holds--;
      // Force a refill so a failed or recurring Done drops the strike-through.
      rows.forEach((r) => r.dataset.key && rowSigs.delete(r.dataset.key));
      if (it.rrule) selectIdAfterRefresh = it.id;
      applyIfIdle();
    }
  }

  async function remove(row: HTMLElement): Promise<void> {
    if (!(await settleEdit())) return;
    const m = modelOf(row);
    if (!m) return;
    if (panel?.panelItemId() === m.item.id) await panel.closePanel(); // its pending edits save first
    removeId(m.item.id);
  }

  /** Deletes with the Undo toast (rows and the detail panel). */
  function removeId(id: string): void {
    api
      .deleteItem(id)
      .then(() =>
        showToast("Deleted", {
          label: "Undo",
          run: () => {
            selectIdAfterRefresh = id;
            api.undelete(id).catch(fail);
          },
        }),
      )
      .catch(fail);
  }

  function snooze(row: HTMLElement, preset: SnoozePreset): void {
    const m = modelOf(row);
    if (!m) return;
    api.snooze(m.item.id, preset).catch(fail);
  }

  function menuItems(row: HTMLElement, snoozeOnly: boolean): MenuItem[] {
    const m = modelOf(row);
    if (!m) return [];
    const it = m.item;
    const items: MenuItem[] = [];
    if (isReminder(it) && !isDone(it)) {
      items.push(
        { label: "Snooze 10 min", icon: "snooze", run: () => snooze(row, "10m") },
        { label: "Snooze 1 hour", icon: "snooze", run: () => snooze(row, "1h") },
        { label: "Snooze until tomorrow", icon: "snooze", run: () => snooze(row, "tomorrow") },
      );
    }
    if (snoozeOnly) return items;
    items.push({ label: "Edit", icon: "edit", run: () => void startEdit(row) });
    if (it.rrule && !isDone(it)) {
      items.push({ label: "Stop repeating", icon: "repeatOff", run: () => void api.stopRepeating(it.id).catch(fail) });
    }
    items.push({ label: "Delete", icon: "trash", danger: true, run: () => void remove(row) });
    return items;
  }

  async function showMenu(row: HTMLElement, snoozeOnly = false, at?: DOMRect): Promise<void> {
    if (!(await settleEdit())) return;
    if (!row.isConnected) return;
    select(row, false); // a scroll would dismiss the menu
    const trigger = row.querySelector<HTMLElement>(".row-more") ?? undefined;
    const items = menuItems(row, snoozeOnly);
    const m = modelOf(row);
    openMenu(at ?? trigger ?? row, items, {
      label: snoozeOnly ? "Snooze" : `Actions for ${m?.item.text ?? "item"}`,
      returnFocus: row,
      trigger,
      onClose: () => row.classList.remove("has-menu"),
    });
    if (isMenuOpen()) row.classList.add("has-menu");
  }

  // ---------- detail panel (lazy: loaded the first time it opens) ----------

  let panel: PanelModule | null = null;
  let panelLoading: Promise<PanelModule> | null = null;

  function loadPanel(): Promise<PanelModule> {
    panelLoading ??= import("./panel/panel").then((mod) => {
      mod.initPanel(root, {
        remove: removeId,
        closed: () => (selectedRow() ?? visibleRows()[0])?.focus({ preventScroll: true }),
      });
      panel = mod;
      return mod;
    });
    return panelLoading;
  }

  function panelOpen(): boolean {
    return panel?.isPanelOpen() ?? false;
  }

  /** Opens the panel on row's item; focus "title" puts the caret in its title. */
  async function openDetails(row: HTMLElement | null | undefined, focus?: "title"): Promise<void> {
    const m = modelOf(row ?? null);
    if (!m) return;
    const mod = await loadPanel();
    await mod.openPanel(m.item.id, focus);
  }

  /** After a keyboard move: an open panel follows the selection. */
  function syncPanel(): void {
    if (panelOpen()) void openDetails(selectedRow());
  }

  // ---------- inline edit ----------

  async function startEdit(row: HTMLElement): Promise<void> {
    if (edit && edit.key === row.dataset.key) {
      edit.input?.focus();
      return;
    }
    if (!(await settleEdit())) return;
    const m = modelOf(row);
    if (!m || !row.isConnected) return;
    // One editor per item: the inline edit replaces the panel on the same item.
    if (panel?.panelItemId() === m.item.id) await panel.closePanel();
    if (!row.isConnected) return;
    select(row, false);
    const state: EditState = { key: m.key, id: m.item.id, original: "", saving: null };
    edit = state;
    let text: string;
    try {
      text = await api.editText(m.item.id);
    } catch (err) {
      if (edit === state) edit = null;
      fail(err);
      applyIfIdle();
      return;
    }
    if (edit !== state || !row.isConnected) {
      if (edit === state) edit = null;
      return;
    }
    state.original = text;
    const { input, setError } = editRow(row, text);
    state.input = input;
    state.setError = setError;
    input.focus();
    input.setSelectionRange(text.length, text.length);
    input.addEventListener("keydown", (e) => {
      if (e.isComposing) return;
      if (e.key === "Enter") {
        e.preventDefault();
        e.stopPropagation();
        void commitEdit();
      } else if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        endEdit(true);
      }
    });
    input.addEventListener("input", () => setError(""));
    // Leaving an unchanged edit cancels it; a changed one stays open until
    // the next action settles it (settleEdit).
    input.addEventListener("blur", () => {
      if (rendering) return; // render() moved the row and refocuses the input
      if (edit === state && !state.saving && input.value === state.original) endEdit(false);
    });
  }

  /**
   * Saves the open edit; refocus returns focus to its row afterwards.
   * Resolves false if Update failed: the edit stays open, shows the error
   * and has focus.
   */
  function commitEdit(refocus = true): Promise<boolean> {
    const state = edit;
    if (!state?.input) return Promise.resolve(true);
    if (state.saving) return state.saving;
    const input = state.input;
    if (input.value === state.original) {
      endEdit(refocus);
      return Promise.resolve(true);
    }
    const saving = api.update(state.id, input.value).then(
      () => {
        state.saving = null;
        selectIdAfterRefresh = state.id; // a tag change moves the row to another section
        if (edit === state) endEdit(refocus);
        return true;
      },
      (err: unknown) => {
        state.saving = null;
        const msg = errorText(err);
        state.setError?.(msg.includes("empty input") ? "Add some text to save" : msg);
        if (edit === state) {
          input.focus();
          input.scrollIntoView({ block: "nearest" });
        }
        return false;
      },
    );
    state.saving = saving;
    return saving;
  }

  /**
   * Settles an open edit before another action: a changed edit is saved, an
   * unchanged one (or one still loading its text) is cancelled. Resolves
   * false when the save failed; the caller must then drop its action.
   */
  async function settleEdit(): Promise<boolean> {
    const state = edit;
    if (!state) return true;
    if (!state.input || (!state.saving && state.input.value === state.original)) {
      endEdit(false);
      return true;
    }
    return commitEdit(false);
  }

  function endEdit(refocus: boolean): void {
    const state = edit;
    if (!state) return;
    edit = null;
    rowSigs.delete(state.key); // refill the row from its model
    const row = rowEls.get(state.key);
    applyIfIdle();
    if (refocus) (rowEls.get(state.key) ?? selectedRow() ?? row)?.focus({ preventScroll: true });
  }

  // ---------- events ----------

  // A click on the row text may be the first half of a double-click (inline
  // edit): the panel opens only once the double-click time has passed.
  let openTimer: number | undefined;
  const cancelPendingOpen = () => window.clearTimeout(openTimer);

  // A click anywhere in the list outside the edited row settles the edit
  // first; the clicked action runs only if that succeeded.
  scroll.addEventListener("click", async (e) => {
    const t = e.target as HTMLElement;
    const row = t.closest<HTMLElement>(".row");
    cancelPendingOpen();
    if (row?.classList.contains("is-editing")) return;
    if (!(await settleEdit())) return;
    if (!row || !row.isConnected) return;
    if (t.closest(".row-check")) {
      select(row, false);
      void toggleDone(row);
    } else if (t.closest(".row-more")) {
      void showMenu(row);
    } else {
      select(row, false); // the row is under the pointer: don't jump
      if (!t.closest(".row-main")) void openDetails(row);
      else if (e.detail <= 1) openTimer = window.setTimeout(() => row.isConnected && void openDetails(row), DBLCLICK_MS);
      // the second click of a double-click opens nothing: dblclick edits inline
    }
  });

  sectionsBox.addEventListener("dblclick", (e) => {
    const t = e.target as HTMLElement;
    const row = t.closest<HTMLElement>(".row");
    if (row && t.closest(".row-main")) {
      cancelPendingOpen();
      void startEdit(row);
    }
  });

  sectionsBox.addEventListener("contextmenu", (e) => {
    const row = (e.target as HTMLElement).closest<HTMLElement>(".row");
    if (!row || row.classList.contains("is-editing")) return;
    e.preventDefault();
    void showMenu(row, false, new DOMRect(e.clientX, e.clientY, 0, 0));
  });

  search.addEventListener("input", () => {
    query = search.value.trim();
    render();
  });

  settingsBtn.addEventListener("click", () => void api.openSettings().catch(() => {}));

  document.addEventListener("keydown", (e) => {
    if (e.isComposing || isMenuOpen()) return;
    const t = e.target;
    if (t instanceof Element && t.closest(".panel")) return; // the panel has its own keys

    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "z" && toastRun && !isTyping(t)) {
      e.preventDefault();
      toastAction.click();
      return;
    }
    if (e.ctrlKey || e.metaKey || e.altKey) return;

    if (isTyping(t)) {
      if (t !== search) return; // the edit input handles its own Enter/Esc
      if (e.key === "Escape") {
        e.preventDefault();
        if (panelOpen()) {
          void panel?.closePanel();
        } else if (search.value !== "") {
          search.value = query = "";
          render();
        } else {
          void api.closeWindow().catch(() => {});
        }
      } else if (e.key === "Enter") {
        e.preventDefault(); // jump to the selected match, else the first
        const rows = visibleRows();
        const cur = selectedRow();
        select(cur && rows.includes(cur) ? cur : rows[0]);
      }
      return;
    }

    const onButton = t instanceof HTMLButtonElement && !(t as HTMLElement).closest(".row");
    const row = selectedRow();
    switch (e.key) {
      case "/":
        e.preventDefault();
        search.focus();
        search.select();
        return;
      case "ArrowDown":
        e.preventDefault();
        move(1);
        syncPanel();
        return;
      case "ArrowUp":
        e.preventDefault();
        move(-1);
        syncPanel();
        return;
      case "Home":
        e.preventDefault();
        select(visibleRows()[0]);
        syncPanel();
        return;
      case "End":
        e.preventDefault();
        select(visibleRows().at(-1));
        syncPanel();
        return;
      case "Escape":
        e.preventDefault();
        // Esc order: the panel, then the search, then the window.
        if (panelOpen()) {
          void panel?.closePanel();
        } else if (query !== "") {
          search.value = query = "";
          render();
        } else if (edit) {
          endEdit(true); // an edit left open by clicking away
        } else {
          void api.closeWindow().catch(() => {});
        }
        return;
    }
    if (onButton || !row) return; // Space/Enter belong to the focused button
    switch (e.key) {
      case " ":
        e.preventDefault();
        void toggleDone(row);
        return;
      case "Enter":
        e.preventDefault();
        void openDetails(row, "title");
        return;
      case "F2":
        e.preventDefault();
        void startEdit(row);
        return;
      case "s":
      case "S":
        e.preventDefault();
        void showMenu(row, true);
        return;
      case "Delete":
        e.preventDefault();
        void remove(row);
        return;
      case ".":
      case "ContextMenu":
        e.preventDefault();
        void showMenu(row);
        return;
    }
  });

  // Wails runs every window in its own page, so these listeners live and die
  // with this window.
  on(events.itemsChanged, scheduleRefresh);
  on(events.listFocus, () => void consumeFocus());
  window.setInterval(scheduleRefresh, RETICK_MS);
  window.addEventListener("blur", () => closeMenu(false));

  renderEmpty();
  api
    .getSettings()
    .then((s) => {
      if (s?.hotkey) {
        hotkey = s.hotkey;
        renderEmpty();
      }
    })
    .catch(() => {});
  void refresh();
}
