// The sync status pill in the list header and its details popover. The pure
// wording and tones live in sync-status.ts; this file renders them.
import { api, onSyncStatus, type SyncStatus } from "./api";
import { el, motionMs } from "./dom";
import { icons } from "./icons";
import { pillModel, relTime, shouldGlow, type PillModel } from "./sync-status";

const TICK_MS = 30_000; // relative times re-render this often

export interface SyncPillHandle {
  /** The latest status, or null before the first answer / without a backend. */
  current(): SyncStatus | null;
  /** Closes the popover without moving focus. */
  close(): void;
}

function pad(n: number): string {
  return String(n).padStart(2, "0");
}

function waiting(n: number): string {
  return n === 1 ? "1 change waiting" : `${n} changes waiting`;
}

/** The state without the clock: what a screen reader hears on a change. */
function stateLine(st: SyncStatus): string {
  switch (st.state) {
    case "syncing":
      return "Syncing…";
    case "synced":
      return st.pending > 0 ? `Synced · ${st.pending} waiting` : "All synced";
    case "offline":
      return "Offline";
    case "error":
      return "Sync error";
  }
  return "";
}

/**
 * Mounts the pill into slot (an empty element). onStatus runs after every
 * status change with the new and the previous status.
 */
export function mountSyncPill(
  slot: HTMLElement,
  opts: { onStatus: (next: SyncStatus, prev: SyncStatus | null) => void },
): SyncPillHandle {
  const POP_ID = "sync-popover";
  slot.classList.add("sync");
  slot.hidden = true;

  const pill = el("button", "sync-pill");
  pill.type = "button";
  pill.setAttribute("aria-expanded", "false");
  pill.setAttribute("aria-controls", POP_ID);
  const pillIcon = el("span", "sync-pill-icon");
  pillIcon.setAttribute("aria-hidden", "true");
  const pillText = el("span", "sync-pill-text");
  pill.append(pillIcon, pillText);

  const live = el("span", "sr-only");
  live.setAttribute("role", "status");

  const pop = el("div", "sync-pop");
  pop.id = POP_ID;
  pop.setAttribute("role", "group");
  pop.setAttribute("aria-label", "Sync details");
  pop.tabIndex = -1;
  pop.hidden = true;
  const popState = el("p", "sync-pop-state");
  const popLast = el("p", "sync-pop-row");
  const popWaiting = el("p", "sync-pop-row");
  const popError = el("p", "sync-pop-row sync-pop-error");
  const popWarnings = el("div", "sync-pop-warnings");
  const popHost = el("p", "sync-pop-row sync-pop-host");
  const actions = el("div", "sync-pop-actions");
  const nowBtn = el("button", "btn btn-primary", "Sync now");
  nowBtn.type = "button";
  const settingsBtn = el("button", "btn btn-ghost", "Sync settings");
  settingsBtn.type = "button";
  actions.append(nowBtn, settingsBtn);
  pop.append(popState, popLast, popWaiting, popError, popWarnings, popHost, actions);

  slot.append(pill, live, pop);

  let st: SyncStatus | null = null;
  let model: PillModel | null = null;
  let announced = "";
  let glowTimer: number | undefined;

  function render(): void {
    if (!st) return;
    const now = new Date();
    model = pillModel(st, now);
    slot.hidden = model === null;
    if (!model) {
      close(false);
      announced = "";
      return;
    }
    pill.dataset.tone = model.tone;
    pill.classList.toggle("is-spin", model.spin);
    if (pillText.textContent !== model.text) pillText.textContent = model.text;
    const iconKey = `${model.icon}`;
    if (pillIcon.dataset.icon !== iconKey) {
      pillIcon.dataset.icon = iconKey;
      pillIcon.innerHTML = icons[model.icon];
    }

    const line = stateLine(st);
    if (line !== announced) {
      announced = line;
      live.textContent = line;
    }

    popState.textContent = line;
    popLast.textContent = st.lastSyncAt
      ? `Last synced ${pad(new Date(st.lastSyncAt).getHours())}:${pad(new Date(st.lastSyncAt).getMinutes())} (${relTime(now, new Date(st.lastSyncAt))})`
      : "Not synced yet";
    popWaiting.hidden = st.pending <= 0;
    popWaiting.textContent = waiting(st.pending);
    popError.hidden = st.error === "";
    popError.textContent = st.error;
    popWarnings.replaceChildren(...st.warnings.map((w) => el("p", "sync-pop-row sync-pop-warning", w)));
    popHost.hidden = st.host === "";
    popHost.textContent = `Cluster ${st.host}`;
    nowBtn.disabled = st.state === "syncing";
  }

  function glow(): void {
    const ms = motionMs("--dur-sync-glow");
    if (ms <= 0) return;
    window.clearTimeout(glowTimer);
    pill.classList.remove("is-glow");
    void pill.offsetWidth; // restart the animation
    pill.classList.add("is-glow");
    glowTimer = window.setTimeout(() => pill.classList.remove("is-glow"), ms + 50);
  }

  function isOpen(): boolean {
    return !pop.hidden;
  }

  function open(): void {
    pop.hidden = false;
    pill.setAttribute("aria-expanded", "true");
    (nowBtn.disabled ? pop : nowBtn).focus({ preventScroll: true });
  }

  function close(refocus: boolean): void {
    if (!isOpen()) return;
    pop.hidden = true;
    pill.setAttribute("aria-expanded", "false");
    if (refocus) pill.focus({ preventScroll: true });
  }

  pill.addEventListener("click", () => (isOpen() ? close(true) : open()));
  nowBtn.addEventListener("click", () => void api.sync.now().catch(() => {}));
  settingsBtn.addEventListener("click", () => {
    close(true);
    void api.openSettings().catch(() => {});
  });

  // The list's own key handling must not see keys typed in the pill or popover.
  slot.addEventListener("keydown", (e) => {
    e.stopPropagation();
    if (e.key === "Escape" && isOpen()) {
      e.preventDefault();
      close(true);
    }
  });
  slot.addEventListener("focusout", (e) => {
    const to = e.relatedTarget;
    if (isOpen() && to instanceof Node && !slot.contains(to)) close(false);
  });
  document.addEventListener("pointerdown", (e) => {
    if (isOpen() && e.target instanceof Node && !slot.contains(e.target)) close(false);
  });
  window.addEventListener("blur", () => close(false));

  function apply(next: SyncStatus): void {
    const prev = st;
    st = next;
    render();
    if (shouldGlow(prev, next)) glow();
    opts.onStatus(next, prev);
  }

  onSyncStatus(apply);
  window.setInterval(render, TICK_MS);
  api.sync
    .status()
    .then((s) => {
      if (!st) apply(s); // an event that arrived first is newer
    })
    .catch(() => {});

  return { current: () => st, close: () => close(false) };
}
