// Settings › "Data folder": where the database, images and voice files live.
// Like the sync and voice sections it sits outside the form's Save flow: its
// buttons act at once. Changing the folder restarts the app (Go quits, main
// relaunches); setup mode then copies the data with progress.
import { api, errorText, type DataFolderInfo, type SetupCheck } from "./api";
import { isConfirmAdopt } from "./confirm-errors";
import { el } from "./dom";
import { icons } from "./icons";

const NETWORK_WARNING = "Network folders can damage the database if the connection drops.";
const CLOUD_WARNING = "Cloud-synced folders can damage the database or create conflicting copies. Sync across machines with Atlas instead.";
const SAME_FOLDER = "This is already your data folder.";

const TEMPLATE = `
<h2 class="sync-title" id="data-folder-title">Data folder</h2>
<p class="field-help">Changes here restart Awesome Reminder.</p>
<p class="data-folder-path" id="data-folder-path"></p>
<p class="field-help" id="data-folder-usage"></p>
<div class="field-row" id="data-folder-actions">
  <button class="btn btn-ghost" id="data-folder-open" type="button">Open folder</button>
  <button class="btn btn-ghost" id="data-folder-change" type="button">Change…</button>
</div>
<div class="data-folder-panel" id="data-folder-panel" role="group" aria-label="Change data folder" hidden></div>
<div class="data-folder-old" id="data-folder-old" hidden>
  <p class="field-help" id="data-folder-old-text"></p>
  <div class="field-row" id="data-folder-old-actions">
    <button class="btn btn-ghost" id="data-folder-remove" type="button">Remove old copy</button>
  </div>
  <div class="data-folder-panel" id="data-folder-old-confirm" role="group" aria-label="Remove old copy" hidden></div>
</div>
<p class="field-error" id="data-folder-error" role="alert" hidden></p>`;

