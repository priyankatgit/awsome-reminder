// Settings › "Sync across machines". The pure helpers are tested; the section
// itself sits outside the Save/dirty flow: its buttons act at once. The typed
// connection string lives only in the input and the connect call.
import { api, errorText, onSyncStatus, type SyncInfo, type SyncStatus } from "./api";
import { el } from "./dom";
import { icons } from "./icons";
import { pillModel } from "./sync-status";

/** The Atlas free tier's storage, for the "Images in Atlas" line. */
const ATLAS_FREE_MB = 512;
const SETUP_GUIDE_URL = "https://github.com/priyankatgit/awsome-reminder/blob/master/docs/atlas-setup.md";
/** secret.Store.Kind() of the encrypted-file fallback. */
const FALLBACK_STORE = "Encrypted file on this computer";
const TICK_MS = 30_000;

/** True for a string that starts like a MongoDB connection string (after trimming). */
export function looksLikeMongoURI(s: string): boolean {
  const t = s.trim();
  return t.startsWith("mongodb+srv://") || t.startsWith("mongodb://");
}

/** "Images in Atlas: 12.4 MB of 512 MB" (decimal MB), or "unknown" for a negative size. */
export function storageLine(bytes: number): string {
  if (!(bytes >= 0)) return "Images in Atlas: unknown";
  return `Images in Atlas: ${(bytes / 1e6).toFixed(1)} MB of ${ATLAS_FREE_MB} MB`;
}

const HELP_URI = "Paste the mongodb+srv:// string from Atlas. Use a database user with readWrite on awesome_reminder only. ";
const HELP_FREE =
  "Free clusters can pause after a long time unused; resume it in Atlas if sync says it can't reach the cluster. " +
  "Atlas must allow this computer's IP (0.0.0.0/0 for laptops that move between networks; the strong password protects it).";

