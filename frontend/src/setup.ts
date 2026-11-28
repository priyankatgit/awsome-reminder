// First-run / unavailable data folder screen (route #setup). One column, one
// decision at a time: options, a confirm panel, the progress of a move, or the
// "your data folder isn't available" page. Copy follows the data-folder spec.
import { api, errorText, type SetupCheck, type SetupInfo, type SetupProgress } from "./api";
import { el } from "./dom";
import { icons } from "./icons";

type Kind = "standard" | "beside" | "other";

const CONFIRM_ADOPT = "confirm-adopt";
const CONFIRM_FRESH = "confirm-fresh";

const NETWORK_WARNING = "Network folders can damage the database if the connection drops.";
const CLOUD_WARNING = "Cloud-synced folders can damage the database or create conflicting copies. Sync across machines with Atlas instead.";
const CANCEL_WAIT_MS = 3000;
const SAME_FOLDER = "That is already the data folder.";

const STEP_LABELS: Record<string, string> = {
  database: "Copying your tasks…",
  attachments: "Copying images…",
  voice: "Copying voice files…",
  checking: "Checking the copy…",
};

/** "41.0 GB", "230 MB": binary units, as the Go side writes them (datamove.FormatBytes). */
export function formatBytes(n: number): string {
  const kb = 1024;
  const mb = kb * 1024;
  const gb = mb * 1024;
  if (n >= gb) return `${(n / gb).toFixed(1)} GB`;
  if (n >= mb) return `${Math.round(n / mb)} MB`;
  if (n >= kb) return `${Math.round(n / kb)} KB`;
  return `${n} B`;
}

/** Percent done of a progress report, 0 while the total is unknown. */
export function percent(p: SetupProgress): number {
  return p.total > 0 ? Math.max(0, Math.min(100, Math.round((p.done / p.total) * 100))) : 0;
}

function button(label: string, cls: string, onClick: () => void, icon?: string): HTMLButtonElement {
  const b = el("button", `btn ${cls}`);
  b.type = "button";
  if (icon) b.insertAdjacentHTML("afterbegin", icon);
  b.append(label);
  b.addEventListener("click", onClick);
  return b;
}

/** A warning or error line: icon plus text, never colour alone. */
function note(text: string, tone: "warn" | "error"): HTMLElement {
  const p = el("p", `setup-note is-${tone}`);
  p.insertAdjacentHTML("afterbegin", icons.warning);
  p.append(el("span", undefined, text));
  return p;
}

