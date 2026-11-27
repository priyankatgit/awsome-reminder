// List window (#list): search, "Waiting on you" (replies owed, by person),
// Today / Upcoming, one section per tag and a collapsed Done section.
// Follows the "List window" section of the UI guide.
//
// Rendering is a keyed update: section and row elements are kept in maps and
// reused across refreshes, a row is refilled only when its signature
// changes, and elements are moved only when out of place. Focus, scroll
// position and an open inline edit therefore survive an items:changed
// refresh.
import { api, errorText, events, on, type Item, type SnoozePreset, type View } from "./api";
import { el, keycaps, motionMs } from "./dom";
import { icons } from "./icons";
import { applyMarker, createRow, editRow, effectiveDue, fillRow, isDone, isOpenReply, isReminder, rowSignature, type RowModel } from "./item-row";
import { closeMenu, isMenuOpen, openMenu, type MenuItem } from "./menu";
import { canAnimate, celebrate, easing, enter, ghost, glide, measure, reducedMotion, stopGlide } from "./motion";
import { personLabel } from "./reply-mode";
import { mountSyncPill } from "./sync-pill";
import { needsRefetch, showMarkers } from "./sync-status";

const REFRESH_DEBOUNCE_MS = 50;
const RETICK_MS = 60_000; // relative times and the Upcoming horizon move on
const TOAST_MS = 5_000;
const DONE_OPEN_KEY = "awesome-reminder.list.doneOpen";
const DEFAULT_HOTKEY = "Ctrl+Shift+Space";
/** A click on the row text waits this long for a double-click (inline edit) before opening the panel. */
const DBLCLICK_MS = 250;

type PanelModule = typeof import("./panel/panel");

interface SectionModel {
  key: string; // "waiting" | "upcoming" | `tag:${tag}` | "done"
  title: string;
  tag?: string;
  rows: RowModel[];
  /** Waiting on you: the rows again, under one subheading per person. */
  groups?: GroupModel[];
}

/** One person's replies inside the Waiting section. */
interface GroupModel {
  key: string; // `person:${lowercased name}`; "person:" is nobody
  title: string;
  rows: RowModel[];
}

interface SectionEls {
  root: HTMLElement;
  title: HTMLElement; // text node holder (or the toggle button for Done)
  /** The rows list; in the Waiting section, the holder of the person groups. */
  rows: HTMLElement;
}

interface GroupEls {
  root: HTMLElement;
  title: HTMLElement;
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
    waiting: (v?.waiting ?? []).map((g) => ({ person: g.person, items: g.items ?? [] })),
    pending: v?.pending ?? [],
  };
}

