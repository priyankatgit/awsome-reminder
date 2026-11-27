// Alert popup (#alert): the reminder cards of the one alert window, newest
// first. Layout and behaviour follow the "Alert popup" section of the UI
// guide. Go owns the queue (internal/app/alerts.go): the page pulls it on
// load and gets the whole queue again with every alerts:show event.
import { api, errorText, events, on, type AlertCard, type SnoozePreset } from "./api";
import { el, motionMs, tagChip } from "./dom";
import { icons } from "./icons";
import { agoLabel, timeLabel } from "./item-row";

/** Cards shown before "+N more". */
const MAX_VISIBLE = 3;
const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
/** An at-time alert reads "now" until this long after the due time. */
const NOW_WINDOW = 2 * MINUTE;
const TICK_MS = 30_000;

const SNOOZES: { preset: SnoozePreset; label: string }[] = [
  { preset: "10m", label: "10 min" },
  { preset: "1h", label: "1 hour" },
  { preset: "tomorrow", label: "Tomorrow" },
];

export type WhenState = "soon" | "now" | "missed" | "snoozed" | "overdue";

/** "10 min", "1 h 30 min", "2 d": a time span rounded to minutes. */
export function spanLabel(ms: number): string {
  const min = Math.max(1, Math.round(ms / MINUTE));
  if (min < 60) return `${min} min`;
  if (min < 24 * 60) {
    const h = Math.floor(min / 60);
    const m = min % 60;
    return m ? `${h} h ${m} min` : `${h} h`;
  }
  return `${Math.round(ms / DAY)} d`;
}

/**
 * The card's time line: "in 10 min · 17:00", "now · 17:00",
 * "missed · was due 14:00", "snoozed · was due 14:00" or
 * "overdue 5m · 14:00". It is recomputed every tick, so it stays true while
 * the card waits.
 */
export function whenLabel(c: AlertCard, now: Date): { text: string; state: WhenState } {
  const due = new Date(c.dueAt);
  const at = timeLabel(due, now);
  const diff = due.getTime() - now.getTime();
  if (diff >= MINUTE / 2) return { text: `in ${spanLabel(diff)} · ${at}`, state: "soon" };
  if (c.missed) return { text: `missed · was due ${at}`, state: "missed" };
  if (-diff <= NOW_WINDOW) return { text: `now · ${at}`, state: "now" };
  if (c.snoozed) return { text: `snoozed · was due ${at}`, state: "snoozed" };
  return { text: `overdue ${agoLabel(due, now)} · ${at}`, state: "overdue" };
}

const TEMPLATE = `
<main class="alert-app" id="alert-app" aria-label="Reminders">
  <div class="alert-content" id="alert-content">
    <div class="alert-stack" id="alert-stack" aria-live="polite"></div>
    <div class="alert-foot" id="alert-foot" hidden>
      <button class="btn btn-ghost alert-more" id="alert-more" type="button" aria-expanded="false" aria-controls="alert-stack"></button>
      <button class="btn btn-ghost alert-clear" id="alert-clear" type="button">Dismiss all</button>
    </div>
  </div>
</main>`;

interface CardView {
  card: AlertCard;
  node: HTMLElement;
  when: HTMLElement;
  error: HTMLElement;
  snoozeBtn: HTMLButtonElement;
  snoozeRow: HTMLElement;
  leaving: boolean;
}

let seq = 0;