function button(label: string, cls: string, onClick: () => void): HTMLButtonElement {
  const b = el("button", `btn ${cls}`, label);
  b.type = "button";
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

/** Mounts the section into host (an empty element after the settings form). */
export function mountDataFolderSection(host: HTMLElement): void {
  host.setAttribute("aria-labelledby", "data-folder-title");
  host.innerHTML = TEMPLATE;
  const $ = <T extends HTMLElement>(id: string) => host.querySelector<T>(`#data-folder-${id}`)!;
  const pathEl = $("path");
  const usage = $("usage");
  const openBtn = $<HTMLButtonElement>("open");
  const changeBtn = $<HTMLButtonElement>("change");
  const panel = $("panel");
  const oldBox = $("old");
  const oldText = $("old-text");
  const oldActions = $("old-actions");
  const removeBtn = $<HTMLButtonElement>("remove");
  const oldConfirm = $("old-confirm");
  const errBox = $("error");

  let info: DataFolderInfo | null = null;
  let busy = false; // a change or removal is in flight, or the app is restarting

  function setError(text: string): void {
    errBox.textContent = text;
    errBox.hidden = text === "";
  }

  function render(): void {
    pathEl.textContent = info?.path ?? "";
    const parts: string[] = [];
    if (info?.usedText) parts.push(`Uses ${info.usedText}`);
    if (info?.freeText) parts.push(info.freeText);
    usage.textContent = parts.join(" · ");
    usage.hidden = parts.length === 0;
    const prev = info?.previousDir ?? "";
    oldBox.hidden = prev === "";
    oldText.textContent = prev === "" ? "" : `Old copy at ${prev}${info?.previousText ? ` (${info.previousText})` : ""}`;
    changeBtn.disabled = busy;
    removeBtn.disabled = busy;
  }

  async function refresh(): Promise<void> {
    try {
      info = await api.dataFolder.info();
    } catch (err) {
      setError(errorText(err));
    }
    render();
  }

  // ----- change -----

  function closePanel(focus: HTMLElement | null): void {
    panel.hidden = true;
    panel.replaceChildren();
    focus?.focus();
  }

  function showPanelError(text: string): void {
    const row = el("div", "field-row");
    const close = button("Close", "btn-ghost", () => closePanel(changeBtn));
    row.append(close);
    panel.replaceChildren(note(text, "error"), row);
    panel.hidden = false;
    close.focus();
  }

  function showConfirm(c: SetupCheck): void {
    const adopt = c.hasData;
    panel.replaceChildren();
    panel.append(
      el(
        "p",
        "data-folder-confirm-text",
        adopt
          ? "This folder already has Awesome Reminder data. Awesome Reminder will restart and use that data. Your current data stays where it is."
          : `Awesome Reminder will restart to copy your data to ${c.path}. Your current data stays where it is as a backup.`,
      ),
    );
    if (adopt) {
      panel.append(el("p", "data-folder-path", c.path));
    }
    if (c.network) panel.append(note(NETWORK_WARNING, "warn"));
    if (c.cloud) panel.append(note(CLOUD_WARNING, "warn"));
    const go = button(adopt ? "Restart and use that data" : "Restart and move", "btn-primary", () => void apply(c, go));
    const cancel = button("Cancel", "btn-ghost", () => closePanel(changeBtn));
    const row = el("div", "field-row");
    row.append(cancel, go);
    panel.append(row);
    panel.hidden = false;
    cancel.focus();
  }

  async function apply(c: SetupCheck, go: HTMLButtonElement): Promise<void> {
    busy = true;
    go.disabled = true;
    render();
    try {
      await api.dataFolder.change(c.path, c.hasData);
      go.textContent = "Restarting…"; // Go quits the app; the window closes with it
      return;
    } catch (err) {
      const text = errorText(err);
      busy = false;
      render();
      if (!c.hasData && isConfirmAdopt(text)) {
        // The folder gained data since it was checked: look again and ask properly.
        await check(c.path);
        return;
      }
      showPanelError(text);
    }
  }

  async function check(picked: string): Promise<void> {
    let c: SetupCheck;
    try {
      c = await api.dataFolder.check(picked);
    } catch (err) {
      showPanelError(errorText(err));
      return;
    }
    if (c.err) showPanelError(c.err);
    else if (c.same) showPanelError(SAME_FOLDER);
    else showConfirm(c);
  }

  changeBtn.addEventListener("click", () => {
    if (busy) return;
    setError("");
    void (async () => {
      let picked = "";
      try {
        picked = await api.chooseFolder("Choose the data folder");
      } catch (err) {
        setError(errorText(err));
        return;
      }
      if (picked === "") return;
      await check(picked);
    })();
  });
  panel.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !busy) {
      e.preventDefault();
      e.stopPropagation();
      closePanel(changeBtn);
    }
  });

  openBtn.addEventListener("click", () => {
    setError("");
    api.dataFolder.open().catch((err) => setError(errorText(err)));
  });

  // ----- old copy -----

  function closeOldConfirm(focus: HTMLElement | null): void {
    oldConfirm.hidden = true;
    oldConfirm.replaceChildren();
    oldActions.hidden = false;
    focus?.focus();
  }

  removeBtn.addEventListener("click", () => {
    if (busy) return;
    setError("");
    oldActions.hidden = true;
    const del = button("Delete", "btn-ghost btn-danger-text", () => void remove(del));
    const cancel = button("Cancel", "btn-ghost", () => closeOldConfirm(removeBtn));
    const row = el("div", "field-row");
    row.append(del, cancel);
    oldConfirm.replaceChildren(el("p", "data-folder-confirm-text", "Delete the old copy? This can't be undone."), row);
    oldConfirm.hidden = false;
    cancel.focus();
  });
  oldConfirm.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !busy) {
      e.preventDefault();
      e.stopPropagation();
      closeOldConfirm(removeBtn);
    }
  });

  async function remove(del: HTMLButtonElement): Promise<void> {
    busy = true;
    del.disabled = true;
    render();
    let failed = "";
    try {
      await api.dataFolder.removeOldCopy();
    } catch (err) {
      failed = errorText(err);
    }
    busy = false;
    closeOldConfirm(null);
    setError(failed);
    await refresh();
    if (!failed) changeBtn.focus();
    else if (!oldBox.hidden) removeBtn.focus();
  }

  render();
  void refresh();
}