/** Whether every whitespace-separated term occurs in the item's text, "#tags", or a reply's person or source. */
function matches(it: Item, terms: string[]): boolean {
  if (terms.length === 0) return true;
  const hay = `${it.text} ${(it.tags ?? []).map((t) => `#${t}`).join(" ")} ${it.owedTo ?? ""} ${it.source ?? ""}`.toLowerCase();
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
      <div id="list-sync"></div>
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
    <span class="toast-timer" id="list-toast-timer" aria-hidden="true" hidden></span>
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
  const toastBar = root.querySelector<HTMLElement>("#list-toast-timer")!;
  const content = root.querySelector<HTMLElement>(".list-content")!;

  let markersOn = false; // sync is configured: rows show "not synced yet" markers
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
  const completing = new Set<string>(); // items whose done animation is running

  const sectionEls = new Map<string, SectionEls>();
  const groupEls = new Map<string, GroupEls>();
  let sectionSeq = 0; // monotonic: section ids never repeat, even after a section is dropped
  const rowEls = new Map<string, HTMLElement>();
  const rowSigs = new Map<string, string>();
  const rowModels = new Map<string, RowModel>();

  // ---------- model ----------

  function buildSections(): SectionModel[] {
    if (!view) return [];
    const pendingIds = new Set(view.pending);
    const terms = query.toLowerCase().split(/\s+/).filter((t) => t !== "");
    // The row being edited always stays, so a filter can't drop its input.
    const rows = (key: string, items: Item[], tag?: string): RowModel[] =>
      items
        .map((item) => ({ key: `${key}|${item.id}`, item, sectionTag: tag, markers: markersOn, unsynced: markersOn && pendingIds.has(item.id) }))
        .filter((r) => r.key === edit?.key || matches(r.item, terms));
    const out: SectionModel[] = [];
    // Replies owed are listed once, under Waiting on you, even if a group
    // below also carries them. Taken from the view, not the filtered rows, so
    // a reply the search hides doesn't surface in its tag instead.
    const waitingIds = new Set((view.waiting ?? []).flatMap((g) => (g.items ?? []).map((it) => it.id)));
    const open = (items: Item[] | null | undefined) => (items ?? []).filter((it) => !waitingIds.has(it.id));
    const groups: GroupModel[] = [];
    for (const g of view.waiting ?? []) {
      const r = rows("waiting", g.items ?? []);
      if (r.length) groups.push({ key: `person:${g.person.trim().toLowerCase()}`, title: personLabel(g.person), rows: r });
    }
    const waiting = groups.flatMap((g) => g.rows);
    if (waiting.length) out.push({ key: "waiting", title: `Waiting on you (${waiting.length})`, rows: waiting, groups });
    const up = rows("upcoming", open(view.upcoming));
    if (up.length) out.push({ key: "upcoming", title: "Today / Upcoming", rows: up });
    for (const g of view.tags ?? []) {
      const key = `tag:${g.tag}`;
      const r = rows(key, open(g.items), g.tag);
      if (r.length) out.push({ key, title: `#${g.tag} (${r.length})`, tag: g.tag, rows: r });
    }
    const done = rows("done", view.done ?? []);
    if (done.length) out.push({ key: "done", title: `Done (${done.length})`, rows: done });
    return out;
  }

  function hasOpenItems(): boolean {
    const any = (groups: { items: Item[] | null }[] | null) => (groups ?? []).some((g) => (g.items?.length ?? 0) > 0);
    return !!view && ((view.upcoming?.length ?? 0) > 0 || any(view.tags) || any(view.waiting));
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
    if (m.groups) {
      rowsEl.classList.add("sec-groups"); // each person group holds its own list
    } else {
      rowsEl.setAttribute("role", "list");
      rowsEl.setAttribute("aria-labelledby", head.id);
    }
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
        render(true);
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

  /** A person group of the Waiting section: a subheading over its own list. */
  function groupFor(g: GroupModel): GroupEls {
    let ge = groupEls.get(g.key);
    if (ge) return ge;
    const n = ++sectionSeq;
    const rootEl = el("div", "sec-group");
    const title = el("h3", "sec-sub");
    title.id = `sec-${n}-head`;
    const rowsEl = el("div", "sec-rows");
    rowsEl.setAttribute("role", "list");
    rowsEl.setAttribute("aria-labelledby", title.id);
    rootEl.append(title, rowsEl);
    ge = { root: rootEl, title, rows: rowsEl };
    groupEls.set(g.key, ge);
    return ge;
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

  /**
   * Renders the model. animate (data changes, the Done toggle; not typing in
   * search) glides rows and headers from where they were to where they land
   * (FLIP), carries a row that changed section across, fades new rows in and
   * leaves a fading ghost where a row went away.
   */
  function render(animate = false): void {
    const before = animate && !reducedMotion() ? measure(reflowEls()) : null;
    // A plain render (typing in search) must not leave a running glide
    // translating rows by an offset measured against the old layout.
    if (!before) for (const e of reflowEls()) stopGlide(e);
    rendering = true;
    try {
      renderNow();
    } finally {
      rendering = false;
    }
    if (before) reflow(before);
  }

  function reflowEls(): HTMLElement[] {
    return Array.from(sectionsBox.querySelectorAll<HTMLElement>(".sec-head, .sec-sub, .sec-rows:not([hidden]) > .row"));
  }

  interface Gone {
    el: HTMLElement;
    rect: DOMRect;
  }

  function reflow(before: Map<Element, DOMRect>): void {
    const els = reflowEls();
    for (const e of els) stopGlide(e);
    const after = measure(els); // one layout pass, then only writes
    const timing = { duration: motionMs("--dur-glide"), easing: easing("--ease-spring") };
    const live = new Set<Element>(els);
    // Removed rows by item id: a reminder has two rows (Today / Upcoming and
    // its tag), so completing it removes both.
    const gone = new Map<string, Gone[]>();
    const goneHeads: Gone[] = [];
    for (const [e, rect] of before) {
      if (live.has(e) || !(e instanceof HTMLElement)) continue;
      const id = e.dataset.id;
      if (id) gone.set(id, [...(gone.get(id) ?? []), { el: e, rect }]);
      else goneHeads.push({ el: e, rect });
    }
    for (const e of els) {
      const last = after.get(e)!;
      const first = before.get(e);
      if (first) {
        glide(e, first, last, timing);
        continue;
      }
      const from = e.dataset.id ? takeClosest(gone.get(e.dataset.id), last) : undefined;
      if (from) glide(e, from.rect, last, timing); // moved to another section: carry it across
      else enter(e, "--row-enter-offset", last);
    }
    const ghosts = [...[...gone.values()].flat(), ...goneHeads];
    if (ghosts.length) {
      const hostRect = content.getBoundingClientRect();
      for (const g of ghosts) ghost(g.el, g.rect, content, hostRect);
    }
  }

  /** Removes and returns the candidate nearest to rect (the shortest carry). */
  function takeClosest(list: Gone[] | undefined, rect: DOMRect): Gone | undefined {
    if (!list?.length) return undefined;
    let best = 0;
    list.forEach((g, i) => {
      if (Math.abs(g.rect.top - rect.top) < Math.abs(list[best].rect.top - rect.top)) best = i;
    });
    return list.splice(best, 1)[0];
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
    const liveGroups = new Set<string>();

    const rowFor = (r: RowModel): HTMLElement => {
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
        } else {
          applyMarker(rowEl, r);
        }
      }
      return rowEl;
    };

    for (const m of models) {
      const s = sectionFor(m);
      if (s.title.textContent !== m.title) s.title.textContent = m.title;
      const collapsed = m.key === "done" && !doneOpen;
      s.root.classList.toggle("is-collapsed", collapsed);
      s.root.querySelector(".sec-toggle")?.setAttribute("aria-expanded", String(!collapsed));
      s.rows.hidden = collapsed;
      if (m.groups) {
        // A row whose person changed keeps its element and glides to its new group.
        const wantedGroups = m.groups.map((g) => {
          liveGroups.add(g.key);
          const ge = groupFor(g);
          if (ge.title.textContent !== g.title) ge.title.textContent = g.title;
          reconcile(ge.rows, g.rows.map(rowFor));
          return ge.root;
        });
        reconcile(s.rows, wantedGroups);
      } else {
        reconcile(s.rows, m.rows.map(rowFor));
      }
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
    for (const k of Array.from(groupEls.keys())) if (!liveGroups.has(k)) groupEls.delete(k);

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
    render(!first);
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

  let toastBarRun: Animation | null = null;

  /** The undo bar runs down over TOAST_MS; stopped, it rests full (paused toast). */
  function runToastBar(on: boolean): void {
    toastBarRun?.cancel();
    toastBarRun = null;
    if (!on || toastBar.hidden || !canAnimate(toastBar)) return;
    toastBarRun = toastBar.animate([{ transform: "scaleX(1)" }, { transform: "scaleX(0)" }], {
      duration: TOAST_MS,
      easing: "linear",
      fill: "forwards",
    });
  }

  function hideToast(): void {
    window.clearTimeout(toastTimer);
    // The bar stays empty through the fade; the next toast restarts it.
    toast.classList.remove("is-visible");
    toastRun = null;
  }

  function armToast(): void {
    window.clearTimeout(toastTimer);
    toastTimer = window.setTimeout(hideToast, TOAST_MS);
    runToastBar(true);
  }

  /** Pointer or focus on the toast: it stays, and its bar rests full. */
  function pauseToast(): void {
    window.clearTimeout(toastTimer);
    runToastBar(false);
  }

  function showToast(message: string, action?: { label: string; run: () => void }): void {
    toastText.textContent = message;
    toastAction.hidden = toastSep.hidden = !action;
    toastAction.textContent = action?.label ?? "";
    toastRun = action?.run ?? null;
    toastBar.hidden = !action || reducedMotion(); // a still bar would say nothing
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
  toast.addEventListener("pointerenter", pauseToast);
  toast.addEventListener("pointerleave", () => toast.classList.contains("is-visible") && armToast());
  toast.addEventListener("focusin", pauseToast);
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
    const party = clearsToday(it);
    completing.add(it.id);
    holds++;
    // The check draws, then the strike follows (--done-stagger later).
    const wait = new Promise((res) => window.setTimeout(res, motionMs("--dur-done") + motionMs("--done-stagger")));
    try {
      await Promise.all([api.done(it.id), wait]);
      if (party && row.isConnected) {
        const box = row.querySelector(".check-box")?.getBoundingClientRect();
        if (box) celebrate(box.left + box.width / 2, box.top + box.height / 2);
      }
    } catch (err) {
      fail(err);
    } finally {
      completing.delete(it.id);
      holds--;
      // Force a refill so a failed or recurring Done drops the strike-through.
      rows.forEach((r) => r.dataset.key && rowSigs.delete(r.dataset.key));
      if (it.rrule) selectIdAfterRefresh = it.id;
      applyIfIdle();
    }
  }

  /**
   * Whether completing it leaves nothing due today: it is due by the end of
   * today (overdue counts) and no other open reminder is, apart from ones
   * whose done animation is already running. A reply counts only when it has
   * a due time today, like any reminder.
   */
  function clearsToday(it: Item): boolean {
    if (!view) return false;
    const now = new Date();
    const end = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1).getTime();
    const dueToday = (i: Item) => {
      const d = effectiveDue(i, now);
      return isReminder(i) && !isDone(i) && d !== null && d.getTime() < end;
    };
    const others = [...(view.upcoming ?? []), ...(view.waiting ?? []).flatMap((g) => g.items ?? [])];
    return dueToday(it) && !others.some((i) => i.id !== it.id && !completing.has(i.id) && dueToday(i));
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
    if (isOpenReply(it)) {
      items.push({ label: "No reply needed", icon: "replyOff", run: () => void api.dropReply(it.id).catch(fail) });
    }
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
    if (!m || !row) return;
    const mod = await loadPanel();
    // Morph only from a fully closed panel: one still sliding out would sit
    // in both snapshots and spring in from twice its width.
    if (mod.panelIdle() && canMorph()) return morphOpen(mod, row, m.item.id, focus);
    await mod.openPanel(m.item.id, focus);
  }

  let morph: ViewTransition | null = null; // the running panel morph

  /** Same-document View Transitions (Chromium/WebView2), motion allowed. */
  function canMorph(): boolean {
    return typeof document.startViewTransition === "function" && !reducedMotion();
  }

  /**
   * Opens the panel with the row's title growing into the panel's title: a
   * view transition in which both carry the name "item-title" (panel.css).
   * Any key press or click skips it, so input acts at once. If the engine
   * aborts it any other way (no frames in time, a viewport change), the
   * panel slides in instead.
   */
  async function morphOpen(mod: PanelModule, row: HTMLElement, id: string, focus?: "title"): Promise<void> {
    // Openings widen a narrow window; a viewport resize mid-transition
    // aborts it, so the window grows first.
    await mod.widenForPanel();
    const src = row.querySelector<HTMLElement>(".row-text");
    if (!src || !row.isConnected || !mod.panelIdle()) return mod.openPanel(id, focus);
    const dst = mod.panelTitle();
    const from = src.getBoundingClientRect();
    const html = document.documentElement;
    src.style.viewTransitionName = "item-title";
    html.dataset.vt = "";
    const t = document.startViewTransition(async () => {
      src.style.viewTransitionName = "";
      dst.style.viewTransitionName = "item-title";
      await mod.openPanel(id, focus);
    });
    morph = t;
    let byUser = false;
    const userSkip = () => {
      byUser = true;
      t.skipTransition();
    };
    document.addEventListener("keydown", userSkip, true);
    document.addEventListener("pointerdown", userSkip, true);
    // A window that gets no frames (hidden, occluded) never captures the old
    // state, and the panel would wait for the UA timeout: skip to the slide.
    const stall = window.setTimeout(() => t.skipTransition(), motionMs("--dur-panel"));
    void t.updateCallbackDone.finally(() => window.clearTimeout(stall)).catch(() => undefined);
    const cleanup = () => {
      src.style.viewTransitionName = "";
      document.removeEventListener("keydown", userSkip, true);
      document.removeEventListener("pointerdown", userSkip, true);
      if (morph !== t) return; // a newer morph owns the shared names
      morph = null;
      dst.style.viewTransitionName = "";
      delete html.dataset.vt;
    };
    t.ready.then(
      () => slideTitle(from, dst),
      () =>
        // Aborted: the panel is open without its slide (data-vt turned the
        // CSS transition off). Give it the slide unless the user skipped.
        void t.updateCallbackDone
          .catch(() => undefined)
          .then(() => {
            const current = morph === t;
            cleanup();
            if (!byUser && current && mod.panelItemId() === id) mod.slideIn();
          }),
    );
    void t.finished.catch(() => undefined).finally(cleanup);
    await t.updateCallbackDone.catch(() => undefined);
  }

  /**
   * Replaces the UA's morph of the title group (which animates width and
   * height) with a translate from the row's title to the panel's: only
   * transform moves, while the old and new title cross-fade at their own size.
   * The UA morph is cancelled only once the replacement is running, so an
   * engine that can't animate the pseudo-element keeps its own.
   */
  function slideTitle(from: DOMRect, dst: HTMLElement): void {
    const pseudo = "::view-transition-group(item-title)";
    const html = document.documentElement;
    const ua = html.getAnimations({ subtree: true }).filter((a) => (a.effect as KeyframeEffect | null)?.pseudoElement === pseudo);
    const to = dst.getBoundingClientRect();
    try {
      html.animate(
        [{ transform: `translate(${from.left}px, ${from.top}px)` }, { transform: `translate(${to.left}px, ${to.top}px)` }],
        { duration: motionMs("--dur-panel"), easing: easing("--ease-spring"), pseudoElement: pseudo },
      );
    } catch {
      return;
    }
    for (const a of ua) a.cancel();
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
  mountSyncPill(root.querySelector<HTMLElement>("#list-sync")!, {
    onStatus: (next, prev) => {
      const on = showMarkers(next);
      if (on !== markersOn) {
        markersOn = on;
        if (view && holds === 0) render();
      }
      // Which items are pending may have changed: refetch the view.
      if (on && needsRefetch(prev, next)) scheduleRefresh();
    },
  });
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