export function mountAlert(root: HTMLElement): void {
  document.documentElement.dataset.view = "alert";
  root.innerHTML = TEMPLATE;
  const content = root.querySelector<HTMLElement>("#alert-content")!;
  const stack = root.querySelector<HTMLElement>("#alert-stack")!;
  const foot = root.querySelector<HTMLElement>("#alert-foot")!;
  const more = root.querySelector<HTMLButtonElement>("#alert-more")!;
  const clear = root.querySelector<HTMLButtonElement>("#alert-clear")!;

  let queue: AlertCard[] = [];
  let expanded = false;
  let loaded = false;
  const views = new Map<string, CardView>();

  const reportHeight = () => {
    if (loaded) void api.resizeAlerts(Math.ceil(content.getBoundingClientRect().height));
  };

  function buildCard(card: AlertCard): CardView {
    const id = `alert-${++seq}`;
    const node = el("article", "alert-card");
    node.dataset.key = card.key;
    node.setAttribute("aria-labelledby", `${id}-text`);
    node.setAttribute("aria-describedby", `${id}-when`);

    const head = el("div", "alert-head");
    const bell = el("span", "alert-bell");
    bell.innerHTML = icons.bell;
    const when = el("span", "alert-when");
    when.id = `${id}-when`;
    const close = el("button", "icon-btn alert-close");
    close.type = "button";
    close.setAttribute("aria-label", `Dismiss: ${card.text}`);
    close.innerHTML = icons.close;
    head.append(bell, when, close);

    const text = el("p", "alert-text", card.text);
    text.id = `${id}-text`;
    text.title = card.text;

    const meta = el("div", "alert-meta");
    for (const t of card.tags.filter((t) => t !== "inbox")) meta.append(tagChip(t));
    if (card.recurring) {
      const rep = el("span", "alert-repeat");
      rep.innerHTML = icons.repeat;
      rep.append(el("span", undefined, "repeats"));
      meta.append(rep);
    }

    const error = el("p", "alert-error");
    error.setAttribute("role", "alert");
    error.hidden = true;

    const actions = el("div", "alert-actions");
    const done = el("button", "btn btn-primary alert-btn");
    done.type = "button";
    done.innerHTML = icons.check;
    done.append(el("span", undefined, "Done"));
    const snoozeBtn = el("button", "btn btn-ghost alert-btn");
    snoozeBtn.type = "button";
    snoozeBtn.setAttribute("aria-expanded", "false");
    snoozeBtn.setAttribute("aria-controls", `${id}-snooze`);
    snoozeBtn.append(el("span", undefined, "Snooze"));
    snoozeBtn.insertAdjacentHTML("beforeend", icons.chevronDown);
    const open = el("button", "btn btn-ghost alert-btn");
    open.type = "button";
    open.append(el("span", undefined, "Open"));
    actions.append(done, snoozeBtn, open);

    const snoozeRow = el("div", "alert-snooze");
    snoozeRow.id = `${id}-snooze`;
    snoozeRow.setAttribute("role", "group");
    snoozeRow.setAttribute("aria-label", `Snooze ${card.text}`);
    snoozeRow.hidden = true;
    snoozeRow.append(el("span", "alert-snooze-label", "Snooze for"));
    for (const s of SNOOZES) {
      const b = el("button", "btn btn-ghost alert-btn", s.label);
      b.type = "button";
      b.addEventListener("click", () => void act(v, () => api.snooze(card.itemId, s.preset), true));
      snoozeRow.append(b);
    }

    node.append(head, text);
    if (meta.childElementCount) node.append(meta);
    node.append(error, actions, snoozeRow);

    const v: CardView = { card, node, when, error, snoozeBtn, snoozeRow, leaving: false };
    done.addEventListener("click", () => void act(v, () => api.done(card.itemId), true));
    snoozeBtn.addEventListener("click", () => toggleSnooze(v));
    open.addEventListener("click", () => {
      void api.openList(card.itemId).catch(() => undefined);
      void act(v, () => api.dismissAlert(card.key), false);
    });
    close.addEventListener("click", () => void act(v, () => api.dismissAlert(card.key), false));
    node.addEventListener("keydown", (e) => {
      if (e.key !== "Escape") return;
      e.preventDefault();
      if (!v.snoozeRow.hidden) {
        toggleSnooze(v, false);
        v.snoozeBtn.focus();
      } else {
        void act(v, () => api.dismissAlert(card.key), false);
      }
    });
    return v;
  }

  function toggleSnooze(v: CardView, show = v.snoozeRow.hidden): void {
    v.snoozeRow.hidden = !show;
    v.snoozeBtn.setAttribute("aria-expanded", String(show));
    if (show) v.snoozeRow.querySelector<HTMLButtonElement>("button")?.focus();
  }

  /**
   * Runs a card action. The card (and, for item actions, every card of the
   * same item) fades out while Go applies it; Go then sends the new queue.
   * On failure the card comes back with the error.
   */
  async function act(v: CardView, call: () => Promise<void>, wholeItem: boolean): Promise<void> {
    if (v.leaving) return;
    const targets = [...views.values()].filter((o) => o === v || (wholeItem && o.card.itemId === v.card.itemId));
    const hadFocus = targets.some((t) => t.node.contains(document.activeElement));
    targets.forEach((t) => {
      t.leaving = true;
      t.node.classList.add("is-leaving");
    });
    const fade = new Promise((r) => window.setTimeout(r, motionMs("--dur-exit")));
    try {
      await call();
      await fade;
      const gone = new Set(targets.map((t) => t.card.key));
      queue = queue.filter((c) => !gone.has(c.key));
      render(hadFocus);
    } catch (err) {
      await fade;
      targets.forEach((t) => {
        t.leaving = false;
        t.node.classList.remove("is-leaving");
      });
      v.error.textContent = errorText(err);
      v.error.hidden = false;
      reportHeight();
    }
  }

  function refreshTimes(): void {
    const now = new Date();
    for (const v of views.values()) {
      const w = whenLabel(v.card, now);
      v.when.textContent = w.text;
      v.node.dataset.when = w.state;
    }
  }

  /** Renders the queue newest first, reusing card nodes by key. */
  function render(restoreFocus = false): void {
    const app = root.querySelector("#alert-app")!;
    const focusWasIn = app.contains(document.activeElement);
    const live = new Set(queue.map((c) => c.key));
    for (const [key, v] of views) {
      if (!live.has(key)) {
        v.node.remove();
        views.delete(key);
      }
    }
    const ordered = [...queue].reverse();
    if (ordered.length <= MAX_VISIBLE) expanded = false;
    const shown = expanded ? ordered : ordered.slice(0, MAX_VISIBLE);
    let prev: HTMLElement | null = null;
    for (const card of shown) {
      let v = views.get(card.key);
      if (!v) {
        v = buildCard(card);
        views.set(card.key, v);
        v.node.classList.add("is-entering");
        v.node.addEventListener("animationend", () => v!.node.classList.remove("is-entering"), { once: true });
      }
      v.card = card;
      const want: ChildNode | null = prev ? prev.nextSibling : stack.firstChild;
      if (want !== v.node) stack.insertBefore(v.node, want);
      prev = v.node;
    }
    // Cards beyond the visible ones are detached but kept, so expanding
    // doesn't replay their entrance.
    const shownKeys = new Set(shown.map((c) => c.key));
    for (const [key, v] of views) {
      if (!shownKeys.has(key) && v.node.isConnected) v.node.remove();
    }
    const hidden = ordered.length - shown.length;
    foot.hidden = ordered.length < 2;
    more.hidden = ordered.length <= MAX_VISIBLE;
    more.textContent = expanded ? "Show less" : `+${hidden} more`;
    more.setAttribute("aria-expanded", String(expanded));
    refreshTimes();
    // A removed card or the hidden "+N more" took focus with it.
    if (restoreFocus || (focusWasIn && !app.contains(document.activeElement)) || (more.hidden && document.activeElement === more)) {
      const first = stack.querySelector<HTMLButtonElement>(".alert-card .btn-primary");
      (first ?? (foot.hidden ? null : more))?.focus({ preventScroll: true });
    }
    // Report now: while the window is hidden, WebView2 runs no animation
    // frames or ResizeObserver callbacks, and Go shows the window only after
    // this report. getBoundingClientRect forces layout even when hidden.
    // The next frame reports again in case fonts or styles settle late.
    reportHeight();
    requestAnimationFrame(reportHeight);
  }

  function setQueue(q: AlertCard[] | null): void {
    queue = (q ?? []).map((c) => ({ ...c, tags: c.tags ?? [] }));
    render();
  }

  more.addEventListener("click", () => {
    expanded = !expanded;
    render();
    more.focus({ preventScroll: true });
  });
  clear.addEventListener("click", () => void api.dismissAlerts().catch(() => undefined));

  on<AlertCard[] | null>(events.alertsShow, (q) => {
    loaded = true;
    setQueue(q);
  });
  window.setInterval(refreshTimes, TICK_MS);
  // Expanding, the snooze row, an error or font loading change the height.
  new ResizeObserver(reportHeight).observe(content);

  void api
    .pendingAlerts()
    .then((q) => {
      loaded = true;
      setQueue(q);
    })
    .catch(() => {
      loaded = true;
      setQueue([]);
    });
}
