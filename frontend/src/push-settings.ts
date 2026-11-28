// Settings › "Phone push". The switch, server, topic, the two options and the
// grace are part of the form's Save/dirty flow (they sync between computers);
// the token, Copy, New topic and Send test act at once, like Sync's buttons.
// The pure helpers are tested.
import { api, errorText, onPushStatus, type PushInfo, type PushStatus, type Settings } from "./api";
import { el } from "./dom";

const TICK_MS = 30_000;
/** Grace choices in seconds (internal/settings: 0, 2, 5 or 10 minutes). */
const GRACE_OPTIONS = [0, 120, 300, 600];

/** "immediately", "1 minute", "2 minutes" for a grace in milliseconds. */
export function graceLabel(ms: number): string {
  if (ms <= 0) return "immediately";
  const min = Math.round(ms / 60_000);
  return `${min} ${min === 1 ? "minute" : "minutes"}`;
}

/** "just now", "2 min ago", "3 h ago", "2 d ago". */
function ago(t: Date, now: Date): string {
  const min = Math.floor((now.getTime() - t.getTime()) / 60_000);
  if (min < 1) return "just now";
  if (min < 60) return `${min} min ago`;
  if (min < 24 * 60) return `${Math.floor(min / 60)} h ago`;
  return `${Math.floor(min / (24 * 60))} d ago`;
}

/** The status line under the options, for one push status. */
export function pushStatusLine(s: PushStatus, now: Date = new Date()): string {
  switch (s.state) {
    case "off":
      return "Phone push is off";
    case "offline":
      return "Offline · will retry";
    case "ok": {
      const sent = s.lastSent ? new Date(s.lastSent) : null;
      const has = sent !== null && sent.getFullYear() > 2000;
      return `Phone push: ${s.scheduled} scheduled${has ? ` · last sent ${ago(sent, now)}` : ""}`;
    }
    default:
      return s.message || "Phone push has a problem";
  }
}

/** The push fields of the form's draft. */
export interface PushDraft {
  enabled: boolean;
  server: string;
  topic: string;
  replies: boolean;
  hideText: boolean;
  /** Seconds; one of 0, 120, 300, 600. */
  graceSec: number;
}

/** What the section needs from the settings form. */
export interface PushForm {
  /** The draft's push fields; null until settings are loaded. */
  get(): PushDraft | null;
  /** Changes draft fields (marks the form dirty). */
  patch(p: Partial<PushDraft>): void;
  /** Takes settings Go has just stored (a new topic) as both saved and draft. */
  adoptSaved(s: Settings): void;
}

export interface PushSection {
  /** Redraws from the form's draft. */
  render(): void;
  /** Re-reads the push info (after a save). */
  refresh(): Promise<void>;
}

const HELP =
  "The server can read what is sent unless text is hidden. The topic works like a password. " +
  "Alerts are only scheduled while a computer is on in the 3 days before. Self-hosted: ntfy 2.16 or newer.";
const HELP_TOKEN = "A token you save is included in the phone's Done/Snooze buttons; use one that can only post to this topic.";

/** Host part of a server URL, for the consent note. */
function hostOf(server: string): string {
  try {
    return new URL(server).host || server;
  } catch {
    return server;
  }
}