/** Mounts the section into host (an empty element after the settings form). */
export function mountSyncSection(host: HTMLElement): void {
  host.setAttribute("aria-labelledby", "sync-title");
  const title = el("h2", "sync-title", "Sync across machines");
  title.id = "sync-title";
  host.append(title);

  // ----- Not connected -----
  const off = el("div", "field");
  const uriLabel = el("label", "field-label", "Atlas connection string");
  uriLabel.htmlFor = "sync-uri";
  const box = el("div", "secret-box");
  const uri = el("input", "input secret-input");
  uri.id = "sync-uri";
  uri.type = "password";
  uri.autocomplete = "off";
  uri.spellcheck = false;
  uri.setAttribute("aria-describedby", "sync-uri-help sync-uri-error");
  const eye = el("button", "btn btn-ghost secret-toggle");
  eye.type = "button";
  eye.setAttribute("aria-label", "Show connection string");
  eye.setAttribute("aria-pressed", "false");
  eye.innerHTML = icons.eye;
  box.append(uri, eye);
  const uriHelp = el("p", "field-help", HELP_URI);
  uriHelp.id = "sync-uri-help";
  const guide = el("a", "link", "Setup guide");
  guide.href = SETUP_GUIDE_URL;
  uriHelp.append(guide);
  const uriError = el("p", "field-error");
  uriError.id = "sync-uri-error";
  uriError.setAttribute("role", "alert");
  uriError.hidden = true;
  const connect = el("button", "btn btn-primary", "Connect");
  connect.type = "button";
  connect.setAttribute("aria-disabled", "true");
  const connectRow = el("div", "field-row");
  connectRow.append(connect);
  off.append(uriLabel, box, uriHelp, uriError, connectRow);

  // ----- Connected -----
  const on = el("div", "field");
  const line = el("p", "sync-line");
  const lineIcon = el("span", "sync-line-icon");
  lineIcon.setAttribute("aria-hidden", "true");
  const lineText = el("span");
  line.append(lineIcon, lineText);
  const lastError = el("p", "field-help");
  const cluster = el("p", "sync-fact");
  const store = el("p", "sync-fact");
  const storeNote = el("p", "field-help");
  const storage = el("p", "sync-fact");
  const warnings = el("div", "sync-warnings");
  const actions = el("div", "field-row");
  const nowBtn = el("button", "btn btn-primary", "Sync now");
  nowBtn.type = "button";
  const discBtn = el("button", "btn btn-ghost", "Disconnect");
  discBtn.type = "button";
  actions.append(nowBtn, discBtn);
  const confirm = el("div", "sync-confirm");
  confirm.setAttribute("role", "group");
  confirm.setAttribute("aria-label", "Confirm disconnect");
  confirm.hidden = true;
  const confirmText = el("p", "sync-confirm-text", "Disconnect this computer? Your items stay here and Atlas keeps its copy.");
  const confirmRow = el("div", "field-row");
  const confirmYes = el("button", "btn btn-ghost btn-danger-text", "Disconnect");
  confirmYes.type = "button";
  const confirmNo = el("button", "btn btn-ghost", "Cancel");
  confirmNo.type = "button";
  confirmRow.append(confirmYes, confirmNo);
  confirm.append(confirmText, confirmRow);
  const actionError = el("p", "field-error");
  actionError.setAttribute("role", "alert");
  actionError.hidden = true;
  on.append(line, lastError, cluster, store, storeNote, storage, warnings, actions, confirm, actionError);
  on.hidden = true;

  const help = el("p", "field-help", HELP_FREE);
  host.append(off, on, help);

  let connected = false;
  let status: SyncStatus | null = null;
  let info: SyncInfo | null = null;
  let busy = false; // connecting
  let syncing = false; // a Sync now call is running
  let lastSeen: string | null = null;

  const setError = (target: HTMLElement, text: string) => {
    target.textContent = text;
    target.hidden = text === "";
  };

  function setDisabled(b: HTMLButtonElement, v: boolean): void {
    if (v) b.setAttribute("aria-disabled", "true");
    else b.removeAttribute("aria-disabled");
  }
  const isDisabled = (b: HTMLElement) => b.getAttribute("aria-disabled") === "true";

  function renderOff(): void {
    // aria-disabled, not disabled: the focused button keeps focus.
    setDisabled(connect, busy || !looksLikeMongoURI(uri.value));
    connect.textContent = busy ? "Connecting…" : "Connect";
    connect.setAttribute("aria-busy", String(busy));
    if (uriError.hidden) uri.removeAttribute("aria-invalid");
    else uri.setAttribute("aria-invalid", "true");
  }

  function renderStatusLine(): void {
    const m = status ? pillModel(status, new Date()) : null;
    line.dataset.tone = m?.tone ?? "neutral";
    line.classList.toggle("is-spin", !!m?.spin);
    lineIcon.innerHTML = m ? icons[m.icon] : icons.cloud;
    lineText.textContent = m?.text ?? "Connected";
    const err = status && (status.state === "error" || status.state === "offline") ? status.error : "";
    lastError.textContent = err;
    lastError.hidden = !err;
    lastError.className = status?.state === "error" ? "field-error" : "field-help";
    warnings.replaceChildren(
      ...(status?.warnings ?? []).map((w) => {
        const p = el("p", "sync-warning");
        const i = el("span", "sync-warning-icon");
        i.setAttribute("aria-hidden", "true");
        i.innerHTML = icons.warning;
        p.append(i, el("span", undefined, w));
        return p;
      }),
    );
    setDisabled(nowBtn, syncing || status?.state === "syncing");
  }

  function renderInfo(): void {
    cluster.textContent = `Cluster ${info?.host || status?.host || ""}`;
    const kind = info?.secretStore ?? "";
    store.textContent = kind ? `Connection string stored in ${kind}` : "";
    store.hidden = !kind;
    const fallback = kind === FALLBACK_STORE;
    storeNote.textContent = fallback ? "Encrypted with a key kept on this computer; anyone with access to your account can read it." : "";
    storeNote.hidden = !fallback;
    storage.textContent = storageLine(info?.attachmentBytes ?? -1);
  }

  function showView(): void {
    off.hidden = connected;
    on.hidden = !connected;
  }

  /** Re-reads the facts (host, secret store, storage); "unknown" storage on failure. */
  async function refreshInfo(): Promise<void> {
    try {
      info = await api.sync.info();
    } catch {
      info = info ? { ...info, attachmentBytes: -1 } : null;
    }
    // Connected comes from the status; info only supplies the facts.
    connected = status ? status.state !== "off" : (info?.connected ?? false);
    showView();
    renderInfo();
    renderStatusLine();
  }

  // ----- Connect -----
  function resetEye(): void {
    uri.type = "password";
    eye.setAttribute("aria-pressed", "false");
    eye.setAttribute("aria-label", "Show connection string");
    eye.innerHTML = icons.eye;
  }

  uri.addEventListener("input", () => {
    setError(uriError, "");
    renderOff();
  });
  uri.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      void doConnect();
    } else if (e.key === "Escape" && uri.value) {
      // Clear the field instead of closing the window on a half-typed string.
      e.preventDefault();
      e.stopPropagation();
      uri.value = "";
      setError(uriError, "");
      renderOff();
    }
  });
  eye.addEventListener("click", () => {
    const show = uri.type === "password";
    uri.type = show ? "text" : "password";
    eye.setAttribute("aria-pressed", String(show));
    eye.setAttribute("aria-label", show ? "Hide connection string" : "Show connection string");
    eye.innerHTML = show ? icons.eyeOff : icons.eye;
  });
  guide.addEventListener("click", (e) => {
    e.preventDefault();
    void api.openURL(SETUP_GUIDE_URL).catch(() => undefined);
  });
  connect.addEventListener("click", () => void doConnect());

  async function doConnect(): Promise<void> {
    if (busy || isDisabled(connect) || !looksLikeMongoURI(uri.value)) return;
    busy = true;
    setError(uriError, "");
    renderOff();
    try {
      status = await api.sync.connect(uri.value.trim());
    } catch (err) {
      busy = false;
      setError(uriError, errorText(err));
      renderOff();
      uri.focus();
      return;
    }
    busy = false;
    uri.value = "";
    resetEye();
    lastSeen = status.lastSyncAt ?? null;
    connected = true;
    renderOff();
    showView();
    renderStatusLine();
    await refreshInfo();
    connected = true; // connect succeeded; a failed info read must not hide that
    showView();
    nowBtn.focus();
  }

  // ----- Sync now / Disconnect -----
  nowBtn.addEventListener("click", async () => {
    if (syncing || isDisabled(nowBtn)) return;
    syncing = true;
    setError(actionError, "");
    renderStatusLine();
    try {
      await api.sync.now();
    } catch (err) {
      setError(actionError, errorText(err));
    }
    syncing = false;
    renderStatusLine();
  });

  const closeConfirm = (focus: HTMLElement) => {
    confirm.hidden = true;
    actions.hidden = false;
    focus.focus();
  };
  discBtn.addEventListener("click", () => {
    setError(actionError, "");
    actions.hidden = true;
    confirm.hidden = false;
    confirmNo.focus();
  });
  confirmNo.addEventListener("click", () => closeConfirm(discBtn));
  confirm.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      closeConfirm(discBtn);
    }
  });
  confirmYes.addEventListener("click", async () => {
    confirmYes.disabled = true;
    try {
      await api.sync.disconnect();
    } catch (err) {
      confirmYes.disabled = false;
      setError(actionError, errorText(err));
      return;
    }
    confirmYes.disabled = false;
    connected = false;
    status = null;
    info = null;
    lastSeen = null;
    confirm.hidden = true;
    actions.hidden = false;
    setError(actionError, "");
    showView();
    renderOff();
    uri.focus();
  });

  // ----- Live status -----
  onSyncStatus((st) => {
    status = st;
    // The first event of a connection made elsewhere, or an "off", changes
    // what the section shows; and each new sync changes the storage line.
    const nowConnected = st.state !== "off";
    if (nowConnected !== connected || (nowConnected && st.lastSyncAt !== lastSeen)) {
      lastSeen = st.lastSyncAt ?? null;
      void refreshInfo();
    }
    renderStatusLine();
  });
  window.setInterval(() => {
    if (connected) renderStatusLine();
  }, TICK_MS);

  renderOff();
  void (async () => {
    try {
      status = await api.sync.status();
      lastSeen = status.lastSyncAt ?? null;
    } catch {
      status = null;
    }
    await refreshInfo();
  })();
}