export function mountSetup(root: HTMLElement): void {
  document.documentElement.dataset.view = "setup";
  document.documentElement.dataset.backdrop = "solid";
  root.innerHTML = `<form class="setup-app" id="setup-form" novalidate>
  <main class="setup-main" id="setup-main"></main>
  <footer class="setup-foot" id="setup-foot"></footer>
</form>`;
  const form = root.querySelector<HTMLFormElement>("#setup-form")!;
  const main = root.querySelector<HTMLElement>("#setup-main")!;
  const foot = root.querySelector<HTMLElement>("#setup-foot")!;

  let info: SetupInfo | null = null;
  let view: "loading" | "options" | "unavailable" | "confirm" | "progress" | "error" = "loading";
  let sel: Kind = "standard";
  let other: SetupCheck | null = null; // the folder picked with Browse, once checked
  let last: { kind: Kind; path: string } | null = null; // what Continue last sent, for Try again
  let chooseErr = ""; // the last rejection of choose, shown on the page we return to
  let cancelling = false;
  let cancelTimer: ReturnType<typeof setTimeout> | undefined;

  // Pieces of the current view that change in place.
  let msgs: HTMLElement | null = null;
  let cont: HTMLButtonElement | null = null;
  let otherPath: HTMLElement | null = null;
  let otherFree: HTMLElement | null = null;
  let bar: HTMLElement | null = null;
  let progress: HTMLElement | null = null;
  let meta: HTMLElement | null = null;
  let cancelBtn: HTMLButtonElement | null = null;

  const pending = () => info?.state === "move";

  function show(v: typeof view): void {
    view = v;
    clearTimeout(cancelTimer);
    main.replaceChildren();
    foot.replaceChildren();
    msgs = cont = otherPath = otherFree = bar = progress = meta = cancelBtn = null;
  }

  // ---------- options (fresh and existing) ----------

  function optionPath(kind: Kind): string {
    if (kind === "other") return other?.path ?? "";
    return info?.options.find((o) => o.kind === kind)?.path ?? "";
  }

  function canContinue(): boolean {
    return sel !== "other" || (other !== null && other.err === "" && !other.same);
  }

  /** The warnings, errors and the last rejection that belong under the options. */
  function renderMsgs(): void {
    if (!msgs) return;
    msgs.replaceChildren();
    if (sel === "other" && other) {
      if (other.err) msgs.append(note(other.err, "error"));
      else if (other.same) msgs.append(note(SAME_FOLDER, "error"));
      else {
        if (other.network) msgs.append(note(NETWORK_WARNING, "warn"));
        if (other.cloud) msgs.append(note(CLOUD_WARNING, "warn"));
      }
    }
    if (chooseErr) msgs.append(note(chooseErr, "error"));
  }

  function refreshOptions(): void {
    renderMsgs();
    if (cont) cont.disabled = !canContinue();
    if (otherPath && otherFree) {
      otherPath.textContent = other ? other.path : "Choose a folder with Browse.";
      otherPath.classList.toggle("is-empty", !other);
      otherFree.textContent = other && other.free > 0 ? `${formatBytes(other.free)} free` : "";
    }
  }

  function optionRow(kind: Kind, label: string, aka: string): { row: HTMLElement; path: HTMLElement; free: HTMLElement } {
    const id = `setup-opt-${kind}`;
    const row = el("div", "setup-opt");
    const radio = el("input");
    radio.type = "radio";
    radio.name = "setup-where";
    radio.value = kind;
    radio.id = id;
    radio.checked = sel === kind;
    radio.addEventListener("change", () => {
      if (!radio.checked) return;
      sel = kind;
      chooseErr = "";
      refreshOptions();
    });
    radio.addEventListener("keydown", (e) => {
      if (e.key !== "Enter") return;
      e.preventDefault();
      void submitOptions();
    });
    const body = el("label", "setup-opt-body");
    body.htmlFor = id;
    const head = el("span", "setup-opt-head");
    head.append(el("span", "setup-opt-label", label));
    if (aka) head.append(el("span", "setup-opt-aka", aka));
    const detail = el("span", "setup-opt-detail");
    const path = el("span", "setup-opt-path");
    const free = el("span", "setup-opt-free");
    detail.append(path, free);
    body.append(head, detail);
    row.append(radio, body);
    return { row, path, free };
  }

  function showOptions(): void {
    if (!info) return;
    const existing = info.state === "existing";
    show("options");
    const title = el("h1", "setup-title", "Where should Awesome Reminder keep its data?");
    title.id = "setup-title";
    const need = existing && info.needText ? `about ${info.needText}` : "about 230 MB with voice";
    main.append(title, el("p", "setup-sub", `Tasks, images and voice files (${need}) all live in this one folder.`));
    if (existing) {
      const n = el("p", "setup-notice");
      n.insertAdjacentHTML("afterbegin", icons.info);
      const t = el("span");
      t.append("You already have data in ", el("span", "setup-mono", info.existingDir));
      n.append(t);
      main.append(n);
    }

    const group = el("div", "setup-options");
    group.setAttribute("role", "radiogroup");
    group.setAttribute("aria-labelledby", "setup-title");
    for (const o of info.options) {
      const kind = o.kind as Kind;
      const { row, path, free } = optionRow(kind, o.label, existing && kind === "standard" ? "Standard location" : "");
      path.textContent = o.path;
      free.textContent = o.freeText;
      group.append(row);
    }
    const o = optionRow("other", "Another folder…", "");
    otherPath = o.path;
    otherFree = o.free;
    o.row.classList.add("has-action");
    o.row.append(button("Browse", "btn-ghost", () => void browse(), icons.folderOpen));
    group.append(o.row);
    main.append(group);

    msgs = el("div", "setup-msgs");
    msgs.setAttribute("role", "status");
    main.append(msgs);

    cont = el("button", "btn btn-primary", "Continue");
    cont.type = "submit";
    foot.append(cont);
    refreshOptions();
    group.querySelector<HTMLInputElement>("input:checked")?.focus();
  }

  /** Browse: pick, check, show the resulting path and its free space. */
  async function browse(): Promise<void> {
    const picked = await api.chooseFolder("Choose the data folder");
    if (picked === "") return;
    try {
      other = await api.setup.check(picked);
    } catch (err) {
      other = { path: picked, free: 0, need: 0, hasData: false, network: false, cloud: false, same: false, err: errorText(err) };
    }
    sel = "other";
    chooseErr = "";
    const radio = main.querySelector<HTMLInputElement>("#setup-opt-other");
    if (radio) radio.checked = true;
    refreshOptions();
  }

  async function submitOptions(): Promise<void> {
    if (view !== "options" || !cont || cont.disabled || !canContinue()) return;
    chooseErr = "";
    await submit(sel, optionPath(sel), false);
  }

  /** Sends the choice. A move switches to progress; anything else restarts the app. */
  async function submit(kind: Kind, path: string, confirm: boolean): Promise<void> {
    last = { kind, path };
    if (cont) cont.disabled = true;
    try {
      await api.setup.choose(kind, path, confirm);
    } catch (err) {
      const text = errorText(err);
      if (text.includes(CONFIRM_ADOPT)) return showConfirm("adopt", kind, path);
      if (text.includes(CONFIRM_FRESH)) return showConfirm("fresh", kind, path);
      chooseErr = text;
      return back();
    }
    // Adopting needs a confirm, so an unconfirmed choice that went through
    // from the existing-data page is the copy.
    if (!confirm && info?.state === "existing" && kind !== "standard") {
      // A fast failure may already have shown its error: only the pre-move pages become progress.
      if (view === "options" || view === "confirm" || view === "loading") showProgress(path);
      return;
    }
    starting();
  }

  /** The folder is back and Go is restarting the app: a quiet page until the window closes. */
  function showStarting(): void {
    show("loading");
    const h = el("h1", "setup-title", "Starting Awesome Reminder…");
    h.setAttribute("role", "status");
    main.append(h);
  }

  /** The app is restarting itself; keep the page quiet until the window closes. */
  function starting(): void {
    if (cont) {
      cont.disabled = true;
      cont.textContent = "Starting…";
    }
    for (const b of foot.querySelectorAll<HTMLButtonElement>("button")) b.disabled = true;
  }

  function back(): void {
    if (info?.state === "unavailable") showUnavailable();
    else showOptions();
  }

  // ---------- confirm ----------

  function showConfirm(what: "adopt" | "fresh", kind: Kind, path: string): void {
    show("confirm");
    const adopt = what === "adopt";
    main.append(el("h1", "setup-title", adopt ? "This folder already has Awesome Reminder data." : "This folder has no Awesome Reminder data."));
    if (adopt) {
      const hasOther = info?.state !== "fresh"; // nothing else to keep on a first run
      main.append(el("p", "setup-sub", hasOther ? "Use that data? Your other data stays where it is." : "Use that data?"));
    } else {
      main.append(el("p", "setup-sub", "Start with an empty list here?"));
    }
    main.append(el("p", "setup-path", path === "" ? "" : path));
    if (adopt) {
      const n = el("p", "setup-notice");
      n.insertAdjacentHTML("afterbegin", icons.info);
      n.append(el("span", undefined, "Sync across machines will be off until you connect again."));
      main.append(n);
    }
    const primary = button(adopt ? "Use that data" : "Start with an empty list", "btn-primary", () => {
      primary.disabled = true;
      void submit(kind, path, true);
    });
    foot.append(button("Back", "btn-ghost", back), primary);
    primary.focus();
  }

  // ---------- progress of a move ----------

  function showProgress(target: string): void {
    show("progress");
    cancelling = false;
    main.append(el("h1", "setup-title", "Moving your data"));
    main.append(el("p", "setup-sub", `to ${target}`));
    const panel = el("div", "setup-panel");
    progress = el("div", "cap-progress");
    progress.setAttribute("role", "progressbar");
    progress.setAttribute("aria-label", STEP_LABELS.database);
    progress.setAttribute("aria-valuemin", "0");
    progress.setAttribute("aria-valuemax", "100");
    progress.setAttribute("aria-valuenow", "0");
    bar = el("span", "cap-progress-bar");
    bar.style.transform = "scaleX(0)";
    progress.append(bar);
    meta = el("p", "cap-voice-meta setup-step", STEP_LABELS.database);
    meta.setAttribute("role", "status");
    panel.append(progress, meta);
    main.append(panel);
    cancelBtn = button("Cancel", "btn-ghost", cancelMove);
    foot.append(cancelBtn);
  }

  function update(p: SetupProgress): void {
    if (view !== "progress") showProgress(last?.path ?? info?.moveTo ?? "");
    const label = STEP_LABELS[p.step] ?? STEP_LABELS.database;
    const pct = percent(p);
    progress?.setAttribute("aria-valuenow", String(pct));
    progress?.setAttribute("aria-label", label);
    if (bar) bar.style.transform = `scaleX(${pct / 100})`;
    if (meta) meta.textContent = label;
  }

  function cancelMove(): void {
    cancelling = true;
    const btn = cancelBtn;
    if (btn) btn.disabled = true;
    void api.setup.cancelMove().catch(() => undefined);
    // If nothing stops (CancelMove had nothing to cancel), give the button back.
    clearTimeout(cancelTimer);
    cancelTimer = setTimeout(() => {
      if (!cancelling || view !== "progress") return;
      cancelling = false;
      if (btn) btn.disabled = false;
    }, CANCEL_WAIT_MS);
  }

  function moveFailed(text: string): void {
    // The user stopped it: a pending move goes back to the old folder, a first-run move back to the options.
    if (cancelling) {
      cancelling = false;
      if (pending()) return abandon();
      return showOptions();
    }
    show("error");
    main.append(el("h1", "setup-title", "Couldn't move your data"));
    const p = el("p", "setup-note is-error");
    p.insertAdjacentHTML("afterbegin", icons.warning);
    p.append(el("span", undefined, text || "Something went wrong."));
    p.setAttribute("role", "alert");
    main.append(p);
    main.append(el("p", "setup-sub", "Your data in the current folder is untouched."));
    const again = button("Try again", "btn-primary", () => {
      if (pending()) {
        showProgress(info?.moveTo ?? "");
        void runPending();
      } else if (last) {
        showProgress(last.path);
        void submit(last.kind, last.path, false);
      }
    });
    foot.append(button("Cancel", "btn-ghost", () => (pending() ? abandon() : showOptions())), again);
    again.focus();
  }

  function abandon(): void {
    api.setup.abandonMove().catch((err) => moveFailed(errorText(err)));
  }

  async function runPending(): Promise<void> {
    try {
      await api.setup.runPendingMove();
    } catch (err) {
      moveFailed(errorText(err));
    }
  }

  // ---------- unavailable ----------

  function showUnavailable(note2 = ""): void {
    if (!info) return;
    show("unavailable");
    main.append(el("h1", "setup-title", "Your data folder isn't available"));
    main.append(el("p", "setup-path", info.root));
    main.append(el("p", "setup-sub", "The drive may be disconnected, or the folder was moved or renamed."));
    const reason = el("p", "setup-reason", [info.reason, note2].filter(Boolean).join(" "));
    main.append(reason);
    msgs = el("div", "setup-msgs");
    msgs.setAttribute("role", "status");
    if (chooseErr) msgs.append(note(chooseErr, "error"));
    main.append(msgs);

    const retry = button("Retry", "btn-primary", () => void doRetry(retry));
    foot.append(
      retry,
      button("Choose another folder…", "btn-ghost", () => void chooseOther(), icons.folderOpen),
      button("Quit", "btn-ghost", () => void api.setup.quit()),
    );
    retry.focus();
  }

  async function doRetry(btn: HTMLButtonElement): Promise<void> {
    btn.disabled = true;
    chooseErr = "";
    try {
      info = await api.setup.retry();
    } catch (err) {
      chooseErr = errorText(err);
      return showUnavailable();
    }
    if (info.state === "unavailable") showUnavailable("Still can't reach it.");
    else if (info.state === "ready") showStarting();
    else route(); // it is back: Go restarts the app; anything else shows its own page
  }

  async function chooseOther(): Promise<void> {
    const picked = await api.chooseFolder("Choose the data folder");
    if (picked === "") return;
    let c: SetupCheck;
    try {
      c = await api.setup.check(picked);
    } catch (err) {
      chooseErr = errorText(err);
      return showUnavailable();
    }
    chooseErr = c.err || (c.same ? SAME_FOLDER : "");
    if (chooseErr) return showUnavailable();
    other = c;
    await submit("other", c.path, false);
  }

  // ---------- start ----------

  function route(): void {
    if (!info) return;
    if (info.state === "unavailable") showUnavailable();
    else if (info.state === "move") {
      showProgress(info.moveTo);
      void runPending();
    } else {
      const std = info.options.find((o) => o.kind === "standard");
      sel = std ? "standard" : (info.options[0]?.kind as Kind | undefined) ?? "other";
      showOptions();
    }
  }

  form.addEventListener("submit", (e) => {
    e.preventDefault();
    void submitOptions();
  });
  api.setup.onProgress(update);
  api.setup.onDone((d) => {
    if (d.ok) return starting();
    moveFailed(d.error);
  });

  void api.setup
    .info()
    .then((i) => {
      info = i;
      route();
    })
    .catch((err) => {
      show("error");
      main.append(el("h1", "setup-title", "Couldn't load the setup page"));
      main.append(note(errorText(err), "error"));
      foot.append(button("Quit", "btn-ghost", () => void api.setup.quit()));
    });
}