/** Mounts the section into host (an empty element after the settings form). */
export function mountPushSection(host: HTMLElement, form: PushForm): PushSection {
  host.setAttribute("aria-labelledby", "push-title");
  const title = el("h2", "sync-title", "Phone push");
  title.id = "push-title";
  host.append(title);

  const sw = el("input", "switch");
  sw.id = "push-on";
  sw.type = "checkbox";
  sw.setAttribute("role", "switch");
  sw.setAttribute("aria-describedby", "push-intro push-consent");
  const swLabel = el("label", "field-label", "Phone push");
  swLabel.htmlFor = "push-on";
  const swRow = el("div", "field-row field-switch");
  swRow.append(swLabel, sw);
  const intro = el("p", "field-help", "Get reminders on your phone through ntfy, even with this computer closed.");
  intro.id = "push-intro";

  // Consent note: shown when the switch goes on with the text not hidden.
  const consent = el("div", "sync-confirm");
  consent.id = "push-consent";
  consent.setAttribute("role", "group");
  consent.setAttribute("aria-label", "Confirm phone push");
  consent.hidden = true;
  const consentText = el("p", "sync-confirm-text");
  const consentRow = el("div", "field-row");
  const consentYes = el("button", "btn btn-primary", "Turn on");
  consentYes.type = "button";
  const consentNo = el("button", "btn btn-ghost", "Cancel");
  consentNo.type = "button";
  consentRow.append(consentYes, consentNo);
  consent.append(consentText, consentRow);

  const details = el("div", "field");
  details.id = "push-details";
  details.hidden = true;

  // ----- Server and topic -----
  const serverField = el("div", "field");
  const serverLabel = el("label", "field-label", "Server");
  serverLabel.htmlFor = "push-server";
  const server = el("input", "input");
  server.id = "push-server";
  server.type = "url";
  server.spellcheck = false;
  server.autocomplete = "off";
  const serverRow = el("div", "field-row");
  serverRow.append(server);
  serverField.append(serverLabel, serverRow);

  const topicField = el("div", "field");
  const topicLabel = el("label", "field-label", "Topic");
  topicLabel.htmlFor = "push-topic";
  const topic = el("input", "input secret-input");
  topic.id = "push-topic";
  topic.type = "text";
  topic.spellcheck = false;
  topic.autocomplete = "off";
  const copyBtn = el("button", "btn btn-ghost", "Copy");
  copyBtn.type = "button";
  const newBtn = el("button", "btn btn-ghost", "New topic");
  newBtn.type = "button";
  const topicRow = el("div", "secret-box");
  topicRow.append(topic, copyBtn, newBtn);
  const qrRow = el("div", "field-row");
  const qr = el("img", "push-qr");
  qr.alt = "QR code of the subscribe address";
  qr.hidden = true;
  const subscribe = el("p", "field-help");
  qrRow.append(qr, subscribe);
  const topicError = el("p", "field-error");
  topicError.setAttribute("role", "alert");
  topicError.hidden = true;
  topicField.append(topicLabel, topicRow, qrRow, topicError);

  // ----- Token -----
  const tokenField = el("div", "field");
  const tokenLabel = el("label", "field-label", "Access token (this computer)");
  tokenLabel.htmlFor = "push-token";
  const token = el("input", "input secret-input");
  token.id = "push-token";
  token.type = "password";
  token.autocomplete = "off";
  token.spellcheck = false;
  token.setAttribute("aria-describedby", "push-token-help");
  const tokenSave = el("button", "btn btn-ghost", "Save");
  tokenSave.type = "button";
  tokenSave.setAttribute("aria-disabled", "true");
  const tokenForget = el("button", "btn btn-ghost", "Forget");
  tokenForget.type = "button";
  const tokenBox = el("div", "secret-box");
  tokenBox.append(token, tokenSave, tokenForget);
  const tokenStore = el("p", "field-help");
  const tokenHelp = el("p", "field-help", HELP_TOKEN);
  tokenHelp.id = "push-token-help";
  const tokenError = el("p", "field-error");
  tokenError.setAttribute("role", "alert");
  tokenError.hidden = true;
  tokenField.append(tokenLabel, tokenBox, tokenStore, tokenHelp, tokenError);

  // ----- Options -----
  const option = (id: string, label: string) => {
    const input = el("input", "switch");
    input.id = id;
    input.type = "checkbox";
    input.setAttribute("role", "switch");
    const lab = el("label", "field-label", label);
    lab.htmlFor = id;
    const row = el("div", "field-row field-switch");
    row.append(lab, input);
    return { input, row };
  };
  const replies = option("push-replies", "Also push reply nudges and the end-of-day sweep");
  const hide = option("push-hide", "Hide task text on the phone");

  const graceField = el("div", "field");
  const graceLab = el("label", "field-label", "Buzz the phone if not handled here within");
  graceLab.htmlFor = "push-grace";
  const grace = el("select", "input input-select");
  grace.id = "push-grace";
  const graceRow = el("div", "field-row");
  graceRow.append(grace);
  graceField.append(graceLab, graceRow);

  // ----- Test and status -----
  const testBtn = el("button", "btn btn-ghost", "Send test");
  testBtn.type = "button";
  const statusLine = el("span", "sync-fact");
  statusLine.setAttribute("role", "status");
  statusLine.setAttribute("aria-live", "polite");
  const testRow = el("div", "field-row");
  testRow.append(testBtn, statusLine);
  const testNote = el("p", "field-help");
  testNote.setAttribute("role", "status");
  testNote.hidden = true;
  const testError = el("p", "field-error");
  testError.setAttribute("role", "alert");
  testError.hidden = true;

  details.append(serverField, topicField, tokenField, replies.row, hide.row, graceField, testRow, testNote, testError);
  const help = el("p", "field-help", HELP);
  details.append(help);
  host.append(swRow, intro, consent, details);

  let info: PushInfo | null = null;
  let status: PushStatus | null = null;
  let infoError = ""; // why the last info read failed
  let ask: "enable" | "show" | null = null; // which consent note is open

  const setText = (target: HTMLElement, text: string) => {
    target.textContent = text;
    target.hidden = text === "";
  };
  const setDisabled = (b: HTMLButtonElement, v: boolean) => {
    if (v) b.setAttribute("aria-disabled", "true");
    else b.removeAttribute("aria-disabled");
  };
  const isDisabled = (b: HTMLElement) => b.getAttribute("aria-disabled") === "true";

  function renderStatus(): void {
    const s = status ?? info?.status ?? null;
    statusLine.textContent = infoError || (s ? pushStatusLine(s, new Date()) : "");
  }

  function renderInfo(): void {
    const kind = info?.tokenStore ?? "";
    const has = info?.hasToken === true;
    tokenStore.textContent = has && kind ? `Token saved; stored in: ${kind}` : "";
    tokenStore.hidden = !(has && kind);
    setDisabled(tokenForget, !has);
    const url = info?.subscribeUrl ?? "";
    const img = info?.qr ?? "";
    qr.hidden = img === "";
    if (img) qr.src = img;
    subscribe.textContent = url
      ? `Scan with the ntfy app, or subscribe to ${url}.`
      : "Save to create the topic and show its QR code.";
    renderStatus();
  }

  function render(): void {
    const d = form.get();
    if (!d) return;
    sw.checked = d.enabled || ask === "enable";
    details.hidden = !d.enabled;
    server.value = d.server;
    topic.value = d.topic;
    replies.input.checked = d.replies;
    hide.input.checked = d.hideText && ask !== "show";
    const secs = GRACE_OPTIONS.includes(d.graceSec) ? GRACE_OPTIONS : [...GRACE_OPTIONS, d.graceSec].sort((a, b) => a - b);
    grace.replaceChildren(
      ...secs.map((s) => {
        const o = el("option", undefined, graceLabel(s * 1000));
        o.value = String(s);
        return o;
      }),
    );
    grace.value = String(d.graceSec);
    setDisabled(copyBtn, d.topic === "");
    renderInfo();
  }

  async function refresh(): Promise<void> {
    try {
      info = await api.push.info();
      status = info.status;
      infoError = "";
    } catch (err) {
      info = null;
      infoError = errorText(err);
    }
    renderInfo();
  }

  // ----- Form fields -----
  function closeAsk(): void {
    ask = null;
    consent.hidden = true;
  }
  sw.addEventListener("change", () => {
    const d = form.get();
    if (!d) return;
    if (!sw.checked) {
      closeAsk();
      form.patch({ enabled: false });
      render();
      return;
    }
    if (d.hideText) {
      form.patch({ enabled: true });
      render();
      return;
    }
    openAsk("enable", d);
  });
  function openAsk(kind: "enable" | "show", d: PushDraft): void {
    ask = kind;
    consentText.textContent = `Reminder text will be sent to ${hostOf(d.server)}.`;
    consentYes.textContent = kind === "enable" ? "Turn on" : "Send text";
    consent.hidden = false;
    consentYes.focus();
  }
  consentYes.addEventListener("click", () => {
    const kind = ask;
    closeAsk();
    if (kind === "show") form.patch({ hideText: false });
    else form.patch({ enabled: true });
    render();
    (kind === "show" ? hide.input : sw).focus();
  });
  const cancelAsk = () => {
    const kind = ask;
    closeAsk();
    if (kind === "show") {
      hide.input.checked = true;
      hide.input.focus();
    } else {
      sw.checked = false;
      sw.focus();
    }
  };
  consentNo.addEventListener("click", cancelAsk);
  consent.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      cancelAsk();
    }
  });

  server.addEventListener("input", () => form.patch({ server: server.value.trim() }));
  topic.addEventListener("input", () => {
    form.patch({ topic: topic.value.trim() });
    setDisabled(copyBtn, topic.value.trim() === "");
  });
  replies.input.addEventListener("change", () => form.patch({ replies: replies.input.checked }));
  hide.input.addEventListener("change", () => {
    const d = form.get();
    // Un-hiding while push is on starts sending task text: ask first.
    if (d && d.enabled && d.hideText && !hide.input.checked) {
      openAsk("show", d);
      return;
    }
    closeAsk();
    form.patch({ hideText: hide.input.checked });
  });
  grace.addEventListener("change", () => form.patch({ graceSec: Number(grace.value) }));

  // ----- Immediate actions -----
  copyBtn.addEventListener("click", async () => {
    if (isDisabled(copyBtn)) return;
    try {
      await navigator.clipboard.writeText(topic.value);
      setText(topicError, "");
    } catch (err) {
      setText(topicError, `Couldn't copy: ${errorText(err)}`);
    }
  });
  newBtn.addEventListener("click", async () => {
    if (isDisabled(newBtn)) return;
    setDisabled(newBtn, true);
    setText(topicError, "");
    try {
      form.adoptSaved(await api.push.newTopic());
      render();
      await refresh();
    } catch (err) {
      setText(topicError, errorText(err));
    }
    setDisabled(newBtn, false);
  });

  token.addEventListener("input", () => {
    setText(tokenError, "");
    setDisabled(tokenSave, token.value.trim() === "");
  });
  token.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      void saveToken();
    }
  });
  async function saveToken(): Promise<void> {
    if (isDisabled(tokenSave)) return;
    setText(tokenError, "");
    setDisabled(tokenSave, true);
    try {
      await api.push.setToken(token.value);
      token.value = "";
      await refresh();
    } catch (err) {
      setDisabled(tokenSave, token.value.trim() === "");
      setText(tokenError, errorText(err));
    }
  }
  tokenSave.addEventListener("click", () => void saveToken());
  tokenForget.addEventListener("click", async () => {
    if (isDisabled(tokenForget)) return;
    setText(tokenError, "");
    try {
      await api.push.forgetToken();
      await refresh();
    } catch (err) {
      setText(tokenError, errorText(err));
    }
  });

  testBtn.addEventListener("click", async () => {
    if (isDisabled(testBtn)) return;
    setDisabled(testBtn, true);
    setText(testError, "");
    setText(testNote, "");
    try {
      await api.push.test();
      setText(testNote, "Test sent. It uses the saved settings.");
    } catch (err) {
      setText(testError, errorText(err));
    }
    setDisabled(testBtn, false);
  });

  // ----- Live status -----
  onPushStatus((s) => {
    status = s;
    infoError = "";
    renderStatus();
  });
  window.setInterval(() => renderStatus(), TICK_MS);

  void refresh();
  return { render, refresh };
}
